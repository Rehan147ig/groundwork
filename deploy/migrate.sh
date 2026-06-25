#!/usr/bin/env bash
# Apply Groundwork's Postgres migrations (003..013) to your Supabase database.
#
# Runs the official migrate/migrate v4 image, which honors the `-- no-transaction`
# directive in 013_extend_audit_log_indexes_concurrently (CREATE INDEX CONCURRENTLY
# cannot run inside a transaction).
#
# REQUIREMENTS:
#   * Docker available locally.
#   * DATABASE_URL pointing at the Supabase SESSION pooler (port 5432) or the
#     DIRECT connection, with ?sslmode=require. Do NOT use the transaction pooler
#     (6543): CREATE INDEX CONCURRENTLY needs a real session.
#
# USAGE:
#   export DATABASE_URL='postgresql://postgres.<ref>:<pw>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require'
#   bash deploy/migrate.sh           # apply all up migrations
#   bash deploy/migrate.sh down 1    # roll back one (optional)
set -euo pipefail

: "${DATABASE_URL:?set DATABASE_URL to your Supabase session-pooler/direct URI with sslmode=require}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATIONS_DIR="${REPO_ROOT}/migrations"

if [[ ! -d "${MIGRATIONS_DIR}" ]]; then
  echo "migrations dir not found at ${MIGRATIONS_DIR}" >&2
  exit 1
fi

CMD="${1:-up}"
ARG="${2:-}"

echo "Applying migrations from ${MIGRATIONS_DIR} ..."
docker run --rm \
  -v "${MIGRATIONS_DIR}:/migrations:ro" \
  migrate/migrate:v4.17.1 \
  -path=/migrations \
  -database "${DATABASE_URL}" \
  "${CMD}" ${ARG}

echo "Done. Tables: audit_log, audit_log_decisions, principal_aliases, msgraph_* and demo corpus."
