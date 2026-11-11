# Deployment guide (512 MB fly.io target)

The control plane is a single statically linked binary. It needs:

* a writable data directory (`PH_HOME`, `DATA_ROOT`),
* an unprivileged OS user,
* ports 80/443 for the edge and `DAEMON_PORT` for the daemon,
* outbound HTTPS for PocketBase release downloads and ACME.

## 1. Build

```bash
make release            # bin/pocketghost-linux-amd64, bin/pocketghost-linux-arm64
make static-check       # asserts "statically linked"
make docker-free        # asserts no container SDK/CLI/socket usage
```

## 2. fly.io

`fly.toml` (512 MB / shared CPU):

```toml
app = "pockethost-edge"
primary_region = "sfo"

[build]
  image = "scratch"      # the binary is static; no base image needed

[env]
  NODE_ENV = "production"
  APEX_DOMAIN = "example.com"
  HTTP_PROTOCOL = "https:"
  DAEMON_PORT = "3000"
  PH_TLS_ACME = "true"
  PH_TLS_EMAIL = "ops@example.com"
  PH_HOME = "/data/pockethost"
  DATA_ROOT = "/data/pockethost/data"
  PH_INSTANCE_PORT_MIN = "30000"
  PH_INSTANCE_PORT_MAX = "30100"

[[mounts]]
  source = "pockethost_data"
  destination = "/data"

[http_service]
  internal_port = 3000
  force_https = true
  auto_stop_machines = false
  min_machines_running = 1

[[vm]]
  cpu_kind = "shared"
  cpus = 1
  memory = "512mb"
```

Notes for 512 MB:

* Keep `PH_INSTANCE_MEMORY_MB` at **96–128**. PocketBase idles around 25–40 MB;
  the limit maps to `GOMEMLIMIT`, which lets the Go GC stay inside it.
* Keep `DAEMON_PB_IDLE_TTL` at 5000 ms or higher so instances hibernate between
  requests; this is what keeps a 512 MB box from swapping.
* Cap the port pool (`PH_INSTANCE_PORT_MAX - PH_INSTANCE_PORT_MIN`) at the number
  of instances that can be resident at once, e.g. 100.
* Set `PH_MAX_CONCURRENT_DOCKER_LAUNCHES` (name kept for compatibility) to 2–3
  to avoid a thundering herd of spawns after a deploy.
* The SQLite metadata DB and per-instance `pb_data` live on the volume; enable
  fly snapshots or run `pocketghost backup create` from cron.

Because the binary is static, the container image can literally be:

```dockerfile
FROM scratch
COPY pocketghost-linux-amd64 /pocketghost
ENTRYPOINT ["/pocketghost", "serve"]
```

(No Docker is used **by** PocketHost at runtime; this only describes how your
hosting provider packages the binary.)

## 3. systemd (bare metal / VM)

```ini
# /etc/systemd/system/pocketghost-daemon.service
[Unit]
Description=PocketHost daemon
After=network-online.target

[Service]
Type=simple
User=pockethost
Group=pockethost
EnvironmentFile=/etc/pockethost/env
ExecStart=/usr/local/bin/pocketghost serve
Restart=always
RestartSec=2
LimitNOFILE=65536
# Kill the whole cgroup so tenant processes never survive a restart.
KillMode=control-group

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/pocketghost-edge.service
[Unit]
Description=PocketHost edge
After=network-online.target pocketghost-daemon.service

[Service]
Type=simple
User=pockethost
Group=pockethost
EnvironmentFile=/etc/pockethost/env
ExecStart=/usr/local/bin/pocketghost firewall --http :80 --https :443
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`/etc/pockethost/env` is a copy of `.env-template` with production values. The
file services are separate units:

```bash
pocketghost ftp serve     # port 21 (FTPS), PASV 10000-20000
pocketghost sftp serve    # port 2222
```

## 4. Reverse proxy / DNS

* Wildcard DNS: `*.example.com` → the edge. Instances resolve as
  `<subdomain>.<APEX_DOMAIN>`.
* Custom domains: set `instances.cname`; the edge matches the `Host` header
  before falling back to the subdomain mapping.
* If a CDN sits in front, make sure it forwards `CF-Connecting-IP` /
  `X-Forwarded-For` — the rate limiter prefers them in that order.

## 5. Operating

```bash
pocketghost status                       # processes, pids, ports, cached versions
pocketghost instance logs <id> -f        # live tenant log
pocketghost backup create <id>           # on-demand snapshot
pocketghost backup restore <id> <zip>    # rollback
pocketghost cron reconcile               # stop processes for powered-off instances
```

Health endpoints:

* `GET /_api/daemon/health` → `{"status":"ok"}` (503 while warming up)
* `GET /api/firewall/health` → same, on the edge

## 6. Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `no free port in range` | pool exhausted | raise `PH_INSTANCE_PORT_MAX` or shorten `DAEMON_PB_IDLE_TTL` |
| `PocketBase binary (...) not found` | cache empty | `pocketghost pocketbase download <version>` |
| Instance stuck `starting` | binary crash-looping | `pocketghost instance logs <id>`; check `processes.last_error` |
| 402 on first request | plan slot is zero | set `users.subscription_quantity > 0` |
| 503 on first request | instance not verified/powered | check `users.verified`, `instances.power` |
| ACME failures | port 80 blocked | open 80 for the HTTP-01 challenge or use a static cert |
