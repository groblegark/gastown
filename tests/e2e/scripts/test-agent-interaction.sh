#!/usr/bin/env bash
# test-agent-interaction.sh — Exercise real Claude Code agent capabilities.
#
# Goes beyond the round-trip smoke test to verify Claude can actually DO things:
#   1. Agent is idle and ready
#   2. Create a file via prompt (tool use: Write)
#   3. File exists on pod filesystem
#   4. Read file back via prompt (tool use: Read/Bash)
#   5. Multi-turn context: reference the file without naming it
#   6. Agent handles math/reasoning (not just echo)
#   7. Clean up: delete the test file
#   8. Agent stays healthy through all interactions
#
# IMPORTANT: This test sends multiple real prompts to a live Claude agent.
# Each prompt costs tokens. Prompts are designed to be minimal.
#
# Usage:
#   ./scripts/test-agent-interaction.sh [NAMESPACE]

MODULE_NAME="agent-interaction"
source "$(dirname "$0")/lib.sh"

log "Testing real Claude Code interactions in namespace: $E2E_NAMESPACE"

# ── Configuration ────────────────────────────────────────────────────
WORK_TIMEOUT=90     # seconds to wait for each prompt to complete
TEST_FILE="/tmp/e2e-interaction-test-$(date +%s).txt"
TEST_CONTENT="hello from e2e test"

# ── Discover agent pods ──────────────────────────────────────────────
AGENT_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | { grep "Running" || true; } | awk '{print $1}')
AGENT_POD=$(echo "$AGENT_PODS" | head -1)

if [[ -z "$AGENT_POD" ]]; then
  skip_test "Agent is idle and ready" "no running agent pods"
  skip_test "Create file via prompt (tool use)" "no running agent pods"
  skip_test "File exists on pod filesystem" "no running agent pods"
  skip_test "Read file contents via prompt" "no running agent pods"
  skip_test "Multi-turn: reference previous file" "no running agent pods"
  skip_test "Reasoning: solve a word problem" "no running agent pods"
  skip_test "Clean up test file" "no running agent pods"
  skip_test "Agent healthy after all interactions" "no running agent pods"
  print_summary
  exit 0
fi

log "Using agent pod: $AGENT_POD"

# ── Port-forward to agent's main API port (8080) ─────────────────────
COOP_PORT=""

setup_coop() {
  if [[ -z "$COOP_PORT" ]]; then
    COOP_PORT=$(start_port_forward "pod/$AGENT_POD" 8080) || return 1
  fi
}

# Helper: get agent state
get_state() {
  local resp tmpf state
  resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${COOP_PORT}/api/v1/agent" 2>/dev/null) || echo '{}'
  tmpf=$(mktemp)
  printf '%s' "$resp" > "$tmpf"
  state=$(python3 -c "
import json
with open('$tmpf') as f:
    print(json.load(f).get('state', 'unknown'))
" 2>/dev/null)
  rm -f "$tmpf"
  echo "$state"
}

# Helper: send a prompt and wait for completion.
# Returns 0 if agent returns to idle, 1 on timeout/error.
# Usage: send_prompt "prompt text"
send_prompt() {
  local prompt="$1"
  local timeout="${2:-$WORK_TIMEOUT}"

  # Capture screen_seq before sending so we can detect when output changes
  local before_seq
  before_seq=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${COOP_PORT}/api/v1/agent" 2>/dev/null \
    | python3 -c "import sys,json; print(json.load(sys.stdin).get('screen_seq',0))" 2>/dev/null || echo "0")

  # Build JSON body safely with python3
  local bodyfile
  bodyfile=$(mktemp)
  python3 -c "
import json
with open('$bodyfile', 'w') as f:
    json.dump({'text': '''$prompt''', 'enter': True}, f)
" 2>/dev/null

  # Send input
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    -X POST -H "Content-Type: application/json" \
    -d "@${bodyfile}" \
    "http://127.0.0.1:${COOP_PORT}/api/v1/input" 2>/dev/null)
  rm -f "$bodyfile"

  if [[ "$status" != "200" && "$status" != "204" ]]; then
    log "Input API returned HTTP $status"
    return 1
  fi

  # Phase 1: Wait for agent to leave idle (start working).
  # Must see working/tool_use OR screen_seq advance past our baseline.
  local saw_work=false
  local phase1_deadline=$((SECONDS + 30))
  while [[ $SECONDS -lt $phase1_deadline ]]; do
    local state
    state=$(get_state)
    if [[ "$state" == "working" || "$state" == "tool_use" || "$state" == "tool_input" ]]; then
      saw_work=true
      break
    fi
    # Check if screen advanced (means agent already processed and returned)
    local cur_seq
    cur_seq=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${COOP_PORT}/api/v1/agent" 2>/dev/null \
      | python3 -c "import sys,json; print(json.load(sys.stdin).get('screen_seq',0))" 2>/dev/null || echo "0")
    if [[ "$cur_seq" -gt "$before_seq" && "$state" == "idle" ]]; then
      saw_work=true
      break
    fi
    sleep 1
  done

  if [[ "$saw_work" != "true" ]]; then
    log "Agent did not start working within 30s"
    return 1
  fi

  # Phase 2: Wait for agent to return to idle.
  local deadline=$((SECONDS + timeout))
  while [[ $SECONDS -lt $deadline ]]; do
    local state
    state=$(get_state)
    case "$state" in
      idle) return 0 ;;
      exited|error)
        log "Agent in bad state: $state"
        return 1
        ;;
    esac
    sleep 2
  done

  log "Agent did not return to idle within ${timeout}s (state: $(get_state))"
  return 1
}

