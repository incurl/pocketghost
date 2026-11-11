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
| `better-sqlite3` usage | `internal/store` | ✅ `modernc.org/sqlite`, CGO-free |
| `cron` package + pm2 cron entries | `internal/scheduler` | ✅ backups, reconcile, retention |
| backup webhook `_ph_backup.pb.js` | `internal/backup` | ✅ zip create/list/restore + retention |
| `devcert` | `internal/tlscert` | ✅ self-signed dev cert (x509) |
| certbot / `SSL_CERT` files | `internal/tlscert` | ✅ ACME via `autocert` or static files |
| `ftp-srv` | `internal/ftp` | ✅ FTP + explicit FTPS (PASV/EPSV) |
| `ssh2` SftpService | `internal/sftp` | ✅ SFTP v3 subsystem over `x/crypto/ssh` |
| `InstanceVfs` | `internal/vfs` | ✅ jailed, symlink-safe |
| Commander CLI | `cmd/pocketghost` (cobra) | ✅ serve/firewall/instance/backup/migrate/status |
| Express static dashboard | `internal/api` + `internal/dashboard` | ✅ embedded SPA with 404 fallback |
| mothership hooks (`pb_hooks`, ~50k LOC) | `internal/api` handlers | ⚠️ core flows ported (see matrix) |

## 2. REST surface

**Auth / users**

```
POST   /api/collections/users/auth-with-password
POST   /api/collections/users/auth-refresh
POST   /api/collections/users/records
GET    /api/collections/users/records/{id}
PATCH  /api/collections/users/records/{id}
POST   /api/collections/users/request-verification
POST   /api/collections/users/confirm-verification
POST   /api/collections/users/request-password-reset
POST   /api/collections/users/confirm-password-reset
POST   /api/collections/users/request-email-change
POST   /api/collections/users/confirm-email-change
POST   /api/collections/_superusers/auth-with-password
```

**Instances**

```
GET    /api/collections/instances/records?filter=&expand=uid&page=&perPage=
GET    /api/collections/instances/records/{id}
POST   /api/instance
PUT    /api/instance/{id}
DELETE /api/instance/{id}
```

**Misc**

```
GET    /api/versions
GET    /api/userToken/{id}        (superuser)
GET    /stats.json
GET    /api/signup?instanceName=
POST   /api/signup
GET    /api/health
GET    /_api/daemon/health
```

**Daemon admin** (guarded by `PH_SECRET` or a superuser token)

```
GET    /_api/daemon/processes
GET    /_api/daemon/instances
POST   /_api/daemon/instances/{id}/start|stop|restart
GET    /_api/daemon/instances/{id}/logs[?lines=&target=]
GET    /_api/daemon/instances/{id}/logs/stream        (SSE)
POST   /_api/daemon/instances/{id}/backup
GET    /_api/daemon/instances/{id}/backups
GET    /_ph/backup/{id}/{zip}
GET    /_api/daemon/version
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
| Mothership mirror sync (realtime) | ⚠️ | replaced by direct DB reads; the single-binary deployment is authoritative |
| Edge affinity / multi-region placement | ⚠️ | `edges` table + `PH_EDGE_*` supported; placement is operator-driven |
| Lemon Squeezy billing webhooks | ⛔ | no payment SDK linked; see below |
| Outbound mail (SES/SMTP) | ⚠️ | `api.Mailer` interface; the default implementation logs the link |
| Personal access tokens (`/api/pat/*`) | ⚠️ | storage present, verification route not wired |
| SSH key upload API | ⚠️ | storage + SFTP auth present, REST upload route not wired |
| Public live-stats realtime topics | ⚠️ | `/stats.json` served; realtime topics not implemented |
| `pockethost` end-user CLI (deploy/sync) | ⛔ | separate upstream package, not part of this port |

### Intentional differences

1. **No container isolation.** Processes are isolated by directory, port,
   process group and rlimits, not namespaces. A tenant that deliberately abuses
   the filesystem could read other tenants' files if the OS user allows it —
   deploy under a dedicated unprivileged user (see DEPLOY.md) for defence in
   depth, or run one instance per system user.
2. **Mothership data is local.** The original edge mirrored the mothership
   PocketBase over realtime + `/api/mirror`. This port reads the metadata
   database directly, removing an entire sync failure mode. If you keep a
   separate mothership, run `pocketghost migrate` periodically or share the same
   SQLite file.
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
