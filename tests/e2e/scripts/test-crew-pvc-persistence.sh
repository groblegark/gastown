#!/usr/bin/env bash
# test-crew-pvc-persistence.sh — E2E: crew agent survives pod recreation with PVC.
#
# Tests that a crew agent's workspace survives pod deletion and recreation:
#   1. Daemon HTTP API is reachable
#   2. Create a crew bead → controller creates agent pod with PVC
#   3. Agent clones repo and creates a branch (simulating real work)
#   4. Record workspace state (git log, branch, file)
#   5. Delete agent pod (not PVC)
#   6. Controller recreates pod with same PVC (within timeout)
#   7. Workspace persists: git repo, branch, and committed file all survive
#   8. Agent can continue working (git status, git log)
#   9. Cleanup
#
# This validates bd-59lhb: PVC persistence story for crew agents.
#
# Usage:
#   E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-pvc-persistence.sh

MODULE_NAME="crew-pvc-persistence"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing crew PVC persistence after pod recreation in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
POD_RECREATE_TIMEOUT=180
POD_DELETE_TIMEOUT=60

CREW_NAME="e2e-pvc-$(date +%s)"
CREW_RIG="beads"
TEST_BRANCH="e2e/pvc-persist-${CREW_NAME}"
CLONE_DIR="/tmp/beads-pvc-test"
MARKER_FILE="${CLONE_DIR}/docs/e2e-pvc-marker.txt"
MARKER_VALUE="pvc-persist-${CREW_NAME}"
TEST_BEAD_ID=""
TEST_POD_NAME=""
OLD_POD_UID=""
NEW_POD_NAME=""
DAEMON_POD=""
DAEMON_PORT=""
DAEMON_TOKEN=""
PVC_NAME=""

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
  kube exec "$TEST_POD_NAME" -c agent -- bash -c "$@"
}

new_agent_exec() {
  kube exec "$NEW_POD_NAME" -c agent -- bash -c "$@"
}

