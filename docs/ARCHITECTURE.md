# Architecture — Docker-free PocketHost

This document explains how the Go implementation replaces the container-based
orchestrator, and which invariants each layer must preserve.

## 1. Control plane vs data plane

| Layer | Legacy (TypeScript) | This implementation |
| --- | --- | --- |
| Control plane | Express 5 + mothership PocketBase hooks | `internal/api` (Go `net/http`) + `internal/store` |
| Tenant runtime | one container per instance (`dockerode`) | native OS process per instance (`internal/procs`) |
| Port mapping | ephemeral `8090/tcp → host:0` | deterministic loopback pool (`PH_INSTANCE_PORT_MIN..MAX`) |
| Health checks | container inspect + HTTP probe | `GET /api/health` probe + `kill(pid, 0)` |
| Restart policy | external/daemon logic | bounded backoff restarts inside the supervisor |
| Reverse proxy | `httpxy` / `http-proxy-middleware` | `net/http/httputil.ReverseProxy` |
| Rate limiting | `rate-limiter-flexible` + `bottleneck` | `internal/ratelimit` fixed windows + counters |
| Metadata DB | `better-sqlite3` | `modernc.org/sqlite` (pure Go, CGO-free) |
| Cron | `cron` NPM package / pm2 | `robfig/cron/v3` in-process |
| TLS | `devcert` + certbot files | `crypto/x509` self-signed + `autocert` (ACME) |
| Log tailing | `tail` NPM package | NDJSON writer + subscriber fan-out |
| FTP/SFTP | `ftp-srv` + `ssh2` | `internal/ftp`, `internal/sftp` (`x/crypto/ssh`) |
| Files | bind mounts + `InstanceVfs` | `internal/vfs` jailed filesystem |

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

SQLite tables mirror the PocketBase collections the platform used, with
PocketBase-compatible record ids (`[a-z0-9]{15}`) and timestamps
(`YYYY-MM-DD HH:MM:SS.mmmZ`):

`users`, `instances`, `processes`, `edges`, `versions`, `ssh_keys`,
`api_tokens`, `deploys`, `notifications`, `audit_logs`, `kv`,
`schema_migrations`.

`processes` replaces Docker's container list. `kv` stores the generated JWT
signing key when `PH_SECRET` is not configured.

### Legacy import

```bash
pocketghost migrate --from /var/lib/pockethost/data/mothership/pb_data/data.db
```

The importer copies the intersection of source and destination columns for
`users`, `instances`, `edges`, `ssh_keys`, `api_tokens` and `versions`, mapping
the PocketBase 0.22 `passwordHash` column onto `password`. It runs in a single
transaction and is idempotent (`insert or replace`), so it can be re-run.

## 5. Filesystem layout

```
$PH_HOME/
├── pocketghost.db            metadata database
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
