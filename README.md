# PocketHost (Go, Docker-free)

A complete reimplementation of the [PocketHost](https://github.com/pockethost/pockethost)
multi-tenant PocketBase hosting platform in **pure Go**, with **no Docker, no
containerd, no Node.js and no CGO**.

Every tenant PocketBase instance now runs as an **isolated native OS process**
instead of a container: its own working directory, its own loopback port, its own
PID and lifecycle, its own log files and OS-level resource limits.

```
                    ┌──────────────────────────────────────────────┐
 browser ──TLS──▶   │ pocketghost firewall  (edge, 80/443)         │
                    │  CORS → rate limit → host routing            │
                    └───────────────┬──────────────────────────────┘
                                    │ loopback / private network
                    ┌───────────────▼──────────────────────────────┐
                    │ pocketghost daemon  (DAEMON_PORT)            │
                    │  ┌───────────────┐  ┌─────────────────────┐  │
                    │  │ control-plane │  │ reverse proxy       │  │
                    │  │ REST + static │  │ (HTTP/WS/SSE)       │  │
                    │  └───────┬───────┘  └──────────┬──────────┘  │
                    │          │                     │             │
                    │  ┌───────▼────────┐   ┌────────▼──────────┐  │
                    │  │ SQLite (pure)  │   │ process supervisor│  │
                    │  └────────────────┘   └────────┬──────────┘  │
                    └────────────────────────────────┼─────────────┘
                                                     │ spawn / signals
                       ┌─────────────┬───────────────┼──────────────┐
                       ▼             ▼               ▼              ▼
                   pocketbase    pocketbase      pocketbase     pocketbase
                   :30001        :30002          :30003         :3000N
                   (tenant A)    (tenant B)      (tenant C)     (...)
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
internal/config          environment/settings (1:1 with the legacy constants.ts)
internal/phome           .env + data directory resolution
internal/logging         structured logger + per-instance NDJSON logs & tailing
internal/store           SQLite metadata (modernc.org/sqlite, CGO-free) + legacy import
internal/procs           native process supervisor, port allocator, restart policy
internal/pbbin           PocketBase release cache/download/verify/semver
internal/instance        instance lifecycle, hibernation, status sync
internal/proxy           dynamic host reverse proxy (HTTP/WebSocket/SSE)
internal/ratelimit       hourly + concurrent limits, PocketHost headers
internal/metrics         edge traffic counters
internal/api             PocketBase-compatible REST + daemon admin API + dashboard
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

The dashboard and the public REST surface are kept compatible: the Svelte app is
served from the binary and talks to the same `/api/collections/users/...`,
`/api/instance`, `/api/versions` and `/_api/daemon/...` routes it always used.

The frontend source stays unmodified in the upstream monorepo; embed its build
output with:

```bash
./scripts/build-dashboard.sh /path/to/pockethost-monorepo   # build-time only
make build
```

See the parity matrix in [docs/MIGRATION.md](docs/MIGRATION.md) for the precise
status of each module.

## License

MIT — same as upstream PocketHost.
