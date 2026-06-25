#!/bin/sh
# All-in-one entrypoint for free single-service hosting.
# Idempotent — safe to run on every boot (including free-tier cold starts):
#   1. apply the app DB migrations to Supabase
#   2. apply OpenFGA's migrations, then start OpenFGA on localhost
#   3. start the query-runtime on the host-assigned $PORT
set -eu

: "${DATABASE_URL:?set DATABASE_URL to your Supabase SESSION-pooler URI (port 5432, sslmode=require)}"

# OpenFGA shares the same Supabase database unless you override it.
OPENFGA_DATASTORE_URI="${OPENFGA_DATASTORE_URI:-$DATABASE_URL}"
export OPENFGA_DATASTORE_ENGINE=postgres OPENFGA_LOG_FORMAT=json OPENFGA_DATASTORE_URI

echo "[entrypoint] applying app migrations…"
migrate -path /migrations -database "$DATABASE_URL" up || {
  echo "[entrypoint] app migrate failed — DATABASE_URL must be the SESSION pooler (:5432) or direct connection, NOT the transaction pooler (:6543)"
  exit 1
}

echo "[entrypoint] applying openfga migrations…"
openfga migrate

echo "[entrypoint] starting openfga on 127.0.0.1:8081…"
OPENFGA_HTTP_ADDR=127.0.0.1:8081 \
OPENFGA_GRPC_ADDR=127.0.0.1:8082 \
OPENFGA_PLAYGROUND_ENABLED=false \
  openfga run &
fga_pid=$!

echo "[entrypoint] waiting for openfga health…"
i=0
while ! wget -q -O - http://127.0.0.1:8081/healthz >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -gt 60 ] && { echo "[entrypoint] openfga did not become healthy in 60s"; exit 1; }
  kill -0 "$fga_pid" 2>/dev/null || { echo "[entrypoint] openfga exited early"; exit 1; }
  sleep 1
done
echo "[entrypoint] openfga healthy."

# query-runtime talks to OpenFGA over localhost; listens on the public $PORT.
export OPENFGA_URL="http://127.0.0.1:8081"
export QUERY_RUNTIME_ADDR=":${PORT:-8080}"
echo "[entrypoint] starting query-runtime on ${QUERY_RUNTIME_ADDR} (OPENFGA_URL=${OPENFGA_URL})…"
exec query-runtime
