#!/usr/bin/env bash
# test-mux-restart-persistence.sh — Verify mux sessions survive broker restart.
#
# Tests that agent sessions persist or re-register after the coop broker restarts:
#   1. Daemon and mux APIs are reachable
#   2. Create a test agent bead → pod → mux session
#   3. Verify session is visible in mux
#   4. Restart the coop broker pod (delete and wait for replacement)
#   5. Verify session re-appears in mux after broker restart
#   6. Clean up: close bead, verify pod deletion
#
# KNOWN ISSUE (bd-zpcb7): As of 2026-02-13, coop agents register with the mux
# only once at startup. After a broker restart, sessions are lost and do NOT
# automatically re-register. This test documents the current (broken) behavior
# and will pass once the fix is implemented (controller-driven mux registration
# or coop heartbeat/re-registration).
#
# Usage:
#   ./scripts/test-mux-restart-persistence.sh [NAMESPACE]

MODULE_NAME="mux-restart-persistence"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing mux session persistence across broker restart in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
MUX_SESSION_TIMEOUT=120
MUX_SESSION_RESTORE_TIMEOUT=180  # seconds to wait for session to re-appear after restart
BROKER_RESTART_TIMEOUT=120
POD_DELETE_TIMEOUT=180

TEST_BEAD_TITLE="e2e-mux-persist-$(date +%s)"
TEST_BEAD_ID=""
TEST_POD_NAME=""
PRE_EXISTING_PODS=""

# ── Discover daemon ──────────────────────────────────────────────────
DAEMON_POD=""
DAEMON_PORT=""
DAEMON_TOKEN=""

discover_daemon() {
  DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$DAEMON_POD" ]] || return 1

  DAEMON_TOKEN=$(kube exec "$DAEMON_POD" -c bd-daemon -- printenv BD_DAEMON_TOKEN 2>/dev/null) || true
  [[ -n "$DAEMON_TOKEN" ]]
}

# ── Discover mux ─────────────────────────────────────────────────────
MUX_PORT=""
MUX_TOKEN=""
BROKER_SVC=""

discover_mux() {
  local broker_pod
  broker_pod=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$broker_pod" ]] || return 1

  # Container is "coopmux" (single all-in-one binary, NOT "coop-mux")
  MUX_TOKEN=$(kube exec "$broker_pod" -c coopmux -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null) || true
  [[ -n "$MUX_TOKEN" ]] || return 1

  BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  [[ -n "$BROKER_SVC" ]]
}

daemon_api() {
  local method="$1"
  local bodyfile="${2:-}"
  [[ -n "$DAEMON_PORT" ]] || return 1

  local respfile
  respfile=$(mktemp)

  local curl_args=(-s --connect-timeout 10 -o "$respfile" -w "%{http_code}" -X POST
    -H "Content-Type: application/json"
    -H "Authorization: Bearer $DAEMON_TOKEN")

  if [[ -n "$bodyfile" && -f "$bodyfile" ]]; then
    curl_args+=(-d "@${bodyfile}")
  else
    curl_args+=(-d '{}')
  fi

  curl_args+=("http://127.0.0.1:${DAEMON_PORT}/bd.v1.BeadsService/${method}")

  local http_code
  http_code=$(curl "${curl_args[@]}" 2>/dev/null)

  if [[ "$http_code" -ge 200 && "$http_code" -lt 300 ]]; then
    cat "$respfile"
    rm -f "$respfile"
  else
    log "daemon_api $method: HTTP $http_code — $(cat "$respfile" 2>/dev/null)"
    rm -f "$respfile"
    return 1
  fi
}

mux_sessions() {
  [[ -n "$MUX_PORT" ]] || return 1
  curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $MUX_TOKEN" \
    "http://127.0.0.1:${MUX_PORT}/api/v1/sessions" 2>/dev/null
}

write_json_expr() {
  local expr="$1"
  local tmpfile
  tmpfile=$(mktemp)
  python3 -c "
import json
data = $expr
with open('$tmpfile', 'w') as f:
    json.dump(data, f)
" 2>/dev/null
  echo "$tmpfile"
}

# Helper: check if our test session exists in mux
check_session_in_mux() {
  local sessions
  sessions=$(mux_sessions 2>/dev/null) || return 1
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$sessions" > "$tmpf"
  python3 -c "
import json
with open('$tmpf') as f:
    sessions = json.load(f)
for s in sessions:
    if s.get('id') == '$TEST_POD_NAME':
        print('found')
        exit(0)
exit(1)
" 2>/dev/null
  local rc=$?
  rm -f "$tmpf"
  return $rc
}

