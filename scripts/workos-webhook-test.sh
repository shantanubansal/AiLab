#!/usr/bin/env bash
# Drives the /v1/webhooks/workos endpoint with real signed payloads so
# we exercise the verifier path end-to-end.
#
# Usage:
#   WORKOS_WEBHOOK_SECRET=secret ./scripts/workos-webhook-test.sh
#
# Runs three scenarios:
#   1. organization.created → tenants row inserted
#   2. organization.updated → name updated
#   3. organization.deleted → tenants row gone
# Plus a negative case: tampered signature → 401, no DB change.
#
# Assumes:
#   * api is on http://localhost:8080 with WORKOS_WEBHOOK_SECRET matching
#   * docker exec ailab-postgres psql works (the local-dev compose)

set -euo pipefail

SECRET="${WORKOS_WEBHOOK_SECRET:?WORKOS_WEBHOOK_SECRET env required}"
API="${AILAB_API:-http://localhost:8080}"
ORG_ID="org_$(date +%s)_$(openssl rand -hex 4)"
TEST_NAME="Acme-${ORG_ID}"

post_signed() {
  local body="$1"
  local ts="$2"
  local sig
  sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}')
  curl -sS -o /dev/stderr -w "HTTP %{http_code}\n" \
    -X POST "$API/v1/webhooks/workos" \
    -H "WorkOS-Signature: t=${ts},v1=${sig}" \
    -H "Content-Type: application/json" \
    --data "$body"
}

count_rows() {
  # tenants.id is a deterministic UUID derived from the WorkOS org id;
  # look up by slug (which the handler sets to the org id when no slug
  # is sent in the payload) for portability across test scenarios.
  docker exec ailab-postgres psql -U ailab -d ailab -tA -c "SELECT COUNT(*) FROM tenants WHERE slug = '$ORG_ID'"
}

fetch_name() {
  docker exec ailab-postgres psql -U ailab -d ailab -tA -c "SELECT name FROM tenants WHERE slug = '$ORG_ID'"
}

echo "== 1) organization.created =="
body=$(printf '{"event":"organization.created","data":{"id":"%s","name":"%s"}}' "$ORG_ID" "${TEST_NAME}-A")
post_signed "$body" "$(date +%s)"
[[ "$(count_rows)" == "1" ]] || { echo "FAIL: tenant not inserted"; exit 1; }
[[ "$(fetch_name)" == "${TEST_NAME}-A" ]] || { echo "FAIL: name mismatch (got $(fetch_name))"; exit 1; }
echo "ok"

echo "== 2) organization.updated =="
body=$(printf '{"event":"organization.updated","data":{"id":"%s","name":"%s"}}' "$ORG_ID" "${TEST_NAME}-B")
post_signed "$body" "$(date +%s)"
[[ "$(fetch_name)" == "${TEST_NAME}-B" ]] || { echo "FAIL: name not updated (got $(fetch_name))"; exit 1; }
echo "ok"

echo "== 3) bad signature =="
ts=$(date +%s)
body=$(printf '{"event":"organization.updated","data":{"id":"%s","name":"Tampered"}}' "$ORG_ID")
code=$(curl -sS -o /dev/null -w "%{http_code}" -X POST "$API/v1/webhooks/workos" \
  -H "WorkOS-Signature: t=${ts},v1=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" \
  -H "Content-Type: application/json" --data "$body")
[[ "$code" == "401" ]] || { echo "FAIL: expected 401, got $code"; exit 1; }
[[ "$(fetch_name)" == "${TEST_NAME}-B" ]] || { echo "FAIL: tampered request mutated the row"; exit 1; }
echo "ok (rejected with $code)"

echo "== 4) organization.deleted =="
body=$(printf '{"event":"organization.deleted","data":{"id":"%s"}}' "$ORG_ID")
post_signed "$body" "$(date +%s)"
[[ "$(count_rows)" == "0" ]] || { echo "FAIL: tenant still present"; exit 1; }
echo "ok"

echo
echo "PASS — all four scenarios verified"
