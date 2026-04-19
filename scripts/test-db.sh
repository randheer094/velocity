#!/usr/bin/env bash
# End-to-end test runner: boot the local Postgres (compose.yml), run
# `go test ./...` against it, and tear the container down on exit.
# Data under .pgdata/ persists across runs so re-boots are fast.
#
#   scripts/test-db.sh            # run all tests
#   scripts/test-db.sh ./internal/db/...   # pass-through args for go test
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
cd "$root"

cleanup() {
  docker compose down -t 30 >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo ">>> starting postgres"
docker compose up -d postgres >/dev/null

echo ">>> waiting for readiness"
for _ in $(seq 1 60); do
  if docker compose exec -T postgres pg_isready -U velocity -d velocity >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! docker compose exec -T postgres pg_isready -U velocity -d velocity >/dev/null 2>&1; then
  echo "postgres did not become ready within 60s" >&2
  exit 1
fi

echo ">>> running tests"
VELOCITY_DB_HOST=127.0.0.1 \
VELOCITY_DB_PORT=55432 \
VELOCITY_DB_USER=velocity \
VELOCITY_DB_PASSWORD=velocity \
VELOCITY_DB_NAME=velocity \
  go test "${@:-./...}"
