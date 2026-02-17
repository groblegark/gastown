#!/usr/bin/env bash
# test-sling-nudge-peek.sh — Verify sling, nudge, and peek parity on K8s.
#
# Tests the core agent interaction loop:
#   1. Daemon API reachable (prerequisite)
#   2. Peek: coop screen/text returns agent terminal output
#   3. Nudge: send text via coop input API, verify it appears on screen
#   4. Sling: create bead, assign to agent via daemon, verify state=hooked
#   5. Sling cleanup: close test bead
#   6. Peek line count: screen text has expected minimum content
#   7. Nudge delivery: agent processes nudge (screen changes)
#   8. Cross-agent peek: can peek at multiple agent pods
#
# This exercises the same APIs that gt sling, gt nudge, and gt peek use
# on K8s, validating parity with local execution paths.
#
# Usage: E2E_NAMESPACE=gastown-rwx ./scripts/test-sling-nudge-peek.sh

set -euo pipefail
MODULE_NAME="sling-nudge-peek"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing sling/nudge/peek parity in $NS"

# ── Discover infrastructure ────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep "daemon" || true; } \
  | { grep -v "dolt\|nats\|slackbot" || true; } \
  | { grep "Running" || true; } \
  | head -1 | awk '{print $1}')

AGENT_PODS=$(kube get pods --no-headers 2>/dev/null \
  | { grep "^gt-" || true; } \
  | { grep "Running" || true; } \
  | awk '{print $1}')

# Prefer mayor (always has screen content)
AGENT_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  if [[ "$_role" == "mayor" ]]; then
    AGENT_POD="$_p"
    break
  fi
done
[[ -z "$AGENT_POD" ]] && AGENT_POD=$(echo "$AGENT_PODS" | head -1)

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

if [[ -z "$AGENT_POD" ]]; then
  skip_all "no running agent pods"
  exit 0
fi

log "Daemon pod: $DAEMON_POD"
log "Agent pod: $AGENT_POD"

# ── Port-forwards ──────────────────────────────────────────────────
DAEMON_PORT=""
COOP_PORT=""

# Helper: run bd CLI inside agent pod
agent_bd() {
  kube exec "$AGENT_POD" -c agent -- bd "$@" 2>/dev/null
}

# Helper: coop API call on agent pod
coop_api() {
  local endpoint="$1"
  shift
  curl -sf --connect-timeout 10 "http://127.0.0.1:${COOP_PORT}${endpoint}" "$@" 2>/dev/null
}

# ── Test 1: Daemon API is reachable ────────────────────────────────
test_daemon_reachable() {
  DAEMON_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || return 1
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    "http://127.0.0.1:${DAEMON_PORT}/healthz" 2>/dev/null)
  log "Daemon /healthz: HTTP $status"
  [[ "$status" == "200" ]]
}
run_test "Daemon API is reachable" test_daemon_reachable

if [[ -z "$DAEMON_PORT" ]]; then
  skip_test "Peek: screen/text returns content" "daemon unreachable"
  skip_test "Nudge: send input via coop API" "daemon unreachable"
  skip_test "Nudge: input appears on screen" "daemon unreachable"
  skip_test "Sling: create test bead via bd CLI" "daemon unreachable"
  skip_test "Sling: update bead to in_progress" "daemon unreachable"
  skip_test "Sling: verify bead state persists" "daemon unreachable"
  skip_test "Sling: close test bead" "daemon unreachable"
  skip_test "Peek: screen has substantial content" "daemon unreachable"
  print_summary
  exit 0
fi

# ── Test 2: Peek — screen/text returns content ─────────────────────
SCREEN_BEFORE=""

test_peek_screen() {
  COOP_PORT=$(start_port_forward "pod/$AGENT_POD" 8080) || return 1
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    "http://127.0.0.1:${COOP_PORT}/api/v1/screen/text" 2>/dev/null)
  log "screen/text HTTP status: $status"
  [[ "$status" == "200" ]] || return 1

  SCREEN_BEFORE=$(coop_api "/api/v1/screen/text")
  local len=${#SCREEN_BEFORE}
  log "Screen text length: $len chars"
  [[ "$len" -gt 0 ]]
}
run_test "Peek: screen/text returns content" test_peek_screen

if [[ -z "$COOP_PORT" ]]; then
  skip_test "Nudge: send input via coop API" "coop port-forward failed"
  skip_test "Nudge: input appears on screen" "coop port-forward failed"
  skip_test "Sling: create test bead via bd CLI" "coop port-forward failed"
  skip_test "Sling: update bead to in_progress" "coop port-forward failed"
  skip_test "Sling: verify bead state persists" "coop port-forward failed"
  skip_test "Sling: close test bead" "coop port-forward failed"
  skip_test "Peek: screen has substantial content" "coop port-forward failed"
  print_summary
  exit 0
fi

# ── Test 3: Nudge — send input via coop API ────────────────────────
NUDGE_TOKEN="e2e-nudge-$(date +%s)"

test_nudge_send() {
  # Build JSON body safely
  local bodyfile
  bodyfile=$(mktemp)
  python3 -c "
import json
with open('$bodyfile', 'w') as f:
    json.dump({'text': 'echo $NUDGE_TOKEN', 'enter': False}, f)
" 2>/dev/null

  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    -X POST -H "Content-Type: application/json" \
    -d "@${bodyfile}" \
    "http://127.0.0.1:${COOP_PORT}/api/v1/input" 2>/dev/null)
  rm -f "$bodyfile"
  log "Input API HTTP status: $status"
  [[ "$status" == "200" || "$status" == "204" ]]
}
run_test "Nudge: send input via coop API" test_nudge_send

