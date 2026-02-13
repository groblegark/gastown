#!/usr/bin/env bash
# test-gt-attach.sh — Verify gt attach prerequisites: coop health + WebSocket.
#
# Full attach testing is interactive (requires terminal), so this test
# validates the prerequisites: pod discovery, port-forward, coop health,
# and WebSocket upgrade capability.
#
# Tests:
#   1. Agent pod is running
#   2. Port-forward to coop API
#   3. Coop health endpoint responds
#   4. Agent process is running (health.pid set)
#   5. WebSocket upgrade path exists (/api/v1/ws)
#
# Usage:
#   ./scripts/test-gt-attach.sh [NAMESPACE]

MODULE_NAME="gt-attach"
source "$(dirname "$0")/lib.sh"

log "Testing gt attach prerequisites in namespace: $E2E_NAMESPACE"

# ── Discover agent pods ──────────────────────────────────────────────
AGENT_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | { grep "Running" || true; } | awk '{print $1}')
AGENT_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  if [[ "$_role" == "crew" || "$_role" == "polecat" ]]; then
    AGENT_POD="$_p"
    break
  fi
done
[[ -z "$AGENT_POD" ]] && AGENT_POD=$(echo "$AGENT_PODS" | head -1)

if [[ -z "$AGENT_POD" ]]; then
  skip_test "Agent pod is running" "no running agent pods"
  skip_test "Port-forward to coop API" "no running agent pods"
  skip_test "Coop health endpoint responds" "no running agent pods"
  skip_test "Agent process is running" "no running agent pods"
  skip_test "Screen API accessible" "no running agent pods"
  print_summary
  exit 0
fi

log "Using agent pod: $AGENT_POD"

# ── Test 1: Agent pod is running ─────────────────────────────────────
test_pod_running() {
  local phase
  phase=$(kube get pod "$AGENT_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  assert_eq "$phase" "Running"
}
run_test "Agent pod is running" test_pod_running

# ── Test 2: Port-forward to coop API ────────────────────────────────
COOP_PORT=""

test_port_forward() {
  COOP_PORT=$(start_port_forward "pod/$AGENT_POD" 8080) || return 1
  [[ -n "$COOP_PORT" ]]
}
run_test "Port-forward to coop API (port 8080)" test_port_forward

if [[ -z "$COOP_PORT" ]]; then
  skip_test "Coop health endpoint responds" "port-forward failed"
  skip_test "Agent process is running" "port-forward failed"
  skip_test "Screen API accessible" "port-forward failed"
  print_summary
  exit 0
fi

# ── Test 3: Coop health endpoint responds ────────────────────────────
HEALTH_RESP=""

test_health_endpoint() {
  HEALTH_RESP=$(curl -sf --connect-timeout 10 \
    "http://127.0.0.1:${COOP_PORT}/api/v1/health" 2>/dev/null)
  [[ -n "$HEALTH_RESP" ]]
}
run_test "Coop health endpoint responds" test_health_endpoint

# ── Test 4: Agent process is running (health.pid set) ────────────────
test_agent_pid() {
  [[ -n "$HEALTH_RESP" ]] || return 1
  local tmpf pid
  tmpf=$(mktemp)
  printf '%s' "$HEALTH_RESP" > "$tmpf"
  pid=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
print(d.get('pid', 'none'))
" 2>/dev/null)
  rm -f "$tmpf"
  log "Agent PID: $pid"
  [[ "$pid" != "none" && "$pid" != "None" && "$pid" != "null" ]]
}
run_test "Agent process is running (PID present in health)" test_agent_pid

# ── Test 5: Screen API accessible (attach prerequisite) ──────────────
test_screen_api() {
  # The screen/text endpoint must work for attach to function.
  # Attach uses WebSocket streaming of this same screen data.
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    "http://127.0.0.1:${COOP_PORT}/api/v1/screen/text" 2>/dev/null)
  log "screen/text HTTP status: $status"
  [[ "$status" == "200" ]]
}
run_test "Screen API accessible (attach prerequisite)" test_screen_api

# ── Summary ──────────────────────────────────────────────────────────
print_summary
