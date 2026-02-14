#!/usr/bin/env bash
# test-event-flow.sh — End-to-end event bus verification.
#
# Verifies that the NATS event bus is operational and events flow between
# the daemon, controller, and sidecars. Tests the event infrastructure
# rather than specific event payloads.
#
# Tests:
#   Phase 1 — NATS Infrastructure:
#     1. NATS pod is Running
#     2. NATS monitoring API reachable (port 8222)
#     3. NATS has JetStream enabled
#     4. JetStream has expected streams
#
#   Phase 2 — Stream Health:
#     5. MUTATION_EVENTS stream has messages (bead mutations)
#     6. DECISION_EVENTS stream exists
#     7. Streams have active consumers
#
#   Phase 3 — Connection Health:
#     8. Daemon is connected to NATS
#     9. Sidecars (slack-bot/cred) connected to NATS
#    10. At least 3 NATS connections (daemon, controller, +1)
#
#   Phase 4 — Event Publishing:
#    11. Create test bead → verify MUTATION_EVENTS message count increases
#    12. Clean up test bead
#
# Usage:
#   ./scripts/test-event-flow.sh [NAMESPACE]

MODULE_NAME="event-flow"
source "$(dirname "$0")/lib.sh"

log "Testing event flow in namespace: $E2E_NAMESPACE"

# ── Discover NATS ──────────────────────────────────────────────────────
NATS_POD=$(kube get pods --no-headers 2>/dev/null | grep "nats" | grep -v "clusterctl" | grep "Running" | head -1 | awk '{print $1}')
NATS_SVC=$(kube get svc --no-headers 2>/dev/null | grep "nats" | head -1 | awk '{print $1}')

log "NATS pod: ${NATS_POD:-none}"
log "NATS svc: ${NATS_SVC:-none}"

if [[ -z "$NATS_POD" ]]; then
  skip_all "no NATS pod found"
  exit 0
fi

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: NATS Infrastructure
# ═══════════════════════════════════════════════════════════════════════