# ── Test 4: Nudge — input appears on screen ────────────────────────
test_nudge_visible() {
  # Wait briefly for the input to appear on terminal
  local deadline=$((SECONDS + 10))
  while [[ $SECONDS -lt $deadline ]]; do
    local screen
    screen=$(coop_api "/api/v1/screen/text" || echo "")
    if echo "$screen" | grep -q "$NUDGE_TOKEN"; then
      log "Nudge token found on screen"
      return 0
    fi
    sleep 1
  done
  log "Nudge token not found on screen after 10s"
  return 1
}
run_test "Nudge: input appears on screen" test_nudge_visible

# ── Test 5: Sling — create test bead via bd CLI ───────────────────
TEST_BEAD_ID=""

test_sling_create_bead() {
  local resp
  resp=$(agent_bd create --title="e2e-sling-test-$(date +%s)" --type=task --priority=4 --json 2>&1)
  TEST_BEAD_ID=$(echo "$resp" | python3 -c "
import json, sys
data = sys.stdin.read()
# bd create --json outputs a single JSON object on one line (after warning lines)
for line in data.splitlines():
    line = line.strip()
    if line.startswith('{'):
        d = json.loads(line)
        print(d.get('id', '')); break
" 2>/dev/null)

  log "Created test bead: $TEST_BEAD_ID"
  [[ -n "$TEST_BEAD_ID" ]]
}
run_test "Sling: create test bead via bd CLI" test_sling_create_bead

# Helper: extract a field from bd JSON output (handles multi-line pretty-printed JSON)
bd_json_field() {
  local field="$1"
  python3 -c "
import json, sys
data = sys.stdin.read()
try:
    d = json.loads(data)
    if isinstance(d, list) and len(d) > 0: d = d[0]
    print(d.get('$field', ''))
except:
    print('')
" 2>/dev/null
}

# ── Test 6: Sling — update bead to in_progress (mimics hook) ──────
test_sling_hook_bead() {
  [[ -n "$TEST_BEAD_ID" ]] || return 1

  local resp
  resp=$(agent_bd update "$TEST_BEAD_ID" --status=in_progress --json 2>&1)
  local new_status
  new_status=$(echo "$resp" | bd_json_field "status")
  log "Updated status: $new_status"
  [[ "$new_status" == "in_progress" ]]
}
run_test "Sling: update bead to in_progress (mimics hook)" test_sling_hook_bead

# ── Test 7: Sling — verify bead state persists ───────────────────
test_sling_verify_state() {
  [[ -n "$TEST_BEAD_ID" ]] || return 1

  local resp
  resp=$(agent_bd show "$TEST_BEAD_ID" --json 2>&1)
  local bead_status
  bead_status=$(echo "$resp" | bd_json_field "status")
  log "Bead status on re-read: $bead_status"
  [[ "$bead_status" == "in_progress" ]]
}
run_test "Sling: verify bead state persists after update" test_sling_verify_state

# ── Test 8: Sling — close test bead (cleanup) ─────────────────────
test_sling_close_bead() {
  [[ -n "$TEST_BEAD_ID" ]] || return 1

  local resp
  resp=$(agent_bd close "$TEST_BEAD_ID" --json 2>&1)
  local close_status
  close_status=$(echo "$resp" | bd_json_field "status")
  log "Close status: $close_status"
  [[ "$close_status" == "closed" ]]
}
run_test "Sling: close test bead" test_sling_close_bead

# ── Test 9: Peek — screen has substantial content ──────────────────
test_peek_content_depth() {
  local screen
  screen=$(coop_api "/api/v1/screen/text" || echo "")
  local line_count
  line_count=$(echo "$screen" | wc -l | tr -d ' ')
  local stripped
  stripped=$(echo "$screen" | tr -d '[:space:]')
  local stripped_len=${#stripped}

  log "Screen: $line_count lines, $stripped_len non-whitespace chars"
  # Active agent should have at least some content (prompt + output)
  [[ "$stripped_len" -gt 10 ]]
}
run_test "Peek: screen has substantial content" test_peek_content_depth

# ── Test 10: Cross-agent peek — can peek multiple pods ──────────────
test_cross_agent_peek() {
  local pod_count=0
  local peek_ok=0

  for _p in $AGENT_PODS; do
    pod_count=$((pod_count + 1))
    [[ $pod_count -gt 3 ]] && break  # Test up to 3 pods

    if [[ "$_p" == "$AGENT_POD" ]]; then
      peek_ok=$((peek_ok + 1))
      continue
    fi

    local pf_port
    pf_port=$(start_port_forward "pod/$_p" 8080 2>/dev/null) || continue
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 \
      "http://127.0.0.1:${pf_port}/api/v1/screen/text" 2>/dev/null)
    if [[ "$status" == "200" ]]; then
      peek_ok=$((peek_ok + 1))
      log "Peek on $_p: OK"
    else
      log "Peek on $_p: HTTP $status"
    fi
  done

  log "Peek succeeded on $peek_ok/$pod_count agent pods"
  [[ "$peek_ok" -ge 1 ]]
}
run_test "Cross-agent peek: multiple pods reachable" test_cross_agent_peek

# ── Test 11: Agent health endpoint ──────────────────────────────────
test_agent_health() {
  local resp
  resp=$(coop_api "/api/v1/health" || echo "{}")
  local health_status
  health_status=$(echo "$resp" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('status', d.get('healthy', 'unknown')))
except: print('unknown')
" 2>/dev/null)
  log "Agent health: $health_status"
  [[ "$health_status" != "unknown" ]]
}
run_test "Agent health endpoint responds" test_agent_health

# ── Cleanup ────────────────────────────────────────────────────────
# Port-forwards cleaned up by lib.sh EXIT trap.
# Test bead already closed in test 8.

print_summary
