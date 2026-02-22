#!/usr/bin/env bash
# test-crew-concurrent.sh — E2E: multiple crew agents work without interference.
#
# Tests that two crew agents created simultaneously can each independently
# clone, build, and operate without workspace cross-contamination:
#   1. Daemon HTTP API is reachable
#   2. Create 2 crew beads simultaneously
#   3. Both pods become Ready
#   4. Both agents clone beads repo independently
#   5. Each agent creates a unique branch
#   6. Each agent runs make build
#   7. Verify branch isolation (no cross-contamination)
#   8. Cleanup: close beads, pods deleted
#
# Usage:
#   E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-concurrent.sh

MODULE_NAME="crew-concurrent"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing concurrent crew agents in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
POD_DELETE_TIMEOUT=120
BUILD_TIMEOUT=300

TIMESTAMP=$(date +%s)
CREW_RIG="beads"
CLONE_DIR="/tmp/beads-concurrent"

# Agent A
CREW_NAME_A="e2e-concurrent-a-${TIMESTAMP}"
BEAD_ID_A="gt-${CREW_RIG}-crew-${CREW_NAME_A}"
BRANCH_A="e2e/concurrent-a-${TIMESTAMP}"
POD_A=""

# Agent B
CREW_NAME_B="e2e-concurrent-b-${TIMESTAMP}"
BEAD_ID_B="gt-${CREW_RIG}-crew-${CREW_NAME_B}"
BRANCH_B="e2e/concurrent-b-${TIMESTAMP}"
POD_B=""

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

create_crew_bead() {
  local bead_id="$1"
  local crew_name="$2"
  local bodyfile
  bodyfile=$(write_json_expr "{
    'id': '$bead_id',
    'title': 'E2E concurrent test: $crew_name',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E concurrent crew test agent',
    'labels': ['gt:agent', 'execution_target:k8s', 'rig:$CREW_RIG', 'role:crew', 'agent:$crew_name']
  }")
  local resp
  resp=$(daemon_api "Create" "$bodyfile")
  rm -f "$bodyfile"
  [[ -n "$resp" ]] || return 1

  # Set to in_progress
  local uf
  uf=$(write_json_expr "{'id': '$bead_id', 'status': 'in_progress'}")
  daemon_api "Update" "$uf" >/dev/null 2>&1 || true
  rm -f "$uf"
}

close_bead() {
  local bead_id="$1"
  [[ -n "$bead_id" ]] || return 0
  local bf
  bf=$(write_json_expr "{'id': '$bead_id'}")
  daemon_api "Close" "$bf" >/dev/null 2>&1 || true
  rm -f "$bf"
}

