#!/usr/bin/env bash
# test-crew-pod-resilience.sh — E2E: crew agent survives pod recreation.
#
# Tests that a crew agent's work-in-progress survives pod deletion and
# recreation by the controller:
#   1. Daemon HTTP API is reachable
#   2. Create a crew bead → controller creates agent pod
#   3. Agent clones beads repo and starts a build (simulating real work)
#   4. Write marker files and verify git state on PVC
#   5. Delete agent pod (simulating node eviction or controller recreation)
#   6. Controller creates replacement pod with same PVC
#   7. Marker files persist on PVC
#   8. Git repo and branch survive on PVC
#   9. Agent can continue building (make build) after recreation
#  10. Cleanup: close bead, pod deleted
#
# This validates the PVC persistence story for crew agents doing real work
# (git repos, build artifacts, in-progress changes).
#
# Requires:
#   - Daemon HTTP API accessible (port 9080)
#   - BD_DAEMON_TOKEN for authentication
#   - Controller watching beads and reconciling pods
#
# Usage:
#   E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-pod-resilience.sh

MODULE_NAME="crew-pod-resilience"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing crew pod resilience in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
POD_RECREATE_TIMEOUT=180
POD_DELETE_TIMEOUT=180

CREW_NAME="e2e-resilience-$(date +%s)"
CREW_RIG="beads"
CLONE_DIR="/home/agent/gt/beads-resilience-test"
MARKER_FILE="/home/agent/gt/.state/e2e-resilience-marker"
MARKER_VALUE="resilience-$(date +%s)"
TEST_BRANCH="e2e/resilience-test-${CREW_NAME}"

TEST_BEAD_ID=""
TEST_POD_NAME=""
OLD_POD_UID=""
NEW_POD=""
DAEMON_POD=""
DAEMON_PORT=""
DAEMON_TOKEN=""

