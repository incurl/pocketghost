# Migration guide — TypeScript/Docker → Go/native

This document maps every module of the upstream PocketHost monorepo to its Go
replacement, states what is fully ported, and lists the intentional differences.

## 1. Module map

| Upstream module (TypeScript) | Go package | Status |
| --- | --- | --- |
| `core/Settings.ts`, `constants.ts`, `core/env.ts` | `internal/config`, `internal/phome` | ✅ same env names + defaults |
| `common/Logger.ts`, `common/ConsoleLogger.ts` | `internal/logging` | ✅ same level/breadcrumb model |
| `services/InstanceLoggerService` | `internal/logging/instance_logs.go` | ✅ NDJSON logs, rotation, live tail |
| `services/InstanceService` | `internal/instance` | ✅ lifecycle, hibernation, deploy lock, status sync |
| `services/PocketBaseService` | `internal/instance` + `internal/procs` | ✅ native spawn/stop/health/attach |
| `core/dockerInstance.ts` | `internal/procs` | ✅ replaced by OS process management |
| `services/PocketBaseBinaryService` | `internal/pbbin` | ✅ cache/download/sha256/semver |
| `services/ProxyService.ts` | `internal/proxy` | ✅ HTTP, WebSocket, SSE, CORS rewrite |
| `FirewallCommand/.../rate-limiter.ts` | `internal/ratelimit` | ✅ same limits, weights and headers |
| `FirewallCommand/.../server.ts` | `internal/app/handlers.go` | ✅ host routing (local or remote daemon) |
| `services/RealtimeLog.ts`, `EdgeTrafficStats.ts` | `internal/metrics` | ✅ counters + top-N snapshot |
| `better-sqlite3` usage | `internal/mothership` (PocketBase's own SQLite) | ✅ `modernc.org/sqlite`, CGO-free |
| `cron` package + pm2 cron entries | `internal/scheduler` | ✅ backups, reconcile, retention |
| backup webhook `_ph_backup.pb.js` | `internal/backup` | ✅ zip create/list/restore + retention |
| `devcert` | `internal/tlscert` | ✅ self-signed dev cert (x509) |
| certbot / `SSL_CERT` files | `internal/tlscert` | ✅ ACME via `autocert` or static files |
| `ftp-srv` | `internal/ftp` | ✅ FTP + explicit FTPS (PASV/EPSV) |
| `ssh2` SftpService | `internal/sftp` | ✅ SFTP v3 subsystem over `x/crypto/ssh` |
| `InstanceVfs` | `internal/vfs` | ✅ jailed, symlink-safe |
| Commander CLI | `cmd/pocketghost` (cobra) | ✅ serve/firewall/instance/backup/migrate/status |
| Express static dashboard | `internal/api` + `internal/dashboard` | ✅ embedded SPA with 404 fallback |
| mothership hooks (`pb_hooks`, ~50k LOC) | `internal/mothership` — the **original hooks**, embedded verbatim and run by the Go PocketBase SDK (goja) | ✅ unmodified, all 32 routes |

## 2. REST surface

The control-plane routes are served by the **original mothership hooks** running
inside the PocketBase SDK, so they are byte-compatible with the legacy build:

```
POST   /api/instance                            (create)
PUT    /api/instance/{id}
DELETE /api/instance/{id}
GET    /api/versions
GET    /api/signup?instanceName=
POST   /api/signup
GET    /api/health
GET    /api/userToken/{id}                      (superuser)
GET    /api/user/trusted-ips                    (auth)
PATCH  /api/user/trusted-ips                    (auth)
POST   /api/tokens, PATCH /api/tokens/{id}      (PATs)
POST   /api/pat/verify, /api/pat/whoami, /api/pat/instances
GET    /api/mirror, POST /api/mirror             (edge sync)
GET    /api/edge/heartbeat
POST   /api/admin/live/platform/refresh
GET    /stats.json
GET    /_ph/archive/{id}, /_ph/backup/{id}/{zip}
```

PocketBase itself provides the collection APIs the dashboard uses:

```
POST   /api/collections/users/auth-with-password
POST   /api/collections/users/auth-refresh
POST   /api/collections/users/records
GET    /api/collections/users/records/{id}
PATCH  /api/collections/users/records/{id}
GET    /api/collections/instances/records
GET    /api/collections/instances/records/{id}
POST   /api/realtime                            (SSE subscriptions)
```

Plus the whole PocketBase surface: files, batch, logs, settings, backups and
the superuser admin UI at `/_/`.

**Edge admin API** (guarded by `PH_SECRET`), added by this codebase:

```
GET    /_api/daemon/health
GET    /_api/daemon/version
GET    /_api/daemon/processes
GET    /_api/daemon/instances
POST   /_api/daemon/instances/{id}/start|stop|restart
GET    /_api/daemon/instances/{id}/logs[?lines=&target=]
GET    /_api/daemon/instances/{id}/logs/stream        (SSE)
POST   /_api/daemon/instances/{id}/backup
GET    /_api/daemon/instances/{id}/backups
GET    /_ph/backup/{id}/{zip}
```

## 3. Feature parity matrix

Legend: ✅ ported · ⚠️ partial (see notes) · ⛔ out of scope

| Capability | Status | Notes |
| --- | --- | --- |
| Multi-tenant instance CRUD | ✅ | plan limits, name policy, power rules enforced server-side |
| Native process start/stop/restart | ✅ | integration tested |
| Crash auto-restart with backoff | ✅ | integration tested |
| Idle hibernation | ✅ | integration tested |
| Port pool allocation + persistence | ✅ | restart-safe |
| Dynamic reverse proxy (HTTP) | ✅ | tested |
| WebSocket / realtime passthrough | ✅ | upgrade test included |
| SSE streaming | ✅ | `FlushInterval: -1` |
| CORS for credentialed requests | ✅ | origin echo + credentials |
| Hourly + concurrent rate limits | ✅ | same numbers/headers |
| Trusted IPs (account + CIDR) | ✅ | from `users.trusted_ips` |
| Instance firewall overrides | ✅ | `instances.firewall` JSON |
| TLS (dev self-signed) | ✅ | generated once, reused |
| TLS (ACME / Let's Encrypt) | ✅ | `autocert`, `PH_TLS_ACME=true` |
| Logs: write, rotate, tail, SSE | ✅ | NDJSON identical to legacy |
| Backups: scheduled + manual + retention | ✅ | stops instance for consistency |
| Backup restore | ✅ | CLI + library |
| Cron scheduling | ✅ | in-process, no pm2 |
| Instance file jail (FTP/SFTP) | ✅ | symlink + traversal safe |
| Legacy SQLite import | ✅ | idempotent, transactional |
| Dashboard embedding | ✅ | `make dashboard` |
| Instance secrets + `PB_ENCRYPTION_KEY` | ✅ | `--encryptionEnv` passthrough |
| `ADMIN_SYNC` superuser seeding | ✅ | same JSON payload |
| `PH_BACKUP_TOKEN` | ✅ | identical HMAC derivation |
| Mothership mirror sync (realtime) | ✅ | the original `/api/mirror` + realtime hooks run unchanged in the SDK |
| Edge affinity / multi-region placement | ✅ | the original `edges`/`regions` collections and placement hooks run unchanged |
| Lemon Squeezy billing webhooks | ✅ | the original `lemon` handlers run unchanged (they call the LS API over HTTP) |
| Outbound mail (SES/SMTP) | ✅ | the original `mail` handlers and `$app.newMailClient` run unchanged |
| Personal access tokens (`/api/pat/*`) | ✅ | the original `apiTokens` handlers run unchanged |
| SSH key upload API | ✅ | the original `sshKeys` handlers run unchanged; the edge consumes them for SFTP auth |
| Public live-stats realtime topics | ✅ | the original `live` admin plugin + realtime topics run unchanged |
| `pockethost` end-user CLI (deploy/sync) | ⛔ | separate upstream package, not part of this port |

### The mothership is embedded, not reimplemented

The upstream mothership application is ~50k lines of TypeScript compiled to
JavaScript hooks. Rather than port it to Go, this migration **embeds the
compiled hooks verbatim** (`internal/mothership/assets/`) and runs them inside
the pure-Go PocketBase SDK via its goja-based JSVM.

Consequences:

* All 32 routes, the realtime service, collection rules, the billing hooks,
  the Cloudflare sweeps, the notification pipeline and the admin UI work
  **exactly as before**, with no behavioural difference to test against.
* No Node.js, npm or TypeScript at runtime. The JS is data, not a dependency.
* Two guards keep it honest: `TestHooksAreNodeFree` fails if a Node built-in
  ever appears in the embedded assets, and `TestMothershipBootsWithOriginalHooks`
  asserts the app boots, applies all 21 migrations and answers on the original
  routes.

### Intentional differences

1. **No container isolation.** Processes are isolated by directory, port,
   process group and rlimits, not namespaces. A tenant that deliberately abuses
   the filesystem could read other tenants' files if the OS user allows it —
   deploy under a dedicated unprivileged user (see DEPLOY.md) for defence in
   depth, or run one instance per system user.
2. **The mothership runs in-process.** The original deployment ran it as a
   separate PocketBase container. Here it is a PocketBase app inside the same
   binary, listening on `MOTHERSHIP_PORT` on loopback. A multi-node deployment
   that keeps a central mothership can still point an edge at it with
   `PH_EDGE_INTERNAL_URL`; the edge's own copy of the app then acts as the
   local cache the original `MothershipMirrorService` provided.
3. **Billing and mail are interfaces** rather than baked-in HTTP clients. No
   payment SDK is linked into the binary.
4. **`DAEMON_PB_IDLE_TTL` semantics** are preserved; hibernation additionally
   persists `idle` in SQLite so a restart shows the correct state.

## 4. Cut-over checklist

1. **Freeze writes** on the old deployment (power off instances to flush the SQLite WAL).
2. **Copy data**: `rsync -a /var/lib/pockethost/data/ $DATA_ROOT/`.
3. **Import metadata**:
   `pocketghost migrate --from $DATA_ROOT/mothership/pb_data/data.db`
4. **Warm the binary cache**: `pocketghost pocketbase download <version>` for each
   version your tenants use (or copy `$PH_HOME/pocketbase` and run
   `pocketghost pocketbase list`).
5. **Start** `pocketghost serve` and verify `curl localhost:3000/_api/daemon/health`.
6. **Smoke a tenant** by requesting `http://<subdomain>.<apex>:3000/api/health`
   and confirming a PID appears in `pocketghost instance list`.
7. **Cut traffic** at the firewall/edge, then enable `pocketghost firewall`.
8. Keep the old database until backups from the new system are verified.

## 5. Rolling back

The Go implementation never mutates the legacy database (it only reads it during
`migrate`). Per-instance `pb_data` directories are written by PocketBase itself,
so restarting the old containers against the same `DATA_ROOT` works unchanged.