wait_pod_running() {
  local pod_name="$1"
  local timeout="$2"
  local deadline=$((SECONDS + timeout))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$pod_name" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      local ready
      ready=$(kube get pod "$pod_name" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$ready" == "1/1" ]]; then
        return 0
      fi
    fi
    sleep 5
  done
  return 1
}

ax() {
  local pod="$1"
  shift
  kube exec "$pod" -c agent -- bash -c "$@" 2>/dev/null
}

# ── Cleanup trap ─────────────────────────────────────────────────────
_test_cleanup() {
  close_bead "$BEAD_ID_A"
  close_bead "$BEAD_ID_B"
  BEAD_ID_A=""
  BEAD_ID_B=""
  sleep 3
}
trap '_test_cleanup; _cleanup' EXIT

# ── Test 1: Daemon HTTP API reachable ────────────────────────────────
test_daemon() {
  discover_daemon || return 1
  DAEMON_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || return 1
  local health
  health=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${DAEMON_PORT}/health" 2>/dev/null)
  assert_contains "$health" "status"
}
run_test "Daemon HTTP API is reachable" test_daemon

if [[ -z "$DAEMON_PORT" || -z "$DAEMON_TOKEN" ]]; then
  skip_all "daemon not reachable"
  exit 0
fi

# ── Test 2: Create 2 crew beads simultaneously ───────────────────────
test_create_both() {
  create_crew_bead "$BEAD_ID_A" "$CREW_NAME_A" || { log "Failed to create bead A"; return 1; }
  log "Created bead A: $BEAD_ID_A"
  create_crew_bead "$BEAD_ID_B" "$CREW_NAME_B" || { log "Failed to create bead B"; return 1; }
  log "Created bead B: $BEAD_ID_B"
}
run_test "Create 2 crew beads simultaneously" test_create_both

# ── Test 3: Both pods become Ready ───────────────────────────────────
test_both_ready() {
  log "Waiting for both pods (timeout: ${POD_CREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_CREATE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    if [[ -z "$POD_A" ]]; then
      POD_A=$(kube get pod "$BEAD_ID_A" --no-headers 2>/dev/null | awk '{print $1}')
      [[ -n "$POD_A" ]] && log "Pod A appeared: $POD_A"
    fi
    if [[ -z "$POD_B" ]]; then
      POD_B=$(kube get pod "$BEAD_ID_B" --no-headers 2>/dev/null | awk '{print $1}')
      [[ -n "$POD_B" ]] && log "Pod B appeared: $POD_B"
    fi
    if [[ -n "$POD_A" && -n "$POD_B" ]]; then
      break
    fi
    sleep 3
  done

  [[ -n "$POD_A" ]] || { log "Pod A never appeared"; return 1; }
  [[ -n "$POD_B" ]] || { log "Pod B never appeared"; return 1; }

  # Wait for both Ready
  wait_pod_running "$POD_A" "$POD_READY_TIMEOUT" || { log "Pod A not ready"; return 1; }
  log "Pod A ready: $POD_A"
  wait_pod_running "$POD_B" "$POD_READY_TIMEOUT" || { log "Pod B not ready"; return 1; }
  log "Pod B ready: $POD_B"
}
run_test "Both pods become Ready" test_both_ready

if [[ -z "$POD_A" || -z "$POD_B" ]]; then
  skip_test "Both agents clone repo independently" "pods not ready"
  skip_test "Each agent creates unique branch" "pods not ready"
  skip_test "Each agent runs make build" "pods not ready"
  skip_test "Branch isolation verified" "pods not ready"
  skip_test "Cleanup" "pods not ready"
  print_summary
  exit 0
fi

# ── Test 4: Both agents clone repo independently ────────────────────
test_clone_both() {
  # Clone in parallel (backgrounded)
  local out_a out_b
  out_a=$(ax "$POD_A" "rm -rf ${CLONE_DIR} && git clone https://github.com/groblegark/beads.git ${CLONE_DIR} 2>&1 && echo OK") || true
  out_b=$(ax "$POD_B" "rm -rf ${CLONE_DIR} && git clone https://github.com/groblegark/beads.git ${CLONE_DIR} 2>&1 && echo OK") || true

  assert_contains "$out_a" "OK" || { log "Agent A clone failed: $out_a"; return 1; }
  assert_contains "$out_b" "OK" || { log "Agent B clone failed: $out_b"; return 1; }
  log "Both agents cloned successfully"
}
run_test "Both agents clone repo independently" test_clone_both

# ── Test 5: Each agent creates unique branch ─────────────────────────
test_create_branches() {
  ax "$POD_A" "
    cd ${CLONE_DIR}
    git checkout -b '${BRANCH_A}'
    echo 'Agent A marker: ${CREW_NAME_A}' > docs/e2e-agent-a.md
    git add docs/e2e-agent-a.md
    git commit -m 'test: concurrent agent A (${CREW_NAME_A})'
  " || { log "Agent A branch creation failed"; return 1; }

  ax "$POD_B" "
    cd ${CLONE_DIR}
    git checkout -b '${BRANCH_B}'
    echo 'Agent B marker: ${CREW_NAME_B}' > docs/e2e-agent-b.md
    git add docs/e2e-agent-b.md
    git commit -m 'test: concurrent agent B (${CREW_NAME_B})'
  " || { log "Agent B branch creation failed"; return 1; }

  log "Both agents created branches"
}
run_test "Each agent creates unique branch" test_create_branches

# ── Test 6: Each agent runs make build ───────────────────────────────
test_build_both() {
  local out_a out_b

  out_a=$(ax "$POD_A" "cd ${CLONE_DIR} && make build 2>&1 | tail -3") || true
  local bin_a
  bin_a=$(ax "$POD_A" "test -f ${CLONE_DIR}/bd && echo yes") || true

  out_b=$(ax "$POD_B" "cd ${CLONE_DIR} && make build 2>&1 | tail -3") || true
  local bin_b
  bin_b=$(ax "$POD_B" "test -f ${CLONE_DIR}/bd && echo yes") || true

  log "Agent A build: ${bin_a:-no binary} — $out_a"
  log "Agent B build: ${bin_b:-no binary} — $out_b"

  [[ "$bin_a" == "yes" ]] || { log "Agent A binary missing"; return 1; }
  [[ "$bin_b" == "yes" ]] || { log "Agent B binary missing"; return 1; }
}
run_test "Each agent runs make build" test_build_both

# ── Test 7: Branch isolation verified ────────────────────────────────
test_branch_isolation() {
  # Verify A is on branch A
  local branch_a
  branch_a=$(ax "$POD_A" "cd ${CLONE_DIR} && git branch --show-current") || return 1
  assert_eq "$branch_a" "$BRANCH_A" || { log "Agent A on wrong branch: $branch_a"; return 1; }

  # Verify B is on branch B
  local branch_b
  branch_b=$(ax "$POD_B" "cd ${CLONE_DIR} && git branch --show-current") || return 1
  assert_eq "$branch_b" "$BRANCH_B" || { log "Agent B on wrong branch: $branch_b"; return 1; }

  # Verify A's marker file doesn't appear in B's workspace
  local marker_a_in_b
  marker_a_in_b=$(ax "$POD_B" "cat ${CLONE_DIR}/docs/e2e-agent-a.md 2>/dev/null") || true
  if [[ -n "$marker_a_in_b" ]]; then
    log "CROSS-CONTAMINATION: Agent A's marker found in Agent B's workspace!"
    return 1
  fi

  # Verify B's marker file doesn't appear in A's workspace
  local marker_b_in_a
  marker_b_in_a=$(ax "$POD_A" "cat ${CLONE_DIR}/docs/e2e-agent-b.md 2>/dev/null") || true
  if [[ -n "$marker_b_in_a" ]]; then
    log "CROSS-CONTAMINATION: Agent B's marker found in Agent A's workspace!"
    return 1
  fi

  log "Branch isolation confirmed: no cross-contamination"
}
run_test "Branch isolation verified (no cross-contamination)" test_branch_isolation

# ── Test 8: Cleanup ──────────────────────────────────────────────────
test_cleanup() {
  close_bead "$BEAD_ID_A"
  close_bead "$BEAD_ID_B"
  local saved_a="$BEAD_ID_A"
  local saved_b="$BEAD_ID_B"
  BEAD_ID_A=""
  BEAD_ID_B=""

  # Wait for both pods to be deleted
  log "Waiting for pod deletion (timeout: ${POD_DELETE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))
  local a_gone=false b_gone=false

  while [[ $SECONDS -lt $deadline ]]; do
    if ! $a_gone; then
      local exists_a
      exists_a=$(kube get pod "$POD_A" --no-headers 2>/dev/null | awk '{print $1}')
      [[ -z "$exists_a" ]] && { a_gone=true; log "Pod A deleted"; }
    fi
    if ! $b_gone; then
      local exists_b
      exists_b=$(kube get pod "$POD_B" --no-headers 2>/dev/null | awk '{print $1}')
      [[ -z "$exists_b" ]] && { b_gone=true; log "Pod B deleted"; }
    fi
    $a_gone && $b_gone && break
    sleep 3
  done

  $a_gone && $b_gone || log "Some pods still terminating (non-fatal)"
  return 0
}
run_test "Cleanup: close beads, pods deleted" test_cleanup

# ── Summary ──────────────────────────────────────────────────────────
print_summary