# Helper: get screen text
get_screen() {
  curl -sf --connect-timeout 5 "http://127.0.0.1:${COOP_PORT}/api/v1/screen/text" 2>/dev/null
}

# ── Test 1: Agent is idle and ready ───────────────────────────────────
test_agent_ready() {
  setup_coop || return 1
  local state
  state=$(get_state)
  log "Agent state: $state"
  [[ "$state" == "idle" ]]
}
run_test "Agent is idle and ready for interaction" test_agent_ready

AGENT_STATE=$(get_state 2>/dev/null)
if [[ "$AGENT_STATE" != "idle" ]]; then
  skip_test "Create file via prompt (tool use)" "agent not idle (state: ${AGENT_STATE:-unknown})"
  skip_test "File exists on pod filesystem" "agent not idle"
  skip_test "Read file contents via prompt" "agent not idle"
  skip_test "Multi-turn: reference previous file" "agent not idle"
  skip_test "Reasoning: solve a word problem" "agent not idle"
  skip_test "Clean up test file" "agent not idle"
  skip_test "Agent healthy after all interactions" "agent not idle"
  print_summary
  exit 0
fi

# ── Test 2: Create a file via prompt (exercises Write tool) ───────────
test_create_file() {
  log "Asking Claude to create $TEST_FILE..."
  send_prompt "Create a file at $TEST_FILE with the exact content: $TEST_CONTENT — do not add anything else to the file, just that exact text."
}
run_test "Create file via prompt (Write tool)" test_create_file

# ── Test 3: File exists on pod filesystem ─────────────────────────────
test_file_exists() {
  # Verify directly on the pod — this proves the Write tool actually worked
  local content
  content=$(kube exec "$AGENT_POD" -- cat "$TEST_FILE" 2>/dev/null)
  log "File content: '$content'"
  assert_contains "$content" "$TEST_CONTENT"
}
run_test "File exists on pod with correct content" test_file_exists

# ── Test 4: Read file via prompt (exercises Read/Bash tool) ───────────
test_read_file() {
  log "Asking Claude to read $TEST_FILE..."
  send_prompt "Read $TEST_FILE and show me its contents."
  # Verify by checking screen for the test content
  local screen
  screen=$(get_screen)
  assert_contains "$screen" "$TEST_CONTENT"
}
run_test "Read file contents via prompt (Read tool)" test_read_file

# ── Test 5: Multi-turn context ────────────────────────────────────────
test_multi_turn() {
  log "Testing multi-turn context..."
  send_prompt "How many words are in that file you just read? Reply with just the number."
  local screen
  screen=$(get_screen)
  # "hello from e2e test" = 4 words
  assert_contains "$screen" "4"
}
run_test "Multi-turn: agent remembers previous context" test_multi_turn

# ── Test 6: Reasoning ─────────────────────────────────────────────────
test_reasoning() {
  log "Testing reasoning capability..."
  send_prompt "What is 7 times 8? Reply with just the number."
  local screen
  screen=$(get_screen)
  assert_contains "$screen" "56"
}
run_test "Reasoning: solves math correctly (56)" test_reasoning

# ── Test 7: Clean up test file ────────────────────────────────────────
test_cleanup_file() {
  log "Asking Claude to delete $TEST_FILE..."
  send_prompt "Delete the file at $TEST_FILE"
  # Verify file is gone
  if kube exec "$AGENT_POD" -- test -f "$TEST_FILE" 2>/dev/null; then
    log "File still exists after deletion request"
    return 1
  fi
  return 0
}
run_test "Clean up: delete test file via prompt" test_cleanup_file

# ── Test 8: Agent still healthy after all interactions ────────────────
test_agent_healthy() {
  local state
  state=$(get_state)
  log "Final agent state: $state"
  # Agent should be idle, not crashed
  [[ "$state" == "idle" ]]
}
run_test "Agent healthy after all interactions (still idle)" test_agent_healthy

# ── Summary ──────────────────────────────────────────────────────────
print_summary
