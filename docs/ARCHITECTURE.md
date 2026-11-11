# Architecture — Docker-free PocketHost

This document explains how the Go implementation replaces the container-based
orchestrator, and which invariants each layer must preserve.

## 1. The control plane is the original mothership

The most important decision in this migration: **the control plane is not
reimplemented**. The upstream PocketHost mothership application
(`packages/server/src/mothership-app`) is a PocketBase app whose logic lives in
JavaScript hooks. Those hooks are embedded **verbatim** into this binary and
executed by the pure-Go PocketBase SDK:

```
pocketghost (single static binary)
|
├── mothership app  <- github.com/pocketbase/pocketbase (pure Go)
|   ├── pb_hooks/mothership.pb.js   <- ORIGINAL, byte-for-byte
|   ├── pb_hooks/mothership.js      <- ORIGINAL, byte-for-byte
|   ├── pb_migrations/*.js          <- ORIGINAL, all 21 files
|   └── pb_admin_ext/live/          <- ORIGINAL admin extension
|        -> 32 routes, realtime, collection rules, billing hooks,
|           Cloudflare sweeps, notifications, admin UI
|
└── edge daemon  <- this codebase
    ├── native process supervisor (internal/procs)
    ├── reverse proxy + rate limiter (internal/proxy, internal/ratelimit)
    ├── file services (internal/ftp, internal/sftp)
    └── scheduler (internal/scheduler)
```

Why this works without Node.js:

* The hooks contain **no** `require('fs'|'crypto'|'path'|'os')` calls — only
  `require(`${__hooks}/mothership`)`, which PocketBase's JSVM resolves.
* They use only PocketBase's own `$app` / `$os` / `$apis` globals, which the
  JSVM provides.
* `plugins/jsvm` runs them in goja, a pure-Go JavaScript runtime.
* The SDK already depends on `modernc.org/sqlite`, so the whole stack stays
  **CGO-free**.

Two tests guard this invariant (`internal/mothership/mothership_test.go`):

* `TestHooksAreNodeFree` / `TestMigrationsAreNodeFree` fail if a Node built-in
  ever appears in the embedded assets.
* `TestMothershipBootsWithOriginalHooks` boots the app, asserts all 21 JS
  migrations applied, the collections exist with their original fields, and
  that the original routes answer over HTTP.

### Boot ordering

PocketBase fires `OnBootstrap` handlers in *reverse* registration order, so the
JS `onBootstrap` handler would run **before** the core bootstrap that applies
the migrations creating the `instances` table — which the original hook
queries with raw SQL (`refreshPublicStats`).

To keep the hooks unmodified, the boot sequence is:

1. `applyMigrations()` — a throwaway app with an **empty hooks directory**
   applies the system + JS migrations.
2. `core.Bootstrap()` — the real app (with hooks) boots; the collections now
   exist, so the original hook's `onBootstrap` handler succeeds.

## 2. Tenant process isolation

`internal/procs.Process` starts a PocketBase binary with:

```
<binPath> serve --http 127.0.0.1:<port> [--dev] [--encryptionEnv=PB_ENCRYPTION_KEY]
```

Isolation properties and how each is achieved without a container runtime:

| Property | Mechanism |
| --- | --- |
| Working directory | `cmd.Dir = <DATA_ROOT>/instances/<id>` (unique per tenant) |
| Data directory | PocketBase's default `pb_data` inside that workdir |
| Network exposure | the process binds `127.0.0.1` only, so it is unreachable from outside the host |
| Port | exclusive allocation from a configured pool, persisted in `processes` |
| Process group | `SysProcAttr{Setpgid: true}`; stop/kill signals target `-pgid` so helpers die too |
| File descriptors | `RLIMIT_NOFILE` via `prlimit` on the child PID |
| Memory | `GOMEMLIMIT=<MB>MiB` (correct for Go; `RLIMIT_AS` breaks the Go runtime) |
| CPU | `GOMAXPROCS` from `PH_INSTANCE_CPU_PERCENT` + lower scheduling priority |
| Disk | `PH_INSTANCE_DISK_MB` is enforced by the quota/archival jobs |
| Logs | stdout/stderr piped to `<workdir>/logs/exec.log` as NDJSON `{message,stream,time}` |
| Env secrets | instance `secrets` injected; control-plane credentials explicitly stripped |

### Lifecycle state machine

```
            EnsureRunning                   healthy
  idle ─────────────────▶ starting ─────────────────▶ running
    ▲                        │                          │
    │                        │ spawn/health error       │ idle TTL reached
    │                        ▼                          ▼
    └──────────────────── failed ◀── crash ──── shutdown (SIGINT → SIGKILL)
```

