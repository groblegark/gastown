#!/usr/bin/env bash
# test-mux-visibility.sh — Verify agent pods are visible in the coop mux.
#
# Tests the full bead→pod→mux-session lifecycle:
#   1. Daemon HTTP API is reachable
#   2. Coop mux is reachable
#   3. Create a test agent bead
#   4. Controller creates agent pod
#   5. Agent pod reaches Running state
#   6. Agent session appears in mux session list
#   7. Mux session has correct metadata (agent name, role)
#   8. Close bead and verify mux session is removed
#
# IMPORTANT: This test creates and deletes a real agent pod. It requires:
#   - Daemon HTTP API accessible (port 9080)
#   - BD_DAEMON_TOKEN for authentication
#   - Controller running and watching beads
#   - Coop broker with mux enabled (port 9800)
#   - COOP_MUX_AUTH_TOKEN for mux API authentication
#   - Sufficient cluster resources for a new pod
#
# Usage:
#   ./scripts/test-mux-visibility.sh [NAMESPACE]

MODULE_NAME="mux-visibility"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing mux visibility for agent pods in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
MUX_SESSION_TIMEOUT=120  # seconds to wait for session to appear in mux
MUX_SESSION_REMOVE_TIMEOUT=180  # seconds to wait for session removal after bead close
POD_DELETE_TIMEOUT=180

TEST_BEAD_TITLE="e2e-mux-vis-$(date +%s)"
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

discover_mux() {
  local broker_pod
  broker_pod=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$broker_pod" ]] || return 1

  MUX_TOKEN=$(kube exec "$broker_pod" -c coop-mux -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null) || true
  [[ -n "$MUX_TOKEN" ]]
}

# Helper: call daemon HTTP API
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

# Helper: query mux sessions
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
  # Port-forward to coop-broker mux port (9800)
  local broker_svc
  broker_svc=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  [[ -n "$broker_svc" ]] || return 1
  MUX_PORT=$(start_port_forward "svc/$broker_svc" 9800) || return 1
  local resp
  resp=$(mux_sessions) || return 1
  # Should return valid JSON (possibly empty array)
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$resp" > "$tmpf"
  python3 -c "import json; json.load(open('$tmpf'))" 2>/dev/null
  local rc=$?
  rm -f "$tmpf"
  return $rc
}
run_test "Coop mux API is reachable" test_mux_reachable

# Bail early if infrastructure isn't reachable
if [[ -z "$DAEMON_PORT" || -z "$MUX_PORT" ]]; then
  skip_test "Create test agent bead" "daemon or mux not reachable"
  skip_test "Controller creates agent pod" "daemon or mux not reachable"
  skip_test "Agent pod reaches Running state" "daemon or mux not reachable"
  skip_test "Agent session appears in mux" "daemon or mux not reachable"
  skip_test "Mux session has correct metadata" "daemon or mux not reachable"
  skip_test "Close bead removes mux session" "daemon or mux not reachable"
  print_summary
  exit 0
fi

# ── Test 3: Create test agent bead ──────────────────────────────────
test_create_bead() {
  PRE_EXISTING_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}' | sort)

  local bodyfile
  bodyfile=$(write_json_expr "{
    'title': '$TEST_BEAD_TITLE',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E mux visibility test',
    'labels': ['gt:agent', 'execution_target:k8s', 'rig:e2e-test', 'role:test', 'agent:muxvis']
  }")

  local resp
  resp=$(daemon_api "Create" "$bodyfile")
  rm -f "$bodyfile"
  log "Create response: ${resp:0:200}"
  [[ -n "$resp" ]] || return 1

  local respfile
  respfile=$(mktemp)
  printf '%s' "$resp" > "$respfile"
  TEST_BEAD_ID=$(python3 -c "
import json
try:
    with open('$respfile') as f:
        d = json.load(f)
    bid = d.get('id', d.get('issue_id', d.get('data', {}).get('id', '')))
    print(bid)
except:
    pass
" 2>/dev/null)
  rm -f "$respfile"

  log "Created test bead: $TEST_BEAD_ID"
  [[ -n "$TEST_BEAD_ID" ]]
}
run_test "Create test agent bead with mux labels" test_create_bead

if [[ -z "$TEST_BEAD_ID" ]]; then
  skip_test "Controller creates agent pod" "bead creation failed"
  skip_test "Agent pod reaches Running state" "bead creation failed"
  skip_test "Agent session appears in mux" "bead creation failed"
  skip_test "Mux session has correct metadata" "bead creation failed"
  skip_test "Close bead removes mux session" "bead creation failed"
  print_summary
  exit 0
fi

# Set bead to in_progress
_update_bf=$(write_json_expr "{'id': '$TEST_BEAD_ID', 'status': 'in_progress'}")
daemon_api "Update" "$_update_bf" >/dev/null 2>&1 || true
rm -f "$_update_bf"

# ── Test 4: Controller creates agent pod ─────────────────────────────
test_controller_creates_pod() {
  log "Waiting for controller to create pod (timeout: ${POD_CREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_CREATE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    TEST_POD_NAME=$(kube get pods -l "gastown.io/bead-id=$TEST_BEAD_ID" --no-headers 2>/dev/null | head -1 | awk '{print $1}')

    if [[ -z "$TEST_POD_NAME" ]]; then
      local current_pods
      current_pods=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}' | sort)
      TEST_POD_NAME=$(comm -13 <(echo "$PRE_EXISTING_PODS") <(echo "$current_pods") | head -1)
    fi

    if [[ -n "$TEST_POD_NAME" ]]; then
      log "Found pod: $TEST_POD_NAME"
      return 0
    fi

    sleep 3
  done

  log "No agent pod appeared within ${POD_CREATE_TIMEOUT}s"
  return 1
}
run_test "Controller creates agent pod" test_controller_creates_pod

