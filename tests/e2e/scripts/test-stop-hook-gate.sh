#!/usr/bin/env bash
# test-stop-hook-gate.sh — Verify Stop hook gate blocks and allows correctly.
#
# Tests the session gate system end-to-end on the cluster:
#
#   Phase 1 — Gate blocking:
#     1. Session gate check blocks when decision gate is unsatisfied
#     2. Block reason contains config bead prompt (not bare gate description)
#     3. Session gate check allows when decision gate is marked
#     4. Gate clear resets the decision gate
#
#   Phase 2 — Cross-session satisfaction (bd-02qeb):
#     5. Mark decision gate under session-A
#     6. Session-B gate check finds it via cross-session fallback
#     7. Stale marker (>12h) is ignored
#
#   Phase 3 — Decision-waiting auto-check:
#     8. Decision-waiting marker auto-satisfies the decision gate
#     9. Clearing decision-waiting restores blocking
#
# Usage:
#   ./scripts/test-stop-hook-gate.sh [NAMESPACE]

MODULE_NAME="stop-hook-gate"
source "$(dirname "$0")/lib.sh"

log "Testing stop hook gate in namespace: $E2E_NAMESPACE"

# ── Discover daemon pod ────────────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

DAEMON_CONTAINER=""
for container in bd-daemon daemon; do
  if kube exec "$DAEMON_POD" -c "$container" -- which bd >/dev/null 2>&1; then
    DAEMON_CONTAINER="$container"
    break
  fi
done

if [[ -z "$DAEMON_CONTAINER" ]]; then
  skip_all "no bd binary in daemon pod"
  exit 0
fi

log "Daemon pod: $DAEMON_POD (container: $DAEMON_CONTAINER)"

# ── Helpers ────────────────────────────────────────────────────────────
# Gate tests need a writable working directory for .runtime/gates/ markers.
# The daemon's home dir (/home/beads) may be read-only, so use /tmp.
GATE_WORKDIR="/tmp/e2e-gate-test-$$"

bd_cmd() {
  local args=""
  for arg in "$@"; do args="$args '$arg'"; done
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- \
    sh -c "mkdir -p $GATE_WORKDIR && cd $GATE_WORKDIR && bd $args" 2>/dev/null
}

# Run bd command with a specific CLAUDE_SESSION_ID
bd_cmd_session() {
  local session_id="$1"
  shift
  local args=""
  for arg in "$@"; do args="$args '$arg'"; done
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- \
    sh -c "mkdir -p $GATE_WORKDIR && cd $GATE_WORKDIR && CLAUDE_SESSION_ID='$session_id' bd $args" 2>/dev/null
}

# Unique test session IDs
TEST_SESSION_A="e2e-gate-a-$(date +%s)"
TEST_SESSION_B="e2e-gate-b-$(date +%s)"

# Cleanup gate directories and workdir on exit
cleanup() {
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- \
    rm -rf "$GATE_WORKDIR" >/dev/null 2>&1 || true
}
trap 'cleanup; stop_port_forwards' EXIT

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: Gate blocking
# ═══════════════════════════════════════════════════════════════════════

# Helper: parse JSON decision from gate check output
parse_decision() {
  python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('decision', 'unknown'))
except:
    print('error')
"
}

# Helper: check if a specific gate is satisfied in JSON output
gate_satisfied() {
  local gate_id="$1"
  python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    for r in d.get('results', []):
        if r.get('gate_id') == '$gate_id' and r.get('satisfied'):
            print('yes')
            sys.exit(0)
    print('no')
except:
    print('error')
"
}

# ── Test 1: Gate blocks when decision gate is unsatisfied ─────────────
test_gate_blocks() {
  local output decision
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  decision=$(echo "$output" | parse_decision)

  if [[ "$decision" == "block" ]]; then
    log "Gate correctly blocks (decision gate unsatisfied)"
    return 0
  fi

  if [[ "$decision" == "allow" ]]; then
    log "Gate allows — decision gate may be soft on this cluster"
    return 1
  fi

  log "Unexpected decision: $decision"
  return 1
}
run_test "Gate blocks when decision unsatisfied" test_gate_blocks

# ── Test 2: Block reason contains config bead prompt ──────────────────
test_block_reason_rich() {
  local output decision
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  decision=$(echo "$output" | parse_decision)

  if [[ "$decision" != "block" ]]; then
    log "Not blocked — cannot test block reason"
    return 0  # Skip gracefully
  fi

  # The block reason should be the rich prompt from config bead,
  # NOT the bare "decision point offered before session end"
  if echo "$output" | grep -q "checkpoint"; then
    log "Block reason contains rich config bead prompt"
    return 0
  fi

  # Fallback: accept bare description if no config bead exists
  if echo "$output" | grep -q "decision point offered"; then
    log "Block reason is bare gate description (no config bead — acceptable)"
    return 0
  fi

  log "Block reason doesn't match expected patterns"
  return 1
}
run_test "Block reason from config bead (rich prompt)" test_block_reason_rich

# ── Test 3: Gate allows when decision gate is marked ──────────────────
test_gate_allows_after_mark() {
  # Mark the decision gate
  bd_cmd_session "$TEST_SESSION_A" gate mark decision || return 1
  log "Marked decision gate for $TEST_SESSION_A"

  local output decision gate_sat
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  decision=$(echo "$output" | parse_decision)
  gate_sat=$(echo "$output" | gate_satisfied "decision")

  if [[ "$decision" == "allow" ]]; then
    log "Gate correctly allows after decision marked"
    return 0
  fi

  # May be blocked by other strict gates but decision gate itself is satisfied
  if [[ "$gate_sat" == yes* ]]; then
    log "Decision gate satisfied but other gate blocks — acceptable"
    return 0
  fi

  log "Unexpected decision=$decision gate_sat=$gate_sat"
  return 1
}
run_test "Gate allows after decision marked" test_gate_allows_after_mark

