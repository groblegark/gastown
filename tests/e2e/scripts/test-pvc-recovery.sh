#!/usr/bin/env bash
# test-pvc-recovery.sh — Verify agent session resumes after pod deletion.
#
# Tests that PVC-backed state survives pod deletion and that the controller
# recreates the pod with the same PVC:
#   1. Daemon HTTP API is reachable
#   2. Identify an agent pod with a PVC
#   3. Record PVC name and write marker file to PVC
#   4. Delete agent pod (not PVC)
#   5. Controller creates replacement pod (within timeout)
#   6. New pod has the same PVC bound
#   7. Marker file persists on PVC
#   8. Coop session state preserved from previous run
#
# IMPORTANT: This test deletes a real agent pod. It uses an EXISTING agent
# rather than creating one (cf. test-controller-create-pod.sh which creates
# a fresh bead). The controller should reconcile the missing pod and recreate.
#
# Requires:
#   - Daemon HTTP API accessible (port 9080)
#   - BD_DAEMON_TOKEN for authentication
#   - At least one running agent pod with a PVC
#   - Controller watching beads and reconciling pods
#
# Usage:
#   ./scripts/test-pvc-recovery.sh [NAMESPACE]

MODULE_NAME="pvc-recovery"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing PVC recovery after pod deletion in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
POD_RECREATE_TIMEOUT=180  # seconds to wait for replacement pod
POD_READY_TIMEOUT=180     # seconds to wait for pod to become Ready
MARKER_FILE="/home/agent/gt/.state/e2e-pvc-recovery-marker"
MARKER_VALUE="pvc-recovery-$(date +%s)"

# ── State ──────────────────────────────────────────────────────────
AGENT_POD=""
AGENT_BEAD_ID=""
PVC_NAME=""
DAEMON_POD=""
DAEMON_PORT=""
DAEMON_TOKEN=""

# ── Discover daemon ──────────────────────────────────────────────────
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
  # Marker file cleanup is intentionally NOT done — it's harmless and
  # useful for debugging. The next test run overwrites it.
  :
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
  skip_test "Identify agent pod with PVC" "daemon not reachable"
  skip_test "Record PVC state and write marker" "daemon not reachable"
  skip_test "Delete agent pod" "daemon not reachable"
  skip_test "Controller creates replacement pod" "daemon not reachable"
  skip_test "New pod has same PVC bound" "daemon not reachable"
  skip_test "Marker file persists on PVC" "daemon not reachable"
  skip_test "Coop session state preserved" "daemon not reachable"
  print_summary
  exit 0
fi

# ── Test 2: Identify an agent pod with PVC ────────────────────────────
test_find_agent_pod() {
  # Find running agent pods (gt-* prefix)
  local pods
  pods=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | grep "Running")

  # Pick the first agent pod that has a PVC
  while IFS= read -r line; do
    local podname
    podname=$(echo "$line" | awk '{print $1}')
    [[ -n "$podname" ]] || continue

    local pvc
    pvc=$(kube get pod "$podname" -o json 2>/dev/null | \
      jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' 2>/dev/null | head -1)

    if [[ -n "$pvc" ]]; then
      AGENT_POD="$podname"
      PVC_NAME="$pvc"

      # Get the bead ID from pod labels
      AGENT_BEAD_ID=$(kube get pod "$podname" -o jsonpath='{.metadata.labels.gastown\.io/bead-id}' 2>/dev/null)

      log "Selected pod: $AGENT_POD (PVC: $PVC_NAME, bead: $AGENT_BEAD_ID)"
      return 0
    fi
  done <<< "$pods"

  log "No running agent pods with PVCs found"
  return 1
}
run_test "Identify agent pod with PVC" test_find_agent_pod

if [[ -z "$AGENT_POD" ]]; then
  skip_test "Record PVC state and write marker" "no agent pod with PVC"
  skip_test "Delete agent pod" "no agent pod with PVC"
  skip_test "Controller creates replacement pod" "no agent pod with PVC"
  skip_test "New pod has same PVC bound" "no agent pod with PVC"
  skip_test "Marker file persists on PVC" "no agent pod with PVC"
  skip_test "Coop session state preserved" "no agent pod with PVC"
  print_summary
  exit 0
fi

# ── Test 3: Record PVC state and write marker ─────────────────────────
COOP_STATE_BEFORE=""
test_write_marker() {
  # Verify PVC is Bound
  local phase
  phase=$(kube get pvc "$PVC_NAME" -o jsonpath='{.status.phase}' 2>/dev/null)
  [[ "$phase" == "Bound" ]] || { log "PVC $PVC_NAME is $phase, not Bound"; return 1; }

  # Record coop state directory listing (for later comparison)
  COOP_STATE_BEFORE=$(kube exec "$AGENT_POD" -c agent -- sh -c \
    'ls -la /home/agent/gt/.state/coop/ 2>/dev/null' 2>/dev/null) || true
  log "Coop state before: $(echo "$COOP_STATE_BEFORE" | wc -l | tr -d ' ') entries"

  # Write marker file to PVC
  kube exec "$AGENT_POD" -c agent -- sh -c \
    "echo '$MARKER_VALUE' > $MARKER_FILE" 2>/dev/null || return 1

  # Verify marker was written
  local readback
  readback=$(kube exec "$AGENT_POD" -c agent -- cat "$MARKER_FILE" 2>/dev/null)
  assert_eq "$readback" "$MARKER_VALUE"
}
run_test "Record PVC state and write marker file" test_write_marker

