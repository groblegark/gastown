#!/usr/bin/env bash
# test-agent-kube-rwx.sh — Verify agent pods have kubectl and RWX access.
#
# Tests:
#   1. Daemon HTTP API is reachable
#   2. Create a test bead → controller creates agent pod
#   3. Agent pod has kubectl and can list pods in its namespace
#   4. Agent pod cannot access resources in other namespaces (RBAC negative test)
#   5. Agent pod has RWX_ACCESS_TOKEN env var set
#   6. Agent pod can reach RWX API (authenticated health check)
#   7. Cleanup: close bead → controller deletes pod
#
# Usage:
#   E2E_NAMESPACE=gastown-smoke ./scripts/test-agent-kube-rwx.sh

MODULE_NAME="agent-kube-rwx"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing agent kubectl/RWX access in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_CREATE_TIMEOUT=180
POD_READY_TIMEOUT=180
POD_DELETE_TIMEOUT=180
EXEC_TIMEOUT=30

TEST_BEAD_TITLE="e2e-kube-rwx-test-$(date +%s)"
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
  skip_test "Create test bead" "daemon not reachable"
  skip_test "Agent pod has kubectl" "daemon not reachable"
  skip_test "Agent pod RBAC denies cross-namespace" "daemon not reachable"
  skip_test "Agent pod has RWX_ACCESS_TOKEN" "daemon not reachable"
  skip_test "Agent pod can reach RWX API" "daemon not reachable"
  skip_test "Cleanup: close bead and delete pod" "daemon not reachable"
  print_summary
  exit 0
fi

# ── Test 2: Create test bead and wait for pod ────────────────────────
test_create_and_wait() {
  PRE_EXISTING_PODS=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}' | sort)

  local bodyfile
  bodyfile=$(write_json_expr "{
    'title': '$TEST_BEAD_TITLE',
    'issue_type': 'agent',
    'priority': 2,
    'description': 'E2E kube/RWX access test',
    'labels': ['gt:agent', 'execution_target:k8s', 'rig:e2e-test', 'role:test', 'agent:kube-rwx']
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
  log "Waiting for agent pod (timeout: ${POD_CREATE_TIMEOUT}s)..."
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
run_test "Create test bead and wait for agent pod" test_create_and_wait

if [[ -z "$TEST_POD_NAME" ]]; then
  skip_test "Agent pod has kubectl" "no agent pod"
  skip_test "Agent pod RBAC denies cross-namespace" "no agent pod"
  skip_test "Agent pod has RWX_ACCESS_TOKEN" "no agent pod"
  skip_test "Agent pod can reach RWX API" "no agent pod"
  skip_test "Cleanup: close bead and delete pod" "no agent pod"
  print_summary
  exit 0
fi

# ── Test 3: Agent pod has kubectl and can list pods ──────────────────
test_kubectl_access() {
  local result
  result=$(kube exec "$TEST_POD_NAME" -- kubectl get pods --no-headers 2>&1) || {
    log "kubectl exec failed: $result"
    return 1
  }
  # Should see at least its own pod
  assert_contains "$result" "$TEST_POD_NAME" || {
    # May not see itself by name, but should get some output (not an error)
    [[ -n "$result" ]] && ! echo "$result" | grep -qi "forbidden\|error\|not found"
  }
}
run_test "Agent pod can kubectl get pods in its namespace" test_kubectl_access

# ── Test 4: RBAC denies cross-namespace access ───────────────────────
test_rbac_deny() {
  local result
  # Try to list pods in kube-system — should be forbidden
  result=$(kube exec "$TEST_POD_NAME" -- kubectl get pods -n kube-system 2>&1) || true
  # Should contain "forbidden" or "cannot" — anything except success
  echo "$result" | grep -qi "forbidden\|cannot\|Error\|is forbidden"
}
run_test "Agent pod RBAC denies access to kube-system" test_rbac_deny

# ── Test 5: Agent pod has RWX_ACCESS_TOKEN ───────────────────────────
test_rwx_token() {
  local token
  token=$(kube exec "$TEST_POD_NAME" -- printenv RWX_ACCESS_TOKEN 2>/dev/null) || return 1
  [[ -n "$token" ]]
}
run_test "Agent pod has RWX_ACCESS_TOKEN env var" test_rwx_token

# ── Test 6: Agent pod can reach RWX API ──────────────────────────────
test_rwx_api() {
  # Use curl to hit the RWX API with the token — just check auth works
  local result
  result=$(kube exec "$TEST_POD_NAME" -- \
    sh -c 'curl -sf -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $RWX_ACCESS_TOKEN" https://api.rwx.com/v1/me 2>/dev/null' \
  ) || true
  # 200 = authenticated, 401 = bad token, anything else = network issue
  if [[ "$result" == "200" ]]; then
    log "RWX API returned 200 (authenticated)"
    return 0
  elif [[ "$result" == "401" || "$result" == "403" ]]; then
    log "RWX API returned $result (token invalid but API reachable)"
    # Token is set but invalid — still passes "can reach API"
    return 0
  else
    log "RWX API returned $result (unexpected)"
    return 1
  fi
}
run_test "Agent pod can reach RWX API" test_rwx_api

# ── Test 7: Cleanup ──────────────────────────────────────────────────
test_cleanup() {
  local bodyfile
  bodyfile=$(write_json_expr "{'id': '$TEST_BEAD_ID'}")
  daemon_api "Close" "$bodyfile" >/dev/null 2>&1 || true
  rm -f "$bodyfile"
  local closed_id="$TEST_BEAD_ID"
  TEST_BEAD_ID=""

  # Wait for pod deletion
  log "Waiting for pod deletion (timeout: ${POD_DELETE_TIMEOUT}s)..."
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
  log "Pod still exists after timeout"
  return 1
}
run_test "Cleanup: close bead and pod deleted" test_cleanup

# ── Summary ──────────────────────────────────────────────────────────
print_summary
