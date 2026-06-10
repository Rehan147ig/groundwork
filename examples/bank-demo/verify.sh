#!/usr/bin/env bash
# Bank-demo acceptance check: runs the keystone allow/deny outcomes against a running
# Groundwork runtime and prints PASS/FAIL. This is the "does enforcement actually work"
# proof for the demo.
#
# Assumes the dev/codespace profile (ALLOW_DEMO_IDENTITY=true), so the persona is passed
# as the body user_id — no JWT needed. For the verified-identity path, use the Demo Console
# (which mints a signed JWT) or the canary tool instead.
#
# Usage: bash examples/bank-demo/verify.sh [RUNTIME_URL] [API_KEY]
#   defaults: http://localhost:8080  gw_local_acme_key
set -u
RT="${1:-http://localhost:8080}"
KEY="${2:-gw_local_acme_key}"
pass=0; fail=0

check() { # persona  question  expect(allow|deny)
  local persona="$1" q="$2" expect="$3" n got
  n=$(curl -s "$RT/v1/query" \
        -H "X-Groundwork-API-Key: $KEY" -H "Content-Type: application/json" \
        -d "{\"question\":\"$q\",\"user_id\":\"$persona\"}" \
      | python3 -c 'import sys,json;
try:
 print(len(json.load(sys.stdin).get("citations",[])))
except Exception:
 print("ERR")' 2>/dev/null)
  if [ "$n" = "ERR" ] || [ -z "$n" ]; then
    echo "ERROR  $persona | \"$q\" (no/invalid response from runtime)"; fail=$((fail+1)); return
  fi
  got="deny"; [ "$n" != "0" ] && got="allow"
  if [ "$got" = "$expect" ]; then
    echo "PASS   $persona | \"$q\" -> $got ($n docs)"; pass=$((pass+1))
  else
    echo "FAIL   $persona | \"$q\" -> $got ($n docs), expected $expect"; fail=$((fail+1))
  fi
}

echo "=== Groundwork bank-demo acceptance ($RT) ==="
# The keystone moments (design intent from personas.json expected_demo_outcomes):
check teller_jane      "executive compensation framework"  deny    # teller blocked from exec-only memo
check exec_starkceo    "executive compensation framework"  allow   # CEO sees it
check rm_tony          "Stark Industries credit memo"      allow   # assigned RM
check rm_natasha       "Stark Industries credit memo"      deny    # different RM/branch
check compliance_mhill "Hammer Industries adverse media"   allow   # compliance sees KYC/adverse-media
check auditor_logan    "whistleblower follow-up"           allow   # auditor has the direct grant
check teller_jane      "what is our credit policy"         allow   # all-staff policy is visible
echo "---"
echo "PASS=$pass  FAIL=$fail"
[ "$fail" -eq 0 ] && echo "ALL GREEN" || echo "SOME FAILED — if it's a relevance miss (wrong/zero docs for an 'allow'), check the embedder is up and re-seed."
[ "$fail" -eq 0 ]
