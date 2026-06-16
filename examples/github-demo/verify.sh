#!/bin/bash
set -euo pipefail

RT="http://localhost:8080"
KEY="gw_local_demo_bank_key"
pass=0
fail=0

check_persona() {
  local persona=$1
  local q=$2
  local expectDoc=$3
  local expect=$4

  local req
  req=$(jq -n --arg q "$q" --arg u "$persona" '{question: $q, user_id: $u}')

  local res
  res=$(curl -s -X POST "$RT/v1/query" \
    -H "X-Groundwork-API-Key: $KEY" \
    -H "Content-Type: application/json" \
    -d "$req")

  local n
  n=$(echo "$res" | jq '.citations | length')
  
  local foundDoc="false"
  if echo "$res" | jq -e ".citations[] | select(.document_id == \"$expectDoc\")" >/dev/null; then
    foundDoc="true"
  fi

  local got="deny"
  if [ "$foundDoc" = "true" ]; then got="allow"; fi

  if [ "$got" = "$expect" ]; then
    echo -e "\033[32mPASS   $persona | \"$q\" -> $got ($n docs, found $expectDoc=$foundDoc)\033[0m"
    pass=$((pass+1))
  else
    echo -e "\033[31mFAIL   $persona | \"$q\" -> $got ($n docs, found $expectDoc=$foundDoc), expected $expect\033[0m"
    fail=$((fail+1))
  fi
}

echo "=== Groundwork github-demo acceptance ($RT) ==="
check_persona "alice" "Q4 budget projections" "gh:finance-budget" "allow"
check_persona "bob" "executive strategy documents" "gh:executive-strategy" "deny"
check_persona "dave" "security audit findings" "gh:security-audit" "allow"
check_persona "carol" "payroll system architecture" "gh:payroll-system" "deny"
check_persona "eve" "board strategy deck" "gh:executive-strategy" "allow"
check_persona "alice" "engineering platform docs" "gh:engineering-platform" "deny"
check_persona "bob" "payroll system code" "gh:payroll-system" "allow"
check_persona "carol" "Q4 budget details" "gh:finance-budget" "deny"
check_persona "dave" "executive compensation" "gh:executive-strategy" "deny"
check_persona "eve" "security posture review" "gh:security-audit" "deny"

echo "---"
echo "PASS=$pass  FAIL=$fail"
if [ "$fail" -eq 0 ]; then
  echo -e "\033[32mALL GREEN\033[0m"
else
  echo -e "\033[31mSOME FAILED\033[0m"
fi