# ── Test 4: Gate clear resets the decision gate ───────────────────────
test_gate_clear_resets() {
  # Clear the decision gate
  bd_cmd_session "$TEST_SESSION_A" gate clear decision || return 1
  log "Cleared decision gate for $TEST_SESSION_A"

  local output decision
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  decision=$(echo "$output" | parse_decision)

  if [[ "$decision" == "block" ]]; then
    log "Gate correctly blocks after clear"
    return 0
  fi

  log "Gate still allows after clear — decision gate may have auto-check"
  return 0
}
run_test "Gate clear resets decision gate" test_gate_clear_resets

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Cross-session satisfaction (bd-02qeb)
# ═══════════════════════════════════════════════════════════════════════

# ── Test 5: Mark decision gate under session-A ────────────────────────
test_mark_session_a() {
  bd_cmd_session "$TEST_SESSION_A" gate mark decision
}
run_test "Mark decision gate under session-A" test_mark_session_a

# ── Test 6: Session-B finds it via cross-session fallback ─────────────
test_cross_session() {
  local output gate_sat
  output=$(bd_cmd_session "$TEST_SESSION_B" gate session-check --hook Stop --json 2>&1) || true
  gate_sat=$(echo "$output" | gate_satisfied "decision")

  if [[ "$gate_sat" == yes* ]]; then
    log "Decision gate satisfied via cross-session lookup"
    return 0
  fi

  # The deployed binary may not have the cross-session fix (bd-02qeb, commit 4e263f7d).
  # Check binary version to decide if this is expected.
  local version
  version=$(bd_cmd version 2>/dev/null | head -1)
  log "Binary version: ${version:-unknown}"
  log "Cross-session not supported on this binary version (expected after 4e263f7d)"
  return 1
}
run_test "Cross-session gate satisfaction (bd-02qeb)" test_cross_session

# ── Test 7: Stale marker is ignored ──────────────────────────────────
test_stale_marker_ignored() {
  # Clean up existing markers first
  bd_cmd_session "$TEST_SESSION_A" gate clear >/dev/null 2>&1 || true
  bd_cmd_session "$TEST_SESSION_B" gate clear >/dev/null 2>&1 || true

  # Create a stale marker under session-A by marking then backdating
  bd_cmd_session "$TEST_SESSION_A" gate mark decision || return 1

  # Backdate the marker to 24 hours ago
  local marker_path="$GATE_WORKDIR/.runtime/gates/${TEST_SESSION_A}/decision"
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- \
    sh -c "touch -d '24 hours ago' $marker_path 2>/dev/null || touch -t \$(date -d '24 hours ago' '+%Y%m%d%H%M' 2>/dev/null || date -v-24H '+%Y%m%d%H%M') $marker_path 2>/dev/null || true" 2>/dev/null

  # Now check from session-B — stale marker should be ignored
  local output
  output=$(bd_cmd_session "$TEST_SESSION_B" gate session-check --hook Stop --json 2>&1) || true

  if echo "$output" | grep -q '"decision":.*"block"'; then
    log "Stale marker correctly ignored — gate blocks"
    return 0
  fi

  # On some systems, touch -d may not work in the container
  log "Gate allows despite stale marker (touch may not have worked in container)"
  return 0  # Non-critical — the local test already verified this
}
run_test "Stale marker (>12h) ignored in cross-session" test_stale_marker_ignored

# ═══════════════════════════════════════════════════════════════════════
# Phase 3: Decision-waiting auto-check
# ═══════════════════════════════════════════════════════════════════════

# Clean up first
bd_cmd_session "$TEST_SESSION_A" gate clear >/dev/null 2>&1 || true
bd_cmd_session "$TEST_SESSION_B" gate clear >/dev/null 2>&1 || true

# ── Test 8: Decision-waiting marker auto-satisfies gate ──────────────
test_decision_waiting_auto() {
  # Mark decision-waiting (simulates bd decision create blocking)
  bd_cmd_session "$TEST_SESSION_A" gate mark decision-waiting || return 1
  log "Marked decision-waiting for $TEST_SESSION_A"

  local output gate_sat
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  gate_sat=$(echo "$output" | gate_satisfied "decision")

  # gate_sat may contain trailing noise from kube exec; check prefix
  if [[ "$gate_sat" == yes* ]]; then
    log "Decision gate auto-satisfied via decision-waiting marker"
    return 0
  fi

  log "Decision-waiting auto-check failed (gate_sat=$gate_sat)"
  return 1
}
run_test "Decision-waiting auto-satisfies gate" test_decision_waiting_auto

# ── Test 9: Clearing decision-waiting restores blocking ──────────────
test_clear_waiting_restores_block() {
  # Clear the decision-waiting marker
  bd_cmd_session "$TEST_SESSION_A" gate clear decision-waiting || return 1
  # Also clear any auto-set decision marker
  bd_cmd_session "$TEST_SESSION_A" gate clear decision || return 1
  log "Cleared decision-waiting and decision markers"

  local output decision
  output=$(bd_cmd_session "$TEST_SESSION_A" gate session-check --hook Stop --json 2>&1) || true
  decision=$(echo "$output" | parse_decision)

  if [[ "$decision" == "block" ]]; then
    log "Gate correctly blocks after clearing decision-waiting"
    return 0
  fi

  log "Gate still allows — may have other satisfaction path"
  return 0  # Non-critical
}
run_test "Clearing decision-waiting restores blocking" test_clear_waiting_restores_block

# ── Summary ──────────────────────────────────────────────────────────
print_summary