* The `processes` table is updated on every transition, so a control-plane restart
  can reconcile: a PID that is alive **and** answers `/api/health` on its recorded
  port is re-adopted, otherwise the row is marked `stopped`.
* Restarts are bounded (5 attempts, exponential backoff 0.5s→30s).
* Hibernation uses an open-request refcount plus a last-request timestamp,
  evaluated every second (`DAEMON_PB_IDLE_TTL`).

## 3. Request path

1. **Edge** (`pocketghost firewall`): `proxy.CORS` → `ratelimit.Middleware` →
   daemon handler (or a remote daemon when `PH_EDGE_INTERNAL_URL` points at
   another node).
2. **Daemon** (`pocketghost serve`): `app.DaemonHandler` checks whether the
   `Host` header maps to an instance.
   * instance host → `proxy.Handler` → `instance.Service.Resolve` → ensure running
     → `httputil.ReverseProxy` with `FlushInterval: -1`.
   * control-plane host → `api.Server` routes → embedded Svelte app.
3. **Proxy details**
   * `Rewrite` preserves the original `Host` header (PocketBase builds absolute
     URLs from it) and sets `X-Forwarded-*`.
   * `FlushInterval: -1` keeps SSE/realtime subscription streams unbuffered.
   * WebSocket upgrades pass through natively.
   * Upstream `Access-Control-Allow-Origin: *` is rewritten to the request origin
     with credentials allowed, as the legacy middleware did.


## 4. Metadata database

The database is PocketBase's own (`<PH_HOME>/mothership/pb_data/data.db`), so the
collections are exactly the ones the original mothership defined — `users`,
`instances`, `edges`, `regions`, `ssh_keys`, `api_tokens`, `campaigns`, ... —
with PocketBase-compatible record ids (`[a-z0-9]{15}`) and timestamps
(`YYYY-MM-DD HH:MM:SS.mmmZ`).

The only addition is the `processes` collection, created by a Go migration
(`internal/mothership/migrations.go`). It replaces Docker's container list: it is
the durable record of which PocketBase OS processes should exist and how to
re-attach to them after a supervisor restart.

### Legacy import

```bash
pocketghost migrate --from /var/lib/pockethost/data/mothership/pb_data/data.db
```

The legacy build already produced a PocketBase database, so the schema matches
exactly: the importer copies the rows of `users`, `instances`, `edges`,
`ssh_keys` and `api_tokens` through PocketBase's record API, preserving the
original record ids so existing links stay valid. It is idempotent (a record
that already exists is skipped).

## 5. Filesystem layout

```
$PH_HOME/
├── mothership/               the control plane (original app)
|   ├── pb_hooks/             original JS hooks
|   ├── pb_migrations/        original migrations
|   └── pb_data/data.db       control-plane database
├── pocketbase/<ver>/<os_arch>/pocketbase
├── ssl/tls.{cert,key}        dev certificate (or ACME cache in ssl/acme)
└── ssh/host_key              SFTP host key

$DATA_ROOT/
├── instances/<instance-id>/
│   ├── pb_data/              PocketBase database + uploads
│   ├── pb_hooks/  pb_migrations/  pb_public/
│   ├── logs/exec.log         NDJSON log
│   └── pocketbase.pid
├── backups/<instance-id>/@auto_pb_backup_ph_<stamp>.zip
├── users/<uid>/<subdomain> → instances/<instance-id>   (FTP/SFTP jail)
└── archive/                  archived instances
```

## 6. Rate limiting model

Two windows are enforced per request, exactly like the legacy firewall:

| Scope | Untrusted | Trusted |
| --- | --- | --- |
| IP hourly | 1 000 API requests | 5 000 |
| Instance hourly | 10 000 API requests | 20 000 |
| IP concurrent | 15 | 50 |
| Instance concurrent | 250 | 250 |

Internally a request costs 10 "micro points" while a `/api/files/...` request
costs 1, so a file download spends 10 % of an API request. Operator overrides
(`instances.firewall`) replace the untrusted caps and raise the trusted floor.
Responses always carry the `X-PocketHost-RateLimit-*` headers.

## 7. Security notes

* Tenant processes only listen on `127.0.0.1`; the only public listener is the
  firewall.
* Instance secrets are validated against `/^[A-Z][A-Z0-9_]*$/` and never logged.
* `MOTHERSHIP_ADMIN_*`, `PH_SECRET`, Lemon Squeezy and Discord credentials are
  stripped from the tenant environment.
* FTP/SFTP jails resolve symlinks and reject any path escaping the user root.
* JWT signing uses HS256 with a key derived from `PH_SECRET` (or a generated
  per-installation secret stored in `kv`).
