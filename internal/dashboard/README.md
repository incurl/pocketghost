# Embedded dashboard

`dist/` is compiled into the binary (`//go:embed all:dist`) and served by
`internal/api.handleDashboard` with an SPA fallback to `404.html`.

Two supported workflows:

## 1. Placeholder build (default, committed)

The repository ships `index.html` and `404.html` placeholders so `make build`
always succeeds. The REST API, tenant proxy and CLI are fully functional; only
the Svelte UI is missing.

## 2. Real dashboard build (build time only)

The Svelte dashboard is kept **unmodified** in the upstream monorepo. To embed
it:

```bash
# one-time, on a machine with Node 20+ and pnpm
git clone https://github.com/pockethost/pockethost /tmp/pockethost
./scripts/build-dashboard.sh /tmp/pockethost
make build
```

`scripts/build-dashboard.sh` runs `pnpm --filter @pockethost/dashboard build`
and copies `packages/dashboard/build/` into `dist/`.

Node.js, pnpm and TypeScript are **build-time** dependencies of the frontend
only. The resulting binary has no Node runtime, no `node_modules` and no
JavaScript engine — it serves plain static files and the Go REST API.

## Why the dashboard is not vendored here

`packages/dashboard` imports shared types from `@pockethost/server/common`
(a TypeScript package that this migration removes). Vendoring the dashboard
without that package would leave a source tree that cannot be built, so the
build-time script references the upstream checkout instead.