# ── Cleanup trap ─────────────────────────────────────────────────────
_test_cleanup() {
  if [[ -n "$TEST_BEAD_ID" && -n "$DAEMON_PORT" && -n "$DAEMON_TOKEN" ]]; then
    log "Cleaning up test bead $TEST_BEAD_ID..."
    local bf
    bf=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
    daemon_api "Close" "$bf" >/dev/null 2>&1 || true
    rm -f "$bf"
    sleep 5
  fi
}
trap '_test_cleanup; _cleanup' EXIT

# ── Test 1: Daemon HTTP API is reachable ─────────────────────────────
test_daemon_api() {
  discover_daemon || return 1
  DAEMON_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || return 1
  local health
  health=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${DAEMON_PORT}/health" 2>/dev/null)
  assert_contains "$health" "status"
}
run_test "Daemon HTTP API is reachable" test_daemon_api

# ── Test 2: Coop mux is reachable ───────────────────────────────────
test_mux_reachable() {
  discover_mux || return 1
  MUX_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
  local resp
  resp=$(mux_sessions) || return 1
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$resp" > "$tmpf"
  python3 -c "import json; json.load(open('$tmpf'))" 2>/dev/null
  local rc=$?
  rm -f "$tmpf"
  return $rc
}
run_test "Coop mux API is reachable" test_mux_reachable

if [[ -z "$DAEMON_PORT" || -z "$MUX_PORT" ]]; then
  skip_test "Create test agent bead" "daemon or mux not reachable"
  skip_test "Agent session appears in mux" "daemon or mux not reachable"
  skip_test "Restart coop broker pod" "daemon or mux not reachable"
  skip_test "Mux session restored after broker restart" "daemon or mux not reachable"
  skip_test "Close bead and cleanup" "daemon or mux not reachable"
  print_summary
  exit 0
fi

