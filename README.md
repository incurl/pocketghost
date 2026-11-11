# PocketHost (Go, Docker-free)

A Docker-free, pure-Go implementation of the [PocketHost](https://github.com/pockethost/pockethost)
multi-tenant PocketBase hosting platform, with **no Docker, no containerd, no
Node.js and no CGO**.

* The **control plane is the original PocketHost mothership application**, with
  its JavaScript hooks embedded verbatim and executed by the pure-Go
  [PocketBase SDK](https://github.com/pocketbase/pocketbase) (goja JSVM). All 32
  routes, realtime, collection rules, billing and the admin UI work unchanged.
* The **data plane** is rewritten in Go: every tenant PocketBase instance runs
  as an **isolated native OS process** instead of a container — its own working
  directory, its own loopback port, its own PID and lifecycle, its own log
  files and OS-level resource limits.

```
                    ┌──────────────────────────────────────────────┐
 browser ──TLS──▶   │ pocketghost firewall  (edge, 80/443)         │
                    │  CORS → rate limit → host routing            │
                    └───────────────┬──────────────────────────────┘
                                    │ loopback / private network
                    ┌───────────────▼──────────────────────────────┐
                    │ pocketghost daemon  (DAEMON_PORT)            │
                    │  ┌───────────────┐  ┌─────────────────────┐  │
                    │  │ edge admin    │  │ reverse proxy       │  │
                    │  │ API + static  │  │ (HTTP/WS/SSE)       │  │
                    │  └───────┬───────┘  └──────────┬──────────┘  │
                    │          │                     │             │
                    │  ┌───────▼─────────────────────────▼───────┐  │
                    │  │ mothership  (original app, Go SDK)     │  │
                    │  │  pb_hooks/*.pb.js  ← embedded verbatim  │  │
                    │  │  SQLite (pure Go)  ← PocketBase's own  │  │
                    │  └────────────────────┬───────────────────┘  │
                    └───────────────────────┼──────────────────────┘
                                            │ spawn / signals
                      ┌─────────────┬────────┴────────┬─────────────┐
                      ▼             ▼                 ▼             ▼
                  pocketbase    pocketbase        pocketbase    pocketbase
                  :30001        :30002            :30003        :3000N
                  (tenant A)    (tenant B)        (tenant C)      (...)
```

## Quick start

```bash
make build                 # static, CGO-free binary in bin/pocketghost
make test                  # unit + integration tests
make smoke                 # boot the daemon and probe /_api/daemon/health

# local development (http://pockethost.lvh.me:3000, *.lvh.me resolves to 127.0.0.1)
NODE_ENV=development make run
```

Point tenants at real PocketBase builds:

```bash
./bin/pocketghost pocketbase download 0.22.20   # caches the release
./bin/pocketghost instance list
./bin/pocketghost instance start <instance-id>
```

## CLI

| Command | Purpose |
| --- | --- |
| `serve` | Control plane: REST API, tenant proxy, scheduler |
| `firewall` | Public edge: TLS, CORS, rate limiting, host routing |
| `instance list\|start\|stop\|restart\|logs` | Native process lifecycle |
| `backup create\|list\|restore` | Per-instance snapshots |
| `status` | Daemon, registry and process overview |
| `migrate --from <data.db>` | Import a legacy PocketBase mothership DB |
| `pocketbase list\|download` | Manage the binary cache |
| `cron backup\|reconcile` | Run maintenance jobs manually |
| `ftp serve`, `sftp serve` | File services (jailed per user) |

## Repository layout

```
cmd/pocketghost          CLI entrypoint (cobra)
internal/mothership      the ORIGINAL control plane, embedded + run by the
                          pure-Go PocketBase SDK (goja JSVM, no Node.js)
internal/mothership/assets
                          pb_hooks/, pb_migrations/, pb_admin_ext/ — verbatim
internal/config          environment/settings (1:1 with the legacy constants.ts)
internal/phome           .env + data directory resolution
internal/logging         structured logger + per-instance NDJSON logs & tailing
internal/store           typed edge view over the PocketBase collections
internal/procs           native process supervisor, port allocator, restart policy
internal/pbbin           PocketBase release cache/download/verify/semver
internal/instance        instance lifecycle, hibernation, status sync
internal/proxy           dynamic host reverse proxy (HTTP/WebSocket/SSE)
internal/ratelimit       hourly + concurrent limits, PocketHost headers
internal/metrics         edge traffic counters
internal/api             edge admin API + embedded dashboard
internal/backup          zip snapshots, retention, restore
internal/scheduler       cron jobs (backups, reconcile, retention)
internal/tlscert         self-signed dev certs + ACME (autocert)
internal/vfs             path-jailed virtual filesystem
internal/ftp             FTP/FTPS server
internal/sftp            SFTP server (SSH subsystem, protocol v3)
internal/app             dependency wiring + daemon/edge handlers
internal/dashboard       embedded Svelte build
```

## Documentation

* [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how the Docker-free data plane works
* [docs/MIGRATION.md](docs/MIGRATION.md) — module-by-module port map and parity matrix
* [docs/DEPLOY.md](docs/DEPLOY.md) — fly.io / systemd deployment, tuning for 512 MB

## Compatibility

The dashboard and the public REST surface are kept compatible. The control
plane is the *original* mothership application, so `/api/instance`,
`/api/versions`, `/api/collections/users/...`, `/api/realtime` and every other
route answer exactly as they always did. The frontend source stays unmodified in
the upstream monorepo; embed its build output with:

```bash
./scripts/build-dashboard.sh /path/to/pockethost-monorepo   # build-time only
make build
```

See the parity matrix in [docs/MIGRATION.md](docs/MIGRATION.md) for the precise
status of each module.

## License

MIT — same as upstream PocketHost.
