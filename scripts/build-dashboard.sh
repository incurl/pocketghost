#!/usr/bin/env bash
# Build-time helper: compile the upstream Svelte dashboard and copy the static
# output into internal/dashboard/dist so `make build` embeds it.
#
# This runs on a developer machine or in CI *only*. The shipped binary never
# needs Node.js, pnpm or TypeScript at runtime.
#
# Usage:
#   ./scripts/build-dashboard.sh /path/to/pockethost-monorepo
#
# The argument must point at a checkout of github.com/pockethost/pockethost that
# contains packages/dashboard and packages/server (the dashboard imports shared
# types from @pockethost/server/common).

set -euo pipefail

UPSTREAM="${1:-${POCKETHOST_UPSTREAM:-}}"
if [[ -z "${UPSTREAM}" ]]; then
  echo "usage: $0 /path/to/pockethost-monorepo" >&2
  exit 1
fi
if [[ ! -d "${UPSTREAM}/packages/dashboard" ]]; then
  echo "error: ${UPSTREAM}/packages/dashboard not found" >&2
  exit 1
fi
command -v pnpm >/dev/null 2>&1 || { echo "error: pnpm is required to build the dashboard" >&2; exit 1; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${REPO_ROOT}/internal/dashboard/dist"

echo "==> building dashboard in ${UPSTREAM}"
(
  cd "${UPSTREAM}"
  pnpm install --frozen-lockfile
  pnpm --filter @pockethost/dashboard build
)

echo "==> copying static output to ${DEST}"
rm -rf "${DEST}"
mkdir -p "${DEST}"
cp -R "${UPSTREAM}/packages/dashboard/build/." "${DEST}/"

# Keep the SPA fallback the SvelteKit adapter-static config expects.
if [[ ! -f "${DEST}/404.html" ]]; then
  cp "${DEST}/index.html" "${DEST}/404.html"
fi

echo "==> done. run: make build"
