#!/usr/bin/env bash
# test-slack-bot-decisions.sh — Verify Slack bot processes decision events end-to-end.
#
# Unlike slack-bot-health (which checks container/NATS health), this test
# verifies the bot actually PROCESSES decision events rather than silently
# ignoring them. This catches subject-parsing bugs like the scoped-subject
# issue (decisions.<scope>.<EventType> vs decisions.<EventType>).
#
# Tests:
#   1. Create a test decision via daemon RPC
#   2. Verify NATS event published (decision stream has new message)
#   3. Verify slack-bot logs show event was DISPATCHED (not ignored)
#   4. Verify no "ignoring event type" log for the test decision
#   5. Clean up: resolve the test decision
#
# Usage:
#   ./scripts/test-slack-bot-decisions.sh [NAMESPACE]

MODULE_NAME="slack-bot-decisions"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing Slack bot decision processing in $NS"

# ── Discover pods & services ──────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')
DAEMON_SVC=$(kube get svc --no-headers 2>/dev/null | grep "daemon" | head -1 | awk '{print $1}')

log "Daemon pod: ${DAEMON_POD:-none}"

# ── Early exit: skip all if slack-bot sidecar not deployed ────────
if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

CONTAINERS=$(kube get pod "$DAEMON_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
if [[ "$CONTAINERS" != *"slack-bot"* ]]; then
  skip_all "slack-bot sidecar not deployed"
  exit 0
fi

# ── Setup port-forward to daemon ──────────────────────────────────
DAEMON_PORT=""
setup_daemon() {
  if [[ -z "$DAEMON_PORT" ]]; then
    if [[ -n "$DAEMON_SVC" ]]; then
      DAEMON_PORT=$(start_port_forward "svc/$DAEMON_SVC" 9080) || return 1
    fi
  fi
}

# Helper: call daemon API
daemon_api() {
  local method="$1" path="$2" body="$3"
  if [[ -n "$body" ]]; then
    curl -sf --connect-timeout 5 -X "$method" \
      -H "Content-Type: application/json" \
      -d "$body" \
      "http://127.0.0.1:${DAEMON_PORT}${path}" 2>/dev/null
  else
    curl -sf --connect-timeout 5 -X "$method" \
      "http://127.0.0.1:${DAEMON_PORT}${path}" 2>/dev/null
  fi
}

# ── Test 1: Create a test decision via daemon ─────────────────────
DECISION_ID=""
E2E_MARKER="e2e-slackdec-$(date +%s)"

test_create_decision() {
  setup_daemon || return 1

  local result
  result=$(daemon_api POST "/api/v1/decision" \
    "{\"prompt\":\"E2E slack-bot test ${E2E_MARKER}: approve?\",\"options\":[{\"id\":\"y\",\"label\":\"Yes\"},{\"id\":\"n\",\"label\":\"No\"}]}")
  [[ -n "$result" ]] || return 1

  DECISION_ID=$(echo "$result" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(d.get('id', d.get('decision_id', '')))
" 2>/dev/null)
  log "Created test decision: $DECISION_ID"
  [[ -n "$DECISION_ID" ]]
}
run_test "Create test decision via daemon" test_create_decision

# Give the event time to propagate
sleep 3

# ── Test 2: Slack-bot received the NATS event ────────────────────
test_bot_received_event() {
  [[ -n "$DECISION_ID" ]] || return 1

  local logs
  logs=$(kube logs "$DAEMON_POD" -c slack-bot --tail=50 --since=30s 2>/dev/null)
  [[ -n "$logs" ]] || return 1

  # Bot should log that it received a message mentioning our decision ID
  assert_contains "$logs" "$DECISION_ID"
}
run_test "Slack-bot received NATS event for test decision" test_bot_received_event

# ── Test 3: Event was dispatched (not ignored) ───────────────────
test_event_not_ignored() {
  [[ -n "$DECISION_ID" ]] || return 1

  local logs
  logs=$(kube logs "$DAEMON_POD" -c slack-bot --tail=50 --since=30s 2>/dev/null)

  # Check that the event was NOT ignored
  local ignored_lines
  ignored_lines=$(echo "$logs" | grep "ignoring event type" | grep "$DECISION_ID" | wc -l | tr -d ' ')
  log "Lines with 'ignoring event type' for $DECISION_ID: $ignored_lines"
  assert_eq "$ignored_lines" "0"
}
run_test "Decision event was dispatched (not ignored)" test_event_not_ignored

# ── Test 4: Bot attempted to process the decision ────────────────
test_event_processed() {
  [[ -n "$DECISION_ID" ]] || return 1

  local logs
  logs=$(kube logs "$DAEMON_POD" -c slack-bot --tail=50 --since=30s 2>/dev/null)

  # Should see one of: "notifying new decision", "notify decision", "fetch decision"
  # Any of these indicates the event was dispatched to the handler, not ignored
  if echo "$logs" | grep -q "notify.*$DECISION_ID\|fetch.*$DECISION_ID"; then
    log "Bot processed decision $DECISION_ID"
    return 0
  fi

  # If the decision was already resolved by the time the bot fetched it, that's fine too
  if echo "$logs" | grep -q "already resolved.*$DECISION_ID"; then
    log "Decision $DECISION_ID was already resolved (race OK)"
    return 0
  fi

  log "No processing evidence found for $DECISION_ID"
  return 1
}
run_test "Bot attempted to process the decision" test_event_processed

# ── Cleanup: resolve the test decision ────────────────────────────
if [[ -n "$DECISION_ID" && -n "$DAEMON_PORT" ]]; then
  daemon_api POST "/api/v1/decision/${DECISION_ID}/respond" \
    "{\"option\":\"y\",\"by\":\"e2e-test\"}" >/dev/null 2>&1 || true
  log "Cleaned up test decision $DECISION_ID"
fi

# ── Summary ──────────────────────────────────────────────────────
print_summary
