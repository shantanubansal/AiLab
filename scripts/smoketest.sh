#!/usr/bin/env bash
# Spine smoke test. Assumes:
#   - docker-compose deps up (`make dev-up`) and migrated (`make migrate`)
#   - kind cluster up (`make dev-cluster`) and CRDs applied (`make crds-apply`)
#   - `make run-api` and `make run-controller` in two other terminals
#
# Drives the full path: create agent → trigger run → CR appears in tenant
# namespace → Job runs → status flips to succeeded in Postgres.

set -euo pipefail

TENANT=00000000-0000-0000-0000-000000000001
TOKEN="dev:${TENANT}:smoke-user"
API=http://localhost:8080
SUFFIX=$(date +%s)

curl -fsS "$API/healthz" >/dev/null
echo "api: healthy"

agent=$(curl -fsS -X POST "$API/v1/agents" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"manifest\":{\"schemaVersion\":\"v1\",\"name\":\"smoke-${SUFFIX}\",\"mode\":\"job\",\"runtime\":\"container\",\"image\":\"hello-world\"}}")
agent_id=$(printf '%s' "$agent" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
echo "agent: $agent_id"

run=$(curl -fsS -X POST "$API/v1/agents/$agent_id/runs" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"inputs":{"hello":"world"}}')
run_id=$(printf '%s' "$run" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
echo "run:   $run_id (pending)"

echo -n "waiting for AgentRun CR..."
until kubectl get agentrun -A 2>/dev/null | grep -q "$run_id"; do sleep 1; printf '.'; done
echo " ok"

echo -n "waiting for terminal phase..."
until kubectl get agentrun -A 2>/dev/null | grep "$run_id" | grep -Eq "Succeeded|Failed"; do sleep 1; printf '.'; done
echo " ok"

final=$(curl -fsS "$API/v1/runs/$run_id" -H "Authorization: Bearer $TOKEN")
status=$(printf '%s' "$final" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
echo "final status (via api/postgres): $status"

if [[ "$status" != "succeeded" ]]; then
  echo "FAIL: expected succeeded, got $status"
  exit 1
fi
echo "PASS"
