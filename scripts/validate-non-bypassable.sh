#!/usr/bin/env bash
#
# Validates the Groundwork non-bypassable deployment profile:
#   - query-runtime is reachable through the gateway
#   - /mcp is reachable (and requires an API key)
#   - Qdrant / OpenFGA / PostgreSQL / Elasticsearch are NOT reachable on host ports
#   - (optional) an authenticated query still works through /mcp
#
# Usage:
#   GW_URL=http://localhost ./scripts/validate-non-bypassable.sh
#   GW_API_KEY=gw_live_xxx GW_URL=http://localhost ./scripts/validate-non-bypassable.sh
#
# Exit code 0 = only Groundwork ingress is exposed; non-zero = a check failed.

set -u

GW_URL="${GW_URL:-http://localhost}"
API_KEY="${GW_API_KEY:-}"
fail=0

pass() { echo "PASS: $1"; }
bad()  { echo "FAIL: $1"; fail=1; }

http_code() { curl -s -o /dev/null -m 5 -w '%{http_code}' "$@" 2>/dev/null; }

echo "== Groundwork non-bypassable validation =="
echo "gateway: $GW_URL"
echo

# 1. query-runtime reachable through the gateway.
c=$(http_code "$GW_URL/healthz")
[ "$c" = "200" ] && pass "query-runtime /healthz reachable (200)" || bad "/healthz expected 200, got '$c'"

# 2. /mcp reachable AND auth-protected (401 without an API key).
c=$(http_code -X POST "$GW_URL/mcp" -H 'Content-Type: application/json' \
      --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}')
[ "$c" = "401" ] && pass "/mcp reachable and requires API key (401)" || bad "/mcp expected 401 without key, got '$c'"

# 3-6. Backend host ports MUST be closed (connection refused).
check_closed() { # <port> <name>
  if timeout 2 bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2>/dev/null; then
    bad "$2 ($1) is reachable from the host — it must be internal-only"
  else
    pass "$2 ($1) not reachable from the host"
  fi
}
check_closed 6333 "Qdrant"
check_closed 8081 "OpenFGA"
check_closed 5432 "PostgreSQL"
check_closed 9200 "Elasticsearch"

# 7. Optional: a real authenticated query still works through /mcp.
if [ -n "$API_KEY" ]; then
  body=$(curl -s -m 8 -X POST "$GW_URL/mcp" -H "X-Groundwork-API-Key: $API_KEY" \
          --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' 2>/dev/null)
  if echo "$body" | grep -q "groundwork_search"; then
    pass "authenticated /mcp tools/list works (Groundwork query path is live)"
  else
    bad "authenticated /mcp tools/list failed: $body"
  fi
else
  echo "INFO: set GW_API_KEY to also verify an authenticated query through /mcp"
fi

echo
if [ "$fail" = "0" ]; then
  echo "ALL CHECKS PASSED — only Groundwork ingress is exposed."
  exit 0
else
  echo "CHECKS FAILED — a backend is exposed or Groundwork ingress is broken."
  exit 1
fi
