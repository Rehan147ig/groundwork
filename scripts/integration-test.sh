#!/usr/bin/env bash
#
# Runs Groundwork's P0 integration tests against a LIVE stack (OpenFGA + Postgres + Qdrant).
# It brings the stack up with Docker Compose, applies migrations (in the test harness),
# runs the build-tagged integration suite, and always tears the stack down afterwards.
#
# Usage:  scripts/integration-test.sh
# Requires: Docker (with the `docker compose` v2 plugin) and Go.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/services/query-runtime/test/integration/docker-compose.yml"
PROJECT="gw-integration"

compose() { docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"; }

cleanup() {
  echo "==> Tearing down integration stack"
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> Starting integration stack (OpenFGA + Postgres + Qdrant)"
compose up -d

# The Go test harness (TestMain) waits for readiness and applies migrations 003-007, so no
# explicit sleep is needed here. These env vars tell the harness where the services live.
export GROUNDWORK_TEST_DATABASE_URL="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable"
export GROUNDWORK_TEST_OPENFGA_URL="http://localhost:8080"
export GROUNDWORK_TEST_QDRANT_URL="http://localhost:6333"
export GROUNDWORK_TEST_MIGRATIONS_DIR="$REPO_ROOT/migrations"

echo "==> Running integration tests (go test -tags integration)"
cd "$REPO_ROOT/services/query-runtime"
go test -tags integration -count=1 -v ./test/integration/...

echo "==> Integration tests passed"