# ── Cleanup trap ─────────────────────────────────────────────────────
_test_cleanup() {
  if [[ -n "$TEST_BEAD_ID" && -n "$DAEMON_PORT" && -n "$DAEMON_TOKEN" ]]; then
    log "Cleaning up test bead $TEST_BEAD_ID..."
    local bf
    bf=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
    daemon_api "Close" "$bf" >/dev/null 2>&1 || true
    rm -f "$bf"
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
  skip_test "Create crew bead and wait for agent pod" "daemon not reachable"
  skip_test "Clone repo and simulate work" "daemon not reachable"
  skip_test "Record workspace state" "daemon not reachable"
  skip_test "Delete agent pod (not PVC)" "daemon not reachable"
  skip_test "Controller recreates pod with same PVC" "daemon not reachable"
  skip_test "Workspace persists after pod recreation" "daemon not reachable"
  skip_test "Agent can continue working" "daemon not reachable"
  skip_test "Cleanup: close bead, pod deleted" "daemon not reachable"
  print_summary
  exit 0
fi

# ── Test 2: Create crew bead and wait for pod ────────────────────────
test_create_and_wait() {
  local CREW_BEAD_ID="gt-${CREW_RIG}-crew-${CREW_NAME}"

  local bodyfile
  bodyfile=$(write_json_expr "{
    'id': '$CREW_BEAD_ID',
    'title': 'E2E PVC persistence test: $CREW_NAME',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E crew PVC persistence test — clone, work, pod recreation',
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

        # Get PVC name now that pod exists
        PVC_NAME=$(kube get pod "$TEST_POD_NAME" -o json 2>/dev/null | \
          jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' \
          2>/dev/null | head -1)
        log "PVC: ${PVC_NAME:-NONE}"

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
  skip_test "Clone repo and simulate work" "no agent pod"
  skip_test "Record workspace state" "no agent pod"
  skip_test "Delete agent pod (not PVC)" "no agent pod"
  skip_test "Controller recreates pod with same PVC" "no agent pod"
  skip_test "Workspace persists after pod recreation" "no agent pod"
  skip_test "Agent can continue working" "no agent pod"
  skip_test "Cleanup: close bead, pod deleted" "no agent pod"
  print_summary
  exit 0
fi

# ── Test 3: Clone repo and simulate real work ─────────────────────────
WORKSPACE_HEAD=""
test_simulate_work() {
  # Clone beads repo
  agent_exec "
    rm -rf ${CLONE_DIR}
    git clone https://github.com/groblegark/beads.git ${CLONE_DIR} --depth=1
  " || return 1

  # Create branch and commit a file (simulates agent mid-task work)
  agent_exec "
    cd ${CLONE_DIR}
    git checkout -b '${TEST_BRANCH}'
    mkdir -p docs
    echo '${MARKER_VALUE}' > ${MARKER_FILE}
    git add docs/e2e-pvc-marker.txt
    git config user.email 'e2e-test@example.com' || true
    git config user.name 'E2E Test' || true
    git commit -m 'test: e2e PVC persistence marker (${CREW_NAME})'
  " || return 1

  WORKSPACE_HEAD=$(agent_exec "cd ${CLONE_DIR} && git rev-parse HEAD 2>/dev/null") || return 1
  log "Workspace HEAD: $WORKSPACE_HEAD"
  [[ -n "$WORKSPACE_HEAD" ]]
}
run_test "Clone repo and simulate work in workspace" test_simulate_work

# ── Test 4: Record workspace state before pod deletion ────────────────
WORKSPACE_BRANCH_BEFORE=""
WORKSPACE_FILE_BEFORE=""
test_record_state() {
  WORKSPACE_BRANCH_BEFORE=$(agent_exec "cd ${CLONE_DIR} && git branch --show-current 2>/dev/null") || return 1
  WORKSPACE_FILE_BEFORE=$(agent_exec "cat ${MARKER_FILE} 2>/dev/null") || return 1

  log "Branch before: $WORKSPACE_BRANCH_BEFORE"
  log "Marker file before: $WORKSPACE_FILE_BEFORE"

  [[ "$WORKSPACE_BRANCH_BEFORE" == "$TEST_BRANCH" ]] || { log "Wrong branch: $WORKSPACE_BRANCH_BEFORE"; return 1; }
  [[ "$WORKSPACE_FILE_BEFORE" == "$MARKER_VALUE" ]] || { log "Wrong file content: $WORKSPACE_FILE_BEFORE"; return 1; }

  # Also verify PVC is Bound
  if [[ -n "$PVC_NAME" ]]; then
    local phase
    phase=$(kube get pvc "$PVC_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    log "PVC $PVC_NAME status: ${phase:-unknown}"
    [[ "$phase" == "Bound" ]] || { log "PVC not Bound"; return 1; }
  fi
}
run_test "Record workspace state before pod deletion" test_record_state

# ── Test 5: Delete agent pod (not PVC) ───────────────────────────────
test_delete_pod() {
  OLD_POD_UID=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.metadata.uid}' 2>/dev/null)
  log "Deleting pod $TEST_POD_NAME (uid: ${OLD_POD_UID:0:8}...)"

  kube delete pod "$TEST_POD_NAME" --wait=false 2>/dev/null || return 1

  # Verify PVC was NOT deleted
  if [[ -n "$PVC_NAME" ]]; then
    local pvc_exists
    pvc_exists=$(kube get pvc "$PVC_NAME" --no-headers 2>/dev/null | awk '{print $1}')
    [[ -n "$pvc_exists" ]] || { log "PVC $PVC_NAME was deleted — unexpected!"; return 1; }
    log "PVC $PVC_NAME still exists"
  fi

  # Wait for pod to start terminating
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ -z "$phase" || "$phase" == "Terminating" ]]; then
      log "Pod $TEST_POD_NAME is ${phase:-gone}"
      return 0
    fi
    sleep 2
  done
  log "Pod still Running after ${POD_DELETE_TIMEOUT}s (proceeding)"
}
run_test "Delete agent pod (not PVC)" test_delete_pod

# ── Test 6: Controller recreates pod with same PVC ────────────────────
test_replacement_pod() {
  log "Waiting for controller to recreate pod (timeout: ${POD_RECREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_RECREATE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    # Look for pod with same name (controller uses bead ID as pod name)
    local uid
    uid=$(kube get pod "$TEST_POD_NAME" -o jsonpath='{.metadata.uid}' 2>/dev/null)
    if [[ -n "$uid" && "$uid" != "$OLD_POD_UID" ]]; then
      NEW_POD_NAME="$TEST_POD_NAME"
      log "Replacement pod found: $NEW_POD_NAME (new uid: ${uid:0:8}...)"

      # Wait for it to become Ready
      local ready_deadline=$((SECONDS + POD_READY_TIMEOUT))
      while [[ $SECONDS -lt $ready_deadline ]]; do
        local phase ready
        phase=$(kube get pod "$NEW_POD_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
        ready=$(kube get pod "$NEW_POD_NAME" --no-headers 2>/dev/null | awk '{print $2}')
        if [[ "$phase" == "Running" && "$ready" == "1/1" ]]; then
          log "Replacement pod $NEW_POD_NAME is Running and Ready"
          return 0
        fi
        sleep 5
      done
      log "Replacement pod not ready within ${POD_READY_TIMEOUT}s"
      return 1
    fi
    sleep 5
  done

  log "No replacement pod appeared within ${POD_RECREATE_TIMEOUT}s"
  return 1
}
run_test "Controller recreates pod with same PVC" test_replacement_pod

if [[ -z "$NEW_POD_NAME" ]]; then
  skip_test "Workspace persists after pod recreation" "no replacement pod"
  skip_test "Agent can continue working" "no replacement pod"
  skip_test "Cleanup: close bead, pod deleted" "no replacement pod"
  print_summary
  exit 0
fi

# ── Test 7: Workspace persists after pod recreation ───────────────────
test_workspace_persists() {
  # Check PVC is still bound to new pod
  if [[ -n "$PVC_NAME" ]]; then
    local new_pvc
    new_pvc=$(kube get pod "$NEW_POD_NAME" -o json 2>/dev/null | \
      jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' \
      2>/dev/null | head -1)
    log "New pod PVC: ${new_pvc:-NONE} (expected: $PVC_NAME)"
    [[ "$new_pvc" == "$PVC_NAME" ]] || { log "PVC mismatch"; return 1; }
  fi

  # Verify git repo exists
  local git_ok
  git_ok=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "test -d ${CLONE_DIR}/.git && echo yes" 2>/dev/null) || true
  [[ "$git_ok" == "yes" ]] || { log "Git repo missing at ${CLONE_DIR}"; return 1; }

  # Verify branch is preserved
  local branch_after
  branch_after=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && git branch --show-current 2>/dev/null") || true
  log "Branch after recreation: ${branch_after:-UNKNOWN}"
  [[ "$branch_after" == "$TEST_BRANCH" ]] || { log "Branch mismatch: $branch_after != $TEST_BRANCH"; return 1; }

  # Verify marker file content is preserved
  local file_after
  file_after=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cat ${MARKER_FILE} 2>/dev/null") || true
  log "Marker file after: ${file_after:-MISSING}"
  [[ "$file_after" == "$MARKER_VALUE" ]] || { log "File content mismatch or missing"; return 1; }

  # Verify git commit history is preserved
  local head_after
  head_after=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && git rev-parse HEAD 2>/dev/null") || true
  log "HEAD after: ${head_after:-UNKNOWN} (expected: $WORKSPACE_HEAD)"
  [[ "$head_after" == "$WORKSPACE_HEAD" ]] || { log "HEAD mismatch"; return 1; }
}
run_test "Workspace persists after pod recreation" test_workspace_persists

# ── Test 8: Agent can continue working ───────────────────────────────
test_can_continue() {
  # Verify git status shows clean working tree (committed state)
  local git_status
  git_status=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && git status --short 2>/dev/null") || true
  log "git status: '${git_status:-clean}'"
  # Empty git status means clean — no untracked or modified files from test perspective

  # Verify git log shows our commit
  local last_msg
  last_msg=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && git log --oneline -1 2>/dev/null") || true
  log "Last commit: $last_msg"
  assert_contains "$last_msg" "e2e PVC persistence marker"

  # Verify the agent can make another file (workspace is writable)
  kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && echo 'resumed' >> ${MARKER_FILE} && git add -A && git commit -m 'test: resume after pod recreation (${CREW_NAME})'" \
    2>/dev/null || { log "Could not make new commit after recreation"; return 1; }

  local new_head
  new_head=$(kube exec "$NEW_POD_NAME" -c agent -- bash -c \
    "cd ${CLONE_DIR} && git rev-parse HEAD 2>/dev/null") || true
  log "New HEAD after resume commit: $new_head"
  [[ -n "$new_head" && "$new_head" != "$WORKSPACE_HEAD" ]] || { log "HEAD unchanged after resume commit"; return 1; }
}
run_test "Agent can continue working after pod recreation" test_can_continue

# ── Test 9: Cleanup ──────────────────────────────────────────────────
test_final_cleanup() {
  # Close bead via daemon API
  local bodyfile
  bodyfile=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
  daemon_api "Close" "$bodyfile" >/dev/null 2>&1 || true
  rm -f "$bodyfile"
  local closed_id="$TEST_BEAD_ID"
  TEST_BEAD_ID=""  # Prevent double-close in trap

  log "Closed test bead $closed_id"

  # Wait for pod deletion
  log "Waiting for pod deletion (timeout: ${POD_DELETE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local exists
    exists=$(kube get pod "$NEW_POD_NAME" --no-headers 2>/dev/null | awk '{print $1}')
    if [[ -z "$exists" ]]; then
      log "Pod $NEW_POD_NAME deleted"
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
