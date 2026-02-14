#!/usr/bin/env bash
# test-coop-broker-health.sh — Verify coop broker + mux health.
#
# Tests:
#   1. Coop broker pod all containers ready
#   2. Broker has coop/mux container
#   3. Health endpoint returns 200
#   4. Mux page responds with auth
#   5. Health API reports session count
#   6. Protected endpoints require auth (401/403)
#   7. At least 1 session registered (or skip)
#   8. Credential volume mounted
#
# Usage:
#   ./scripts/test-coop-broker-health.sh [NAMESPACE]

MODULE_NAME="coop-broker-health"
source "$(dirname "$0")/lib.sh"

log "Testing coop broker health in namespace: $E2E_NAMESPACE"

# ── Discover coop broker ─────────────────────────────────────────────
BROKER_POD=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')

# Discover container name (merged deployment uses "coopmux", split uses "coop-broker")
BROKER_CONTAINER=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)
log "Broker container: ${BROKER_CONTAINER:-unknown}"

# Get broker auth token: try COOP_MUX_AUTH_TOKEN first (merged), then BROKER_TOKEN (split)
BROKER_TOKEN=""
if [[ -n "$BROKER_POD" && -n "$BROKER_CONTAINER" ]]; then
  BROKER_TOKEN=$(kube exec "$BROKER_POD" -c "$BROKER_CONTAINER" -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null || echo "")
  if [[ -z "$BROKER_TOKEN" ]]; then
    BROKER_TOKEN=$(kube exec "$BROKER_POD" -c "$BROKER_CONTAINER" -- printenv BROKER_TOKEN 2>/dev/null || echo "")
  fi
fi
if [[ -z "$BROKER_TOKEN" ]]; then
  _broker_cm=$(kube get configmap --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  if [[ -n "$_broker_cm" ]]; then
    BROKER_TOKEN=$(kube get configmap "$_broker_cm" -o jsonpath='{.data.BROKER_TOKEN}' 2>/dev/null || echo "")
  fi
fi
BROKER_TOKEN="${BROKER_TOKEN:-${BROKER_TOKEN_ENV:-}}"
if [[ -n "$BROKER_TOKEN" ]]; then
  log "Broker token: set (${#BROKER_TOKEN} chars)"
else
  log "Broker token: NOT SET"
fi

log "Broker pod: ${BROKER_POD:-none}"
log "Broker svc: ${BROKER_SVC:-none}"

# ── Test 1: Pod is ready (all containers) ──────────────────────────────
test_pod_ready() {
  [[ -n "$BROKER_POD" ]] || return 1
  local ready total
  ready=$(kube get pod "$BROKER_POD" --no-headers 2>/dev/null | awk '{print $2}' | cut -d/ -f1)
  total=$(kube get pod "$BROKER_POD" --no-headers 2>/dev/null | awk '{print $2}' | cut -d/ -f2)
  log "Broker pod readiness: ${ready}/${total}"
  [[ "$ready" == "$total" && "${ready:-0}" -gt 0 ]]
}
run_test "Coop broker pod all containers ready" test_pod_ready

# ── Test 2: Broker has coop/mux container ─────────────────────────────
test_container_names() {
  [[ -n "$BROKER_POD" ]] || return 1
  local containers
  containers=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
  log "Broker containers: $containers"
  # Accept merged "coopmux" or split "coop-broker" + "coop-mux"
  assert_contains "$containers" "coop" || assert_contains "$containers" "mux"
}
run_test "Broker has coop/mux container" test_container_names

# ── Port-forward to broker service ───────────────────────────────────
BROKER_PORT=""

setup_port_forward() {
  # Detect port: merged coopmux uses 9800, split deployment uses 8080
  local target_port=9800
  local svc_port
  if [[ -n "$BROKER_SVC" ]]; then
    svc_port=$(kube get svc "$BROKER_SVC" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
    target_port="${svc_port:-9800}"
    BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" "$target_port") || return 1
  else
    # Try 9800 first (merged), fall back to 8080 (split)
    BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 9800) || \
      BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 8080) || return 1
  fi
  log "Broker port-forward: localhost:$BROKER_PORT -> $target_port"
}

# ── Test 3: Health endpoint returns 200 ──────────────────────────────
test_health_endpoint() {
  setup_port_forward || return 1
  local resp
  resp=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://127.0.0.1:${BROKER_PORT}/api/v1/health" 2>/dev/null)
  assert_eq "$resp" "200"
}
run_test "Health endpoint (/api/v1/health) returns 200" test_health_endpoint

# ── Test 4: Mux page served (with auth) ─────────────────────────────
test_mux_page() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local body
  body=$(curl -s --connect-timeout 5 \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    "http://127.0.0.1:${BROKER_PORT}/mux" 2>/dev/null)
  # Accept HTML content or JSON session list (mux may serve either)
  assert_contains "$body" "html" || assert_contains "$body" "sessions" || assert_contains "$body" "Mux" || assert_contains "$body" "Coop"
}
run_test "Mux page (/mux) responds with auth" test_mux_page

# ── Test 5: Health reports session count ──────────────────────────────
test_health_session_count() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local resp
  resp=$(curl -s --connect-timeout 5 \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/health" 2>/dev/null)
  log "Health response: $resp"
  # Health should report session_count
  assert_contains "$resp" "session_count"
}
run_test "Health API reports session count" test_health_session_count

# ── Test 6: Protected endpoints require auth ─────────────────────────
test_auth_required() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local status
  # Try /mux without auth — should require auth
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 \
    "http://127.0.0.1:${BROKER_PORT}/mux" 2>/dev/null)
  [[ "$status" == "401" || "$status" == "403" ]]
}
run_test "Protected endpoints require auth (401/403)" test_auth_required

# ── Test 7: At least one session registered ───────────────────────────
_SESSION_COUNT="0"
if [[ -n "$BROKER_PORT" ]]; then
  _health_resp=$(curl -s --connect-timeout 5 \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/health" 2>/dev/null)
  _SESSION_COUNT=$(echo "$_health_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_count',0))" 2>/dev/null || echo "0")
fi

if [[ "${_SESSION_COUNT:-0}" -eq 0 ]]; then
  skip_test "At least 1 session registered" "No sessions registered (fresh namespace)"
else
  test_sessions_registered() {
    assert_ge "$_SESSION_COUNT" 1
  }
  run_test "At least 1 session registered (session_count=$_SESSION_COUNT)" test_sessions_registered
fi

# ── Test 8: Credential PVC mounted ──────────────────────────────────
test_credential_pvc() {
  [[ -n "$BROKER_POD" ]] || return 1
  # Check if a credentials-related volume mount exists
  local volumes
  volumes=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.volumes[*].name}' 2>/dev/null)
  log "Broker volumes: $volumes"
  assert_contains "$volumes" "credential"
}
run_test "Credential volume mounted" test_credential_pvc

# ── Summary ──────────────────────────────────────────────────────────
print_summary