# ── Test 3: Create test agent bead and wait for mux session ─────────
test_create_and_wait_for_mux() {
  PRE_EXISTING_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}' | sort)

  local bodyfile
  bodyfile=$(write_json_expr "{
    'title': '$TEST_BEAD_TITLE',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E mux restart persistence test',
    'labels': ['gt:agent', 'execution_target:k8s', 'rig:e2e-test', 'role:test', 'agent:muxpersist']
  }")

  local resp
  resp=$(daemon_api "Create" "$bodyfile")
  rm -f "$bodyfile"
  [[ -n "$resp" ]] || return 1

  local respfile
  respfile=$(mktemp)
  printf '%s' "$resp" > "$respfile"
  TEST_BEAD_ID=$(python3 -c "
import json
try:
    with open('$respfile') as f:
        d = json.load(f)
    print(d.get('id', d.get('issue_id', d.get('data', {}).get('id', ''))))
except:
    pass
" 2>/dev/null)
  rm -f "$respfile"
  log "Created test bead: $TEST_BEAD_ID"
  [[ -n "$TEST_BEAD_ID" ]] || return 1

  # Set to in_progress
  _update_bf=$(write_json_expr "{'id': '$TEST_BEAD_ID', 'status': 'in_progress'}")
  daemon_api "Update" "$_update_bf" >/dev/null 2>&1 || true
  rm -f "$_update_bf"

  # Wait for pod creation
  log "Waiting for pod (timeout: ${POD_CREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_CREATE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    TEST_POD_NAME=$(kube get pods -l "gastown.io/bead-id=$TEST_BEAD_ID" --no-headers 2>/dev/null | head -1 | awk '{print $1}')
    if [[ -z "$TEST_POD_NAME" ]]; then
      local current_pods
      current_pods=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}' | sort)
      TEST_POD_NAME=$(comm -13 <(echo "$PRE_EXISTING_PODS") <(echo "$current_pods") | head -1)
    fi
    [[ -n "$TEST_POD_NAME" ]] && break
    sleep 3
  done
  [[ -n "$TEST_POD_NAME" ]] || { log "No pod created"; return 1; }
  log "Found pod: $TEST_POD_NAME"

  # Wait for pod ready
  log "Waiting for pod ready (timeout: ${POD_READY_TIMEOUT}s)..."
  deadline=$((SECONDS + POD_READY_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      local ready
      ready=$(kube get pod "$TEST_POD_NAME" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$ready" =~ ^([0-9]+)/\1$ ]]; then
        log "Pod ready: $ready"
        break
      fi
    fi
    sleep 5
  done

  # Wait for mux session
  log "Waiting for mux session (timeout: ${MUX_SESSION_TIMEOUT}s)..."
  deadline=$((SECONDS + MUX_SESSION_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    if [[ "$(check_session_in_mux 2>/dev/null)" == "found" ]]; then
      log "Session visible in mux"
      return 0
    fi
    sleep 5
  done

  log "Session did not appear in mux"
  return 1
}
run_test "Create agent bead and verify mux session" test_create_and_wait_for_mux

if [[ -z "$TEST_POD_NAME" ]]; then
  skip_test "Restart coop broker pod" "pod not created"
  skip_test "Mux session restored after broker restart" "pod not created"
  skip_test "Close bead and cleanup" "pod not created"
  print_summary
  exit 0
fi

# ── Test 4: Restart coop broker pod ──────────────────────────────────
BROKER_POD_BEFORE=""
test_restart_broker() {
  BROKER_POD_BEFORE=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$BROKER_POD_BEFORE" ]] || return 1

  log "Deleting broker pod $BROKER_POD_BEFORE..."
  kube delete pod "$BROKER_POD_BEFORE" --wait=false 2>/dev/null || return 1

  # Stop existing mux port-forward (will reconnect below)
  stop_port_forwards
  MUX_PORT=""

  # Wait for new broker pod to be Running
  log "Waiting for new broker pod (timeout: ${BROKER_RESTART_TIMEOUT}s)..."
  local deadline=$((SECONDS + BROKER_RESTART_TIMEOUT))
  local new_broker=""
  while [[ $SECONDS -lt $deadline ]]; do
    new_broker=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
    if [[ -n "$new_broker" && "$new_broker" != "$BROKER_POD_BEFORE" ]]; then
      # Check all containers are ready
      local ready
      ready=$(kube get pod "$new_broker" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$ready" =~ ^([0-9]+)/\1$ ]]; then
        log "New broker pod: $new_broker ($ready)"
        break
      fi
    fi
    sleep 3
  done

  [[ -n "$new_broker" && "$new_broker" != "$BROKER_POD_BEFORE" ]] || { log "Broker did not restart"; return 1; }

  # Re-establish port-forwards (daemon + mux)
  DAEMON_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || return 1
  MUX_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1

  # Verify mux is responsive
  local resp
  resp=$(mux_sessions 2>/dev/null) || return 1
  log "Mux responsive after restart"
}
run_test "Restart coop broker pod" test_restart_broker

if [[ -z "$MUX_PORT" ]]; then
  skip_test "Mux session restored after broker restart" "mux not reachable after restart"
  skip_test "Close bead and cleanup" "mux not reachable after restart"
  print_summary
  exit 0
fi

# ── Test 5: Mux session restored after broker restart ────────────────
test_session_restored() {
  log "Waiting for mux session to be restored (timeout: ${MUX_SESSION_RESTORE_TIMEOUT}s)..."
  local deadline=$((SECONDS + MUX_SESSION_RESTORE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    if [[ "$(check_session_in_mux 2>/dev/null)" == "found" ]]; then
      log "Session $TEST_POD_NAME restored in mux after broker restart"
      return 0
    fi
    sleep 5
  done

  local sessions
  sessions=$(mux_sessions 2>/dev/null) || true
  log "Session NOT restored. Current mux sessions: ${sessions:0:200}"
  log "NOTE: This is a known issue (bd-zpcb7). Coop agents do not re-register after broker restart."
  return 1
}
run_test "Mux session restored after broker restart" test_session_restored

# ── Test 6: Close bead and cleanup ───────────────────────────────────
test_close_and_cleanup() {
  local bodyfile
  bodyfile=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
  daemon_api "Close" "$bodyfile" >/dev/null 2>&1
  rm -f "$bodyfile"
  local closed_id="$TEST_BEAD_ID"
  TEST_BEAD_ID=""

  log "Bead $closed_id closed. Waiting for pod deletion (timeout: ${POD_DELETE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local exists
    exists=$(kube get pod "$TEST_POD_NAME" --no-headers 2>/dev/null | awk '{print $1}')
    if [[ -z "$exists" ]]; then
      log "Pod $TEST_POD_NAME deleted"
      return 0
    fi
    sleep 3
  done

  log "Pod $TEST_POD_NAME still exists after ${POD_DELETE_TIMEOUT}s"
  return 1
}
run_test "Close bead and verify pod cleanup" test_close_and_cleanup

# ── Summary ──────────────────────────────────────────────────────────
print_summary