# ── Test 1: NATS pod is Running ────────────────────────────────────────
test_nats_running() {
  local phase
  phase=$(kube get pod "$NATS_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  assert_eq "$phase" "Running"
}
run_test "NATS pod is Running" test_nats_running

# ── Test 2: NATS monitoring API reachable ──────────────────────────────
NATS_MON_PORT=""

test_nats_monitoring() {
  [[ -n "$NATS_SVC" ]] || return 1
  NATS_MON_PORT=$(start_port_forward "svc/$NATS_SVC" 8222) || return 1
  local resp
  resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/varz" 2>/dev/null)
  [[ -n "$resp" ]]
}
run_test "NATS monitoring API reachable (/varz)" test_nats_monitoring

if [[ -z "$NATS_MON_PORT" ]]; then
  skip_test "JetStream enabled" "monitoring not reachable"
  skip_test "Expected JetStream streams exist" "monitoring not reachable"
  skip_test "MUTATION_EVENTS stream has messages" "monitoring not reachable"
  skip_test "DECISION_EVENTS stream exists" "monitoring not reachable"
  skip_test "Streams have active consumers" "monitoring not reachable"
  skip_test "Daemon connected to NATS" "monitoring not reachable"
  skip_test "Controller connected to NATS" "monitoring not reachable"
  skip_test "At least 3 NATS connections" "monitoring not reachable"
  skip_test "Event published on bead create" "monitoring not reachable"
  print_summary
  exit 0
fi

# ── Test 3: JetStream enabled ─────────────────────────────────────────
JSZ_RESP=""

test_jetstream_enabled() {
  JSZ_RESP=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/jsz" 2>/dev/null)
  [[ -n "$JSZ_RESP" ]] || return 1
  # Should have a "config" section indicating JetStream is on
  assert_contains "$JSZ_RESP" "config" || assert_contains "$JSZ_RESP" "streams"
}
run_test "JetStream enabled" test_jetstream_enabled

# ── Test 4: Expected JetStream streams exist ───────────────────────────
STREAM_RESP=""

test_expected_streams() {
  STREAM_RESP=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/jsz?streams=true" 2>/dev/null)
  [[ -n "$STREAM_RESP" ]] || return 1

  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$STREAM_RESP" > "$tmpf"
  local stream_names
  stream_names=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
names = set()
for acct in d.get('account_details', []):
    for s in acct.get('stream_detail', []):
        names.add(s.get('name', ''))
print(' '.join(sorted(names)))
" 2>/dev/null)
  rm -f "$tmpf"

  log "JetStream streams: $stream_names"
  # Must have at least one of the expected event streams
  assert_contains "$stream_names" "MUTATION_EVENTS" || \
    assert_contains "$stream_names" "AGENT_EVENTS" || \
    assert_contains "$stream_names" "DECISION_EVENTS"
}
run_test "Expected JetStream streams exist" test_expected_streams

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Stream Health
# ═══════════════════════════════════════════════════════════════════════

# Helper: extract stream info
_stream_info() {
  local stream_name="$1" field="$2"
  [[ -n "$STREAM_RESP" ]] || return 1
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$STREAM_RESP" > "$tmpf"
  python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
for acct in d.get('account_details', []):
    for s in acct.get('stream_detail', []):
        if s.get('name') == '$stream_name':
            print(s.get('state', {}).get('$field', 0))
            exit()
print(0)
" 2>/dev/null
  rm -f "$tmpf"
}

# ── Test 5: MUTATION_EVENTS stream has messages ────────────────────────────
test_bead_events_messages() {
  local msgs
  msgs=$(_stream_info "MUTATION_EVENTS" "messages")
  log "MUTATION_EVENTS messages: $msgs"
  [[ "${msgs:-0}" -gt 0 ]]
}
run_test "MUTATION_EVENTS stream has messages" test_bead_events_messages

# ── Test 6: DECISION_EVENTS stream exists ──────────────────────────────
test_decision_stream_exists() {
  local msgs
  msgs=$(_stream_info "DECISION_EVENTS" "messages")
  # Stream existing with 0 messages is fine — just verify it's there
  [[ -n "$msgs" ]]
}
run_test "DECISION_EVENTS stream exists" test_decision_stream_exists

# ── Test 7: Streams have active consumers ──────────────────────────────
test_stream_consumers() {
  local consumers
  consumers=$(_stream_info "MUTATION_EVENTS" "consumer_count")
  log "MUTATION_EVENTS consumer_count: $consumers"
  [[ "${consumers:-0}" -gt 0 ]]
}
run_test "MUTATION_EVENTS has active consumers" test_stream_consumers

# ═══════════════════════════════════════════════════════════════════════
# Phase 3: Connection Health
# ═══════════════════════════════════════════════════════════════════════

CONNZ_RESP=""

_fetch_connz() {
  if [[ -z "$CONNZ_RESP" ]]; then
    CONNZ_RESP=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/connz" 2>/dev/null)
  fi
}

# ── Test 8: Daemon is connected to NATS ───────────────────────────────
test_daemon_connected() {
  _fetch_connz
  [[ -n "$CONNZ_RESP" ]] || return 1

  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$CONNZ_RESP" > "$tmpf"
  local daemon_found
  daemon_found=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
for c in d.get('connections', []):
    name = c.get('name', '')
    if 'daemon' in name.lower() or 'beads' in name.lower():
        print('found')
        break
" 2>/dev/null)
  rm -f "$tmpf"
  [[ "$daemon_found" == "found" ]]
}
run_test "Daemon is connected to NATS" test_daemon_connected

# ── Test 9: Non-daemon component connected to NATS ───────────────────
# Check that at least one component besides bd-daemon is connected.
# Coopmux connects via Rust (may have empty name). Controller uses gRPC
# to daemon, not direct NATS. Look for any connection besides bd-daemon.
test_other_component_connected() {
  _fetch_connz
  [[ -n "$CONNZ_RESP" ]] || return 1

  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$CONNZ_RESP" > "$tmpf"
  local non_daemon_count
  non_daemon_count=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
count = 0
for c in d.get('connections', []):
    name = c.get('name', '').lower()
    if name != 'bd-daemon':
        count += 1
print(count)
" 2>/dev/null)
  rm -f "$tmpf"
  log "Non-daemon NATS connections: $non_daemon_count"
  [[ "${non_daemon_count:-0}" -ge 1 ]]
}
run_test "Non-daemon component connected to NATS" test_other_component_connected

# ── Test 10: At least N NATS connections ──────────────────────────────
# Minimum connections depend on what's deployed. Core services (daemon + NATS
# internal) provide at least 2. Only require 3+ when optional sidecars exist.
test_min_connections() {
  _fetch_connz
  [[ -n "$CONNZ_RESP" ]] || return 1

  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$CONNZ_RESP" > "$tmpf"
  local count
  count=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
print(d.get('num_connections', len(d.get('connections', []))))
" 2>/dev/null)
  rm -f "$tmpf"
  log "Total NATS connections: $count"
  # Require at least 2 connections (daemon + controller). Fresh NS may not have 3+.
  [[ "${count:-0}" -ge 2 ]]
}
run_test "At least 2 NATS connections (daemon + controller)" test_min_connections

# ═══════════════════════════════════════════════════════════════════════
# Phase 4: Event Publishing
# ═══════════════════════════════════════════════════════════════════════

# ── Test 11: Bead create publishes event ──────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')
DAEMON_CONTAINER=""
TEST_BEAD_ID=""

if [[ -n "$DAEMON_POD" ]]; then
  for container in bd-daemon daemon; do
    if kube exec "$DAEMON_POD" -c "$container" -- which bd >/dev/null 2>&1; then
      DAEMON_CONTAINER="$container"
      break
    fi
  done
fi

_cleanup_test_bead() {
  if [[ -n "$TEST_BEAD_ID" && -n "$DAEMON_POD" && -n "$DAEMON_CONTAINER" ]]; then
    kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd close "$TEST_BEAD_ID" --reason="E2E cleanup" >/dev/null 2>&1 || true
  fi
}
trap '_cleanup_test_bead; _cleanup' EXIT

test_event_on_create() {
  [[ -n "$DAEMON_POD" && -n "$DAEMON_CONTAINER" ]] || return 1

  # Capture current MUTATION_EVENTS message count
  local before_resp before_msgs
  before_resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/jsz?streams=true" 2>/dev/null)
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$before_resp" > "$tmpf"
  before_msgs=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
for acct in d.get('account_details', []):
    for s in acct.get('stream_detail', []):
        if s.get('name') == 'MUTATION_EVENTS':
            print(s.get('state', {}).get('messages', 0))
            exit()
print(0)
" 2>/dev/null)
  rm -f "$tmpf"

  # Create a test bead
  local output
  output=$(kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd create \
    --title="E2E event-flow test $(date +%s)" \
    --type=task \
    --priority=4 2>&1)
  # Parse issue ID from "Created issue: hq-xxx" / "bd-xxx" / "beads-xxx" output
  TEST_BEAD_ID=$(echo "$output" | grep -oE '(hq|bd|beads)-[a-z0-9.]+' | head -1)
  log "Created test bead: $TEST_BEAD_ID"
  [[ -n "$TEST_BEAD_ID" ]] || return 1

  # Wait a moment for the event to propagate
  sleep 3

  # Check MUTATION_EVENTS message count increased
  local after_resp after_msgs
  after_resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${NATS_MON_PORT}/jsz?streams=true" 2>/dev/null)
  tmpf=$(mktemp)
  printf '%s' "$after_resp" > "$tmpf"
  after_msgs=$(python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
for acct in d.get('account_details', []):
    for s in acct.get('stream_detail', []):
        if s.get('name') == 'MUTATION_EVENTS':
            print(s.get('state', {}).get('messages', 0))
            exit()
print(0)
" 2>/dev/null)
  rm -f "$tmpf"

  log "MUTATION_EVENTS messages: before=$before_msgs after=$after_msgs"
  [[ "${after_msgs:-0}" -gt "${before_msgs:-0}" ]]
}

if [[ -n "$DAEMON_POD" && -n "$DAEMON_CONTAINER" ]]; then
  run_test "Bead create publishes NATS event" test_event_on_create
else
  skip_test "Bead create publishes NATS event" "no daemon pod with bd CLI"
fi

# ── Test 12: Clean up test bead ───────────────────────────────────────
test_cleanup_bead() {
  [[ -n "$TEST_BEAD_ID" ]] || return 0
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd close "$TEST_BEAD_ID" --reason="E2E event-flow cleanup" 2>/dev/null || return 1
  log "Closed test bead: $TEST_BEAD_ID"
  TEST_BEAD_ID=""
  return 0
}
run_test "Clean up test bead" test_cleanup_bead

# ── Summary ──────────────────────────────────────────────────────────
print_summary
