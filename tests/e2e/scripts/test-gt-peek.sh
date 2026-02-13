#!/usr/bin/env bash
# test-gt-peek.sh — Verify gt peek command works via coop screen API.
#
# Tests:
#   1. Agent pod is running
#   2. Port-forward to agent pod coop API
#   3. Coop screen/text endpoint returns data
#   4. Screen text is non-empty
#   5. Screen text contains recognizable terminal content
#
# Usage:
#   ./scripts/test-gt-peek.sh [NAMESPACE]

MODULE_NAME="gt-peek"
source "$(dirname "$0")/lib.sh"

log "Testing gt peek (coop screen API) in namespace: $E2E_NAMESPACE"

# ── Discover agent pods ──────────────────────────────────────────────
AGENT_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | { grep "Running" || true; } | awk '{print $1}')
AGENT_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  # Prefer mayor since it should always have screen content
  if [[ "$_role" == "mayor" ]]; then
    AGENT_POD="$_p"
    break
  fi
done
# Fall back to any running agent pod
[[ -z "$AGENT_POD" ]] && AGENT_POD=$(echo "$AGENT_PODS" | head -1)

if [[ -z "$AGENT_POD" ]]; then
  skip_test "Agent pod is running" "no running agent pods"
  skip_test "Port-forward to coop API" "no running agent pods"
  skip_test "Screen/text endpoint responds" "no running agent pods"
  skip_test "Screen text is non-empty" "no running agent pods"
  skip_test "Screen contains terminal content" "no running agent pods"
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
  skip_test "Screen/text endpoint responds" "port-forward failed"
  skip_test "Screen text is non-empty" "port-forward failed"
  skip_test "Screen contains terminal content" "port-forward failed"
  print_summary
  exit 0
fi

# ── Test 3: Screen/text endpoint responds ────────────────────────────
SCREEN_TEXT=""

test_screen_endpoint() {
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    "http://127.0.0.1:${COOP_PORT}/api/v1/screen/text" 2>/dev/null)
  log "screen/text HTTP status: $status"
  [[ "$status" == "200" ]]
}
run_test "Screen/text endpoint responds (HTTP 200)" test_screen_endpoint

# ── Test 4: Screen text is non-empty ─────────────────────────────────
test_screen_nonempty() {
  SCREEN_TEXT=$(curl -sf --connect-timeout 10 \
    "http://127.0.0.1:${COOP_PORT}/api/v1/screen/text" 2>/dev/null)
  local len=${#SCREEN_TEXT}
  log "Screen text length: $len chars"
  [[ "$len" -gt 0 ]]
}
run_test "Screen text is non-empty" test_screen_nonempty

# ── Test 5: Screen contains recognizable terminal content ────────────
test_screen_content() {
  # Agent screens typically contain a prompt marker (❯ or $), Claude output,
  # or at least some visible text. Check for any non-whitespace content.
  local stripped
  stripped=$(echo "$SCREEN_TEXT" | tr -d '[:space:]')
  local stripped_len=${#stripped}
  log "Non-whitespace content: $stripped_len chars"
  # Even idle agents show a prompt line
  [[ "$stripped_len" -gt 0 ]]
}
run_test "Screen contains terminal content" test_screen_content

# ── Summary ──────────────────────────────────────────────────────────
print_summary