if [[ -z "$TEST_POD_NAME" ]]; then
  skip_test "Agent pod reaches Running state" "pod not created"
  skip_test "Agent session appears in mux" "pod not created"
  skip_test "Mux session has correct metadata" "pod not created"
  skip_test "Close bead removes mux session" "pod not created"
  print_summary
  exit 0
fi

# ── Test 5: Agent pod reaches Running state ──────────────────────────
test_pod_running() {
  log "Waiting for pod $TEST_POD_NAME to become Ready (timeout: ${POD_READY_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_READY_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      local ready
      ready=$(kube get pod "$TEST_POD_NAME" --no-headers 2>/dev/null | awk '{print $2}')
      # Accept any ready state (1/1 or 2/2 depending on sidecars)
      if [[ "$ready" =~ ^([0-9]+)/\1$ ]]; then
        log "Pod $TEST_POD_NAME is Running and Ready ($ready)"
        return 0
      fi
    fi
    sleep 5
  done

  local phase
  phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
  log "Pod $TEST_POD_NAME phase: $phase (not ready within timeout)"
  return 1
}
run_test "Agent pod reaches Running state" test_pod_running

# ── Test 6: Agent session appears in mux ─────────────────────────────
test_mux_session_appears() {
  log "Waiting for agent session to appear in mux (timeout: ${MUX_SESSION_TIMEOUT}s)..."
  local deadline=$((SECONDS + MUX_SESSION_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local sessions
    sessions=$(mux_sessions 2>/dev/null) || { sleep 5; continue; }

    # Check if our pod name appears in the sessions list
    local tmpf found
    tmpf=$(mktemp)
    printf '%s' "$sessions" > "$tmpf"
    found=$(python3 -c "
import json
try:
    with open('$tmpf') as f:
        sessions = json.load(f)
    for s in sessions:
        if s.get('id') == '$TEST_POD_NAME':
            print('found')
            break
except:
    pass
" 2>/dev/null)
    rm -f "$tmpf"

    if [[ "$found" == "found" ]]; then
      log "Agent session $TEST_POD_NAME visible in mux"
      return 0
    fi

    sleep 5
  done

  log "Agent session did not appear in mux within ${MUX_SESSION_TIMEOUT}s"
  local sessions
  sessions=$(mux_sessions 2>/dev/null) || true
  log "Current mux sessions: ${sessions:0:200}"
  return 1
}
run_test "Agent session appears in mux session list" test_mux_session_appears

# ── Test 7: Mux session has correct metadata ─────────────────────────
test_mux_session_metadata() {
  local sessions
  sessions=$(mux_sessions 2>/dev/null) || return 1

  local tmpf meta
  tmpf=$(mktemp)
  printf '%s' "$sessions" > "$tmpf"
  meta=$(python3 -c "
import json
try:
    with open('$tmpf') as f:
        sessions = json.load(f)
    for s in sessions:
        if s.get('id') == '$TEST_POD_NAME':
            m = s.get('metadata', {})
            agent = m.get('agent', '')
            role = m.get('role', '')
            url = s.get('url', '')
            # Check agent name and role are present
            if agent == 'muxvis' and role == 'test' and url:
                print('ok')
            else:
                print(f'mismatch: agent={agent} role={role} url={url}')
            break
    else:
        print('not_found')
except Exception as e:
    print(f'error: {e}')
" 2>/dev/null)
  rm -f "$tmpf"

  if [[ "$meta" == "ok" ]]; then
    return 0
  fi
  log "Metadata check: $meta"
  return 1
}
run_test "Mux session has correct metadata (agent=muxvis, role=test)" test_mux_session_metadata

# ── Test 8: Close bead removes mux session ───────────────────────────
test_close_removes_session() {
  # Close the bead
  local bodyfile
  bodyfile=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
  daemon_api "Close" "$bodyfile" >/dev/null 2>&1
  rm -f "$bodyfile"
  local closed_id="$TEST_BEAD_ID"
  TEST_BEAD_ID=""

  log "Bead $closed_id closed. Waiting for mux session removal (timeout: ${MUX_SESSION_REMOVE_TIMEOUT}s)..."
  local deadline=$((SECONDS + MUX_SESSION_REMOVE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local sessions
    sessions=$(mux_sessions 2>/dev/null) || { sleep 5; continue; }

    local tmpf found
    tmpf=$(mktemp)
    printf '%s' "$sessions" > "$tmpf"
    found=$(python3 -c "
import json
try:
    with open('$tmpf') as f:
        sessions = json.load(f)
    for s in sessions:
        if s.get('id') == '$TEST_POD_NAME':
            print('found')
            break
    else:
        print('gone')
except:
    print('gone')
" 2>/dev/null)
    rm -f "$tmpf"

    if [[ "$found" == "gone" ]]; then
      log "Mux session removed after bead close"
      return 0
    fi

    sleep 5
  done

  log "Mux session $TEST_POD_NAME still present after ${MUX_SESSION_REMOVE_TIMEOUT}s"
  return 1
}
run_test "Closing bead removes agent session from mux" test_close_removes_session

# ── Summary ──────────────────────────────────────────────────────────
print_summary