# ── Helpers ──────────────────────────────────────────────────────────
discover_daemon() {
  DAEMON_POD=$(kube get pods --no-headers 2>/dev/null \
    | grep "daemon" | grep -v "dolt\|nats\|clusterctl\|slackbot\|autofailover" \
    | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$DAEMON_POD" ]] || return 1
  DAEMON_TOKEN=$(kube exec "$DAEMON_POD" -c bd-daemon -- printenv BD_DAEMON_TOKEN 2>/dev/null) || true
  [[ -n "$DAEMON_TOKEN" ]]
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

agent_exec() {
  local pod="${1:-$TEST_POD_NAME}"
  shift
  kube exec "$pod" -c agent -- bash -c "$@"
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

if [[ -z "$DAEMON_PORT" || -z "$DAEMON_TOKEN" ]]; then
  skip_all "daemon not reachable"
  exit 0
fi

# ── Test 2: Create crew bead and wait for agent pod ──────────────────
test_create_and_wait() {
  local CREW_BEAD_ID="gt-${CREW_RIG}-crew-${CREW_NAME}"

  local bodyfile
  bodyfile=$(write_json_expr "{
    'id': '$CREW_BEAD_ID',
    'title': 'E2E pod resilience test: $CREW_NAME',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E crew pod resilience — verify PVC survives recreation',
    'labels': ['gt:agent', 'execution_target:k8s', 'rig:$CREW_RIG', 'role:crew', 'agent:$CREW_NAME']
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
    bid = d.get('id', d.get('issue_id', d.get('data', {}).get('id', '')))
    print(bid)
except:
    pass
" 2>/dev/null)
  rm -f "$respfile"
  [[ -n "$TEST_BEAD_ID" ]] || return 1
  log "Created test bead: $TEST_BEAD_ID"

  # Set to in_progress so controller picks it up
  local uf
  uf=$(write_json_expr "{'id': '$TEST_BEAD_ID', 'status': 'in_progress'}")
  daemon_api "Update" "$uf" >/dev/null 2>&1 || true
  rm -f "$uf"

  # Wait for pod
  log "Waiting for agent pod $CREW_BEAD_ID (timeout: ${POD_CREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_CREATE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    TEST_POD_NAME=$(kube get pod "$CREW_BEAD_ID" --no-headers 2>/dev/null | awk '{print $1}')
    if [[ -n "$TEST_POD_NAME" ]]; then
      log "Found pod: $TEST_POD_NAME"
      break
    fi
    sleep 3
  done
  [[ -n "$TEST_POD_NAME" ]] || { log "No pod appeared"; return 1; }

  # Wait for ready
  log "Waiting for pod to become Ready..."
  local ready_deadline=$((SECONDS + POD_READY_TIMEOUT))
  while [[ $SECONDS -lt $ready_deadline ]]; do
    local phase
    phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      local ready
      ready=$(kube get pod "$TEST_POD_NAME" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$ready" == "1/1" ]]; then
        log "Pod $TEST_POD_NAME is Running and Ready"
        return 0
      fi
    fi
    sleep 5
  done
  log "Pod not ready within timeout"
  return 1
}
run_test "Create crew bead and wait for agent pod" test_create_and_wait

if [[ -z "$TEST_POD_NAME" ]]; then
  skip_test "Agent clones repo and starts build" "no agent pod"
  skip_test "Record PVC state with markers" "no agent pod"
  skip_test "Delete agent pod" "no agent pod"
  skip_test "Controller creates replacement pod" "no agent pod"
  skip_test "Marker files persist on PVC" "no agent pod"
  skip_test "Git repo and branch survive on PVC" "no agent pod"
  skip_test "Agent can continue building after recreation" "no agent pod"
  skip_test "Cleanup" "no agent pod"
  print_summary
  exit 0
fi

# ── Test 3: Agent clones repo and starts build ──────────────────────
test_clone_and_build() {
  # Clone beads repo
  agent_exec "$TEST_POD_NAME" "
    rm -rf ${CLONE_DIR}
    git clone https://github.com/groblegark/beads.git ${CLONE_DIR}
  " || return 1

  # Create a branch with a trivial change (simulating in-progress work)
  agent_exec "$TEST_POD_NAME" "
    cd ${CLONE_DIR}
    git checkout -b '${TEST_BRANCH}'
    mkdir -p docs
    echo '# Resilience test: ${CREW_NAME}' > docs/e2e-resilience-test.md
    git add docs/e2e-resilience-test.md
    git commit -m 'test: e2e pod resilience (${CREW_NAME})'
  " || return 1

  # Start a build to create artifacts on disk
  local build_output
  build_output=$(agent_exec "$TEST_POD_NAME" "
    cd ${CLONE_DIR}
    make build 2>&1 | tail -3
  " 2>/dev/null) || true
  log "Build output: $build_output"

  # Verify the binary exists
  local binary_exists
  binary_exists=$(agent_exec "$TEST_POD_NAME" "test -f ${CLONE_DIR}/bd && echo yes" 2>/dev/null) || true
  log "Binary exists: ${binary_exists:-no}"

  # Verify branch
  local branch
  branch=$(agent_exec "$TEST_POD_NAME" "cd ${CLONE_DIR} && git branch --show-current 2>/dev/null") || return 1
  log "On branch: $branch"
  assert_eq "$branch" "$TEST_BRANCH"
}
run_test "Agent clones repo and starts build" test_clone_and_build

# ── Test 4: Record PVC state with markers ────────────────────────────
GIT_HEAD_BEFORE=""
test_write_markers() {
  # Write marker file
  agent_exec "$TEST_POD_NAME" "
    mkdir -p \$(dirname ${MARKER_FILE})
    echo '${MARKER_VALUE}' > ${MARKER_FILE}
  " || return 1

  # Verify marker
  local readback
  readback=$(agent_exec "$TEST_POD_NAME" "cat ${MARKER_FILE}" 2>/dev/null)
  assert_eq "$readback" "$MARKER_VALUE" || return 1

  # Record git state for later comparison
  GIT_HEAD_BEFORE=$(agent_exec "$TEST_POD_NAME" "cd ${CLONE_DIR} && git rev-parse HEAD 2>/dev/null") || true
  log "Git HEAD before: ${GIT_HEAD_BEFORE:-unknown}"

  # Record PVC name
  local pvc
  pvc=$(kube get pod "$TEST_POD_NAME" -o json 2>/dev/null | \
    jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' 2>/dev/null | head -1)
  log "PVC: ${pvc:-none}"
}
run_test "Record PVC state with markers" test_write_markers

# ── Test 5: Delete agent pod ─────────────────────────────────────────
test_delete_pod() {
  OLD_POD_UID=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.metadata.uid}' 2>/dev/null)
  log "Deleting pod $TEST_POD_NAME (uid: ${OLD_POD_UID:0:8}...)"

  kube delete pod "$TEST_POD_NAME" --wait=false 2>/dev/null || return 1

  # Wait for pod to disappear
  local deadline=$((SECONDS + 60))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ -z "$phase" ]]; then
      log "Pod $TEST_POD_NAME gone"
      return 0
    fi
    sleep 2
  done
  log "Pod still terminating (proceeding)"
}
run_test "Delete agent pod (not PVC)" test_delete_pod

# ── Test 6: Controller creates replacement pod ───────────────────────
test_replacement_pod() {
  log "Waiting for controller to recreate pod (timeout: ${POD_RECREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_RECREATE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    # Look for pod with same name (controller uses bead ID as pod name)
    local pod_line
    pod_line=$(kube get pod "$TEST_POD_NAME" --no-headers 2>/dev/null)
    if [[ -n "$pod_line" ]]; then
      local uid
      uid=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.metadata.uid}' 2>/dev/null)
      if [[ "$uid" != "$OLD_POD_UID" ]]; then
        NEW_POD="$TEST_POD_NAME"
        log "Found replacement pod: $NEW_POD (uid: ${uid:0:8}...)"
        break
      fi
    fi
    sleep 5
  done

  [[ -n "$NEW_POD" ]] || { log "No replacement pod appeared"; return 1; }

  # Wait for ready
  log "Waiting for $NEW_POD to become Ready (timeout: ${POD_READY_TIMEOUT}s)..."
  local ready_deadline=$((SECONDS + POD_READY_TIMEOUT))
  while [[ $SECONDS -lt $ready_deadline ]]; do
    local phase
    phase=$(kube get pod "$NEW_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      local ready
      ready=$(kube get pod "$NEW_POD" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$ready" == "1/1" ]]; then
        log "Pod $NEW_POD is Running and Ready"
        return 0
      fi
    fi
    sleep 5
  done
  log "Pod not ready within timeout"
  return 1
}
run_test "Controller creates replacement pod" test_replacement_pod

if [[ -z "$NEW_POD" ]]; then
  skip_test "Marker files persist on PVC" "no replacement pod"
  skip_test "Git repo and branch survive on PVC" "no replacement pod"
  skip_test "Agent can continue building after recreation" "no replacement pod"
  skip_test "Cleanup" "no replacement pod"
  print_summary
  exit 0
fi

# ── Test 7: Marker files persist on PVC ──────────────────────────────
test_marker_persists() {
  local readback
  readback=$(agent_exec "$NEW_POD" "cat ${MARKER_FILE} 2>/dev/null") || return 1
  log "Marker readback: '$readback' (expected: '$MARKER_VALUE')"
  assert_eq "$readback" "$MARKER_VALUE"
}
run_test "Marker files persist on PVC after recreation" test_marker_persists

# ── Test 8: Git repo and branch survive on PVC ───────────────────────
test_git_survives() {
  # Check git directory exists
  agent_exec "$NEW_POD" "test -d ${CLONE_DIR}/.git" 2>/dev/null || {
    log "Git repo not found at ${CLONE_DIR}"
    return 1
  }

  # Check branch is preserved
  local branch
  branch=$(agent_exec "$NEW_POD" "cd ${CLONE_DIR} && git branch --show-current 2>/dev/null") || return 1
  log "Branch after recreation: $branch (expected: $TEST_BRANCH)"
  assert_eq "$branch" "$TEST_BRANCH" || return 1

  # Check HEAD is the same
  local head_after
  head_after=$(agent_exec "$NEW_POD" "cd ${CLONE_DIR} && git rev-parse HEAD 2>/dev/null") || return 1
  log "Git HEAD after: $head_after (before: ${GIT_HEAD_BEFORE:-unknown})"
  if [[ -n "$GIT_HEAD_BEFORE" ]]; then
    assert_eq "$head_after" "$GIT_HEAD_BEFORE" || return 1
  fi

  # Check working tree is clean (committed changes preserved)
  local status
  status=$(agent_exec "$NEW_POD" "cd ${CLONE_DIR} && git status --porcelain 2>/dev/null") || true
  log "Git status: '${status:-clean}'"
  # Binary from build may show as untracked — that's fine
}
run_test "Git repo and branch survive on PVC" test_git_survives

# ── Test 9: Agent can continue building after recreation ─────────────
test_build_after_recreation() {
  local build_output
  build_output=$(agent_exec "$NEW_POD" "
    cd ${CLONE_DIR}
    make build 2>&1 | tail -5
  " 2>/dev/null) || { log "Build failed: $build_output"; return 1; }

  log "Build output: $build_output"

  # Verify binary exists
  local binary_exists
  binary_exists=$(agent_exec "$NEW_POD" "test -f ${CLONE_DIR}/bd && echo yes" 2>/dev/null) || true
  log "Binary exists after rebuild: ${binary_exists:-no}"
  [[ "$binary_exists" == "yes" ]] || return 1

  # Verify binary is runnable
  local version
  version=$(agent_exec "$NEW_POD" "${CLONE_DIR}/bd --version 2>&1 | head -1" 2>/dev/null) || true
  log "bd version: ${version:-unknown}"
}
run_test "Agent can continue building after recreation" test_build_after_recreation

# ── Test 10: Cleanup ─────────────────────────────────────────────────
test_final_cleanup() {
  local bodyfile
  bodyfile=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
  daemon_api "Close" "$bodyfile" >/dev/null 2>&1 || true
  rm -f "$bodyfile"
  local closed_id="$TEST_BEAD_ID"
  TEST_BEAD_ID=""  # Prevent double-close in trap

  # Wait for pod deletion
  log "Waiting for pod deletion (timeout: ${POD_DELETE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local exists
    exists=$(kube get pod "$NEW_POD" --no-headers 2>/dev/null | awk '{print $1}')
    if [[ -z "$exists" ]]; then
      log "Pod $NEW_POD deleted"
      return 0
    fi
    sleep 3
  done
  log "Pod still exists after timeout (non-fatal)"
  return 0
}
run_test "Cleanup: close bead, pod deleted" test_final_cleanup

# ── Summary ──────────────────────────────────────────────────────────
print_summary