# ── Test 4: Delete agent pod ──────────────────────────────────────────
OLD_POD_UID=""
test_delete_pod() {
  # Record the pod UID so we can distinguish old vs new
  OLD_POD_UID=$(kube get pod "$AGENT_POD" -o jsonpath='{.metadata.uid}' 2>/dev/null)
  log "Deleting pod $AGENT_POD (uid: ${OLD_POD_UID:0:8}...)"

  kube delete pod "$AGENT_POD" --wait=false 2>/dev/null || return 1

  # Wait for pod to actually disappear or enter Terminating
  local deadline=$((SECONDS + 60))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$AGENT_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ -z "$phase" ]]; then
      log "Pod $AGENT_POD gone"
      return 0
    fi
    sleep 2
  done

  # Pod might still exist but be Terminating — that's acceptable
  log "Pod $AGENT_POD still terminating (proceeding)"
}
run_test "Delete agent pod (not PVC)" test_delete_pod

# ── Test 5: Controller creates replacement pod ────────────────────────
NEW_POD=""
test_replacement_pod() {
  log "Waiting for controller to create replacement pod (timeout: ${POD_RECREATE_TIMEOUT}s)..."
  local deadline=$((SECONDS + POD_RECREATE_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    # Strategy 1: Look for pod with same bead-id label
    if [[ -n "$AGENT_BEAD_ID" ]]; then
      local candidates
      candidates=$(kube get pods -l "gastown.io/bead-id=$AGENT_BEAD_ID" --no-headers 2>/dev/null | awk '{print $1}')
      for cand in $candidates; do
        local uid
        uid=$(kube get pod "$cand" -o jsonpath='{.metadata.uid}' 2>/dev/null)
        if [[ "$uid" != "$OLD_POD_UID" ]]; then
          NEW_POD="$cand"
          log "Found replacement pod: $NEW_POD (uid: ${uid:0:8}...)"
          return 0
        fi
      done
    fi

    # Strategy 2: Look for any NEW gt-* pod with our PVC
    local all_gt_pods
    all_gt_pods=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}')
    for pod in $all_gt_pods; do
      local uid
      uid=$(kube get pod "$pod" -o jsonpath='{.metadata.uid}' 2>/dev/null)
      [[ "$uid" != "$OLD_POD_UID" ]] || continue

      local pvc
      pvc=$(kube get pod "$pod" -o json 2>/dev/null | \
        jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' 2>/dev/null | head -1)
      if [[ "$pvc" == "$PVC_NAME" ]]; then
        NEW_POD="$pod"
        log "Found replacement pod via PVC match: $NEW_POD"
        return 0
      fi
    done

    sleep 5
  done

  log "No replacement pod appeared within ${POD_RECREATE_TIMEOUT}s"
  return 1
}
run_test "Controller creates replacement pod" test_replacement_pod

if [[ -z "$NEW_POD" ]]; then
  skip_test "New pod has same PVC bound" "no replacement pod"
  skip_test "Marker file persists on PVC" "no replacement pod"
  skip_test "Coop session state preserved" "no replacement pod"
  print_summary
  exit 0
fi

# Wait for new pod to be Ready before running remaining tests
log "Waiting for $NEW_POD to become Ready (timeout: ${POD_READY_TIMEOUT}s)..."
deadline=$((SECONDS + POD_READY_TIMEOUT))
while [[ $SECONDS -lt $deadline ]]; do
  phase=$(kube get pod "$NEW_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  if [[ "$phase" == "Running" ]]; then
    ready=$(kube get pod "$NEW_POD" --no-headers 2>/dev/null | awk '{print $2}')
    if [[ "$ready" =~ ^([0-9]+)/\1$ ]]; then
      log "Pod $NEW_POD is Running and Ready ($ready)"
      break
    fi
  fi
  sleep 5
done

# ── Test 6: New pod has same PVC bound ────────────────────────────────
test_same_pvc() {
  local new_pvc
  new_pvc=$(kube get pod "$NEW_POD" -o json 2>/dev/null | \
    jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName' 2>/dev/null | head -1)
  log "New pod PVC: $new_pvc (expected: $PVC_NAME)"
  assert_eq "$new_pvc" "$PVC_NAME"
}
run_test "New pod has same PVC bound" test_same_pvc

# ── Test 7: Marker file persists on PVC ──────────────────────────────
test_marker_persists() {
  local readback
  readback=$(kube exec "$NEW_POD" -c agent -- cat "$MARKER_FILE" 2>/dev/null)
  log "Marker readback: '$readback' (expected: '$MARKER_VALUE')"
  assert_eq "$readback" "$MARKER_VALUE"
}
run_test "Marker file persists on PVC after pod recreation" test_marker_persists

# ── Test 8: Coop session state preserved ──────────────────────────────
test_coop_state_preserved() {
  # Verify coop state directory still exists and has content
  local state_exists
  state_exists=$(kube exec "$NEW_POD" -c agent -- sh -c \
    'test -d /home/agent/gt/.state/coop && echo yes' 2>/dev/null)
  [[ "$state_exists" == "yes" ]] || { log "Coop state directory missing"; return 1; }

  local state_after
  state_after=$(kube exec "$NEW_POD" -c agent -- sh -c \
    'ls -la /home/agent/gt/.state/coop/ 2>/dev/null' 2>/dev/null) || true
  local count_after
  count_after=$(echo "$state_after" | wc -l | tr -d ' ')
  log "Coop state after: $count_after entries"

  # State directory should still have content (if it had content before)
  if [[ -n "$COOP_STATE_BEFORE" ]]; then
    local count_before
    count_before=$(echo "$COOP_STATE_BEFORE" | wc -l | tr -d ' ')
    # After recreation, coop may have same or more entries (never fewer)
    assert_ge "$count_after" "$count_before"
  else
    # Even if empty before, directory should exist
    [[ "$state_exists" == "yes" ]]
  fi
}
run_test "Coop session state preserved on PVC" test_coop_state_preserved

# ── Summary ──────────────────────────────────────────────────────────
print_summary
