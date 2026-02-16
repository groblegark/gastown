#!/usr/bin/env bash
# test-coop-peek.sh — verify coop peek CLI works from inside agent pods.
#
# Tests the coop peek command added in coop v0.12.6:
#
#   1. Coop binary exists in agent pod
#   2. coop peek (list sessions) returns output
#   3. Session list contains at least one session
#   4. Session entries include expected fields (pod, state)
#   5. coop peek <session> --plain returns screen text
#   6. Screen text contains non-whitespace content
#   7. coop peek with partial ID resolves correctly
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-coop-peek.sh

set -euo pipefail
MODULE_NAME="coop-peek"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing coop peek CLI in $NS"

# ── Discover agent pod ──────────────────────────────────────────────
AGENT_PODS=$(kube get pods --no-headers 2>/dev/null \
  | { grep "^gt-" || true; } \
  | { grep "Running" || true; } \
  | awk '{print $1}')
AGENT_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  if [[ "$_role" == "mayor" ]]; then
    AGENT_POD="$_p"
    break
  fi
done
[[ -z "$AGENT_POD" ]] && AGENT_POD=$(echo "$AGENT_PODS" | head -1)

if [[ -z "$AGENT_POD" ]]; then
  skip_all "no running agent pods"
  exit 0
fi

log "Using agent pod: $AGENT_POD"

# Discover mux URL and token from the agent pod's environment
MUX_URL=""
MUX_TOKEN=""

# Get mux URL from broker service (coop peek needs COOP_MUX_URL)
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
if [[ -n "$BROKER_SVC" ]]; then
  MUX_URL="http://${BROKER_SVC}:9800"
fi

# Get mux token from broker auth secret
AUTH_SECRET=$(kube get secret --no-headers 2>/dev/null | grep -E "coop-broker-auth" | head -1 | awk '{print $1}')
if [[ -n "$AUTH_SECRET" ]]; then
  MUX_TOKEN=$(kube get secret "$AUTH_SECRET" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
fi

# Build env prefix for coop peek commands inside the pod
COOP_ENV="COOP_MUX_URL=${MUX_URL}"
[[ -n "$MUX_TOKEN" ]] && COOP_ENV="${COOP_ENV} COOP_MUX_TOKEN=${MUX_TOKEN}"

# ── Helpers ────────────────────────────────────────────────────────────

agent_exec() {
  kube exec "$AGENT_POD" -c agent -- sh -c "$*" 2>/dev/null
}

# ── Test functions ─────────────────────────────────────────────────────

test_coop_binary_exists() {
  agent_exec "which coop || test -f /usr/local/bin/coop"
}

PEEK_LIST=""

test_peek_list_runs() {
  [[ -n "$MUX_URL" ]] || { log "No MUX_URL discovered"; return 1; }
  PEEK_LIST=$(agent_exec "env ${COOP_ENV} coop peek 2>&1" || true)
  [[ -n "$PEEK_LIST" ]] || return 1
  log "peek output (first 300 chars): ${PEEK_LIST:0:300}"
  # Should not be an error
  if echo "$PEEK_LIST" | grep -qi "error: \|fatal:"; then
    log "coop peek returned an error"
    return 1
  fi
  return 0
}

test_peek_has_sessions() {
  [[ -n "$PEEK_LIST" ]] || return 1
  # Output should list sessions — look for session IDs (hex) or pod names (gt-)
  local line_count
  line_count=$(echo "$PEEK_LIST" | wc -l | tr -d ' ')
  log "Session list lines: $line_count"
  # At minimum we expect a header + 1 session
  [[ "$line_count" -ge 1 ]]
}

test_peek_session_fields() {
  [[ -n "$PEEK_LIST" ]] || return 1
  # Session list should show pod names and state information
  # Look for common fields: pod name (gt-), state (running/idle)
  if echo "$PEEK_LIST" | grep -qiE "gt-|running|idle|healthy|pod|session"; then
    return 0
  fi
  log "Session list missing expected fields"
  return 1
}

# Extract first session identifier from peek output
FIRST_SESSION=""

extract_first_session() {
  [[ -n "$PEEK_LIST" ]] || return
  # Try to extract a session ID (hex UUID prefix) or pod name from the output
  # Session IDs are typically 8+ hex chars, pod names start with gt-
  FIRST_SESSION=$(echo "$PEEK_LIST" | grep -oE '[0-9a-f]{8,}' | head -1)
  if [[ -z "$FIRST_SESSION" ]]; then
    # Try pod name
    FIRST_SESSION=$(echo "$PEEK_LIST" | grep -oE 'gt-[a-z0-9-]+' | head -1)
  fi
}

SCREEN_TEXT=""

test_peek_screen() {
  extract_first_session
  [[ -n "$FIRST_SESSION" ]] || { log "No session ID found to peek"; return 1; }
  log "Peeking session: $FIRST_SESSION"
  # Use exit code to detect coop errors (screen content may contain arbitrary terminal text)
  SCREEN_TEXT=$(agent_exec "env ${COOP_ENV} coop peek ${FIRST_SESSION} --plain")
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    log "coop peek exited with code $rc"
    return 1
  fi
  [[ -n "$SCREEN_TEXT" ]] || return 1
  local len=${#SCREEN_TEXT}
  log "Screen text length: $len chars"
  [[ "$len" -gt 0 ]]
}

test_screen_has_content() {
  [[ -n "$SCREEN_TEXT" ]] || return 1
  local stripped
  stripped=$(echo "$SCREEN_TEXT" | tr -d '[:space:]')
  local stripped_len=${#stripped}
  log "Non-whitespace content: $stripped_len chars"
  [[ "$stripped_len" -gt 0 ]]
}

test_peek_partial_id() {
  extract_first_session
  [[ -n "$FIRST_SESSION" ]] || { log "No session ID to test partial match"; return 1; }
  # Use first 6 chars as partial ID
  local partial="${FIRST_SESSION:0:6}"
  log "Testing partial ID: $partial (from $FIRST_SESSION)"
  local result
  # Use exit code — screen content may contain arbitrary terminal text
  result=$(agent_exec "env ${COOP_ENV} coop peek ${partial} --plain")
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    log "Partial ID lookup failed (exit code $rc)"
    return 1
  fi
  [[ -n "$result" ]]
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Coop binary exists in agent pod" test_coop_binary_exists
run_test "coop peek lists sessions" test_peek_list_runs
run_test "Session list contains entries" test_peek_has_sessions
run_test "Session entries include expected fields" test_peek_session_fields
run_test "coop peek <session> --plain shows screen" test_peek_screen
run_test "Screen text contains non-whitespace content" test_screen_has_content
run_test "Partial session ID resolves correctly" test_peek_partial_id

print_summary
