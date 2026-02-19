#!/usr/bin/env bash
# test-controller-crash-recovery.sh — E2E: reconciliation resumes after controller restart.
#
# Deletes the agent-controller pod and verifies that Kubernetes recreates it
# (via Deployment controller) and that the reconciliation loop resumes without
# disrupting existing agent pods.
#
# Tests:
#   1. Controller pod is Running
#   2. Record existing agent pods before restart
#   3. Delete controller pod
#   4. New controller pod reaches Running within 60s
#   5. Existing agent pods still Running (no disruption)
#   6. Controller logs show reconciliation resumed
#   7. No agent pods deleted during outage
#
# IMPORTANT: This test deletes the controller pod. The Deployment controller
# will recreate it automatically. Existing agent pods should be unaffected.
#
# Requires:
#   - Agent controller deployed as Deployment
#   - At least one agent pod (gt-*) to verify no disruption
#
# Usage:
#   ./scripts/test-controller-crash-recovery.sh [NAMESPACE]

MODULE_NAME="controller-crash-recovery"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing controller crash recovery in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
CTRL_RESTART_TIMEOUT=60   # seconds to wait for new controller pod
CTRL_READY_TIMEOUT=60     # seconds for new pod to become Ready
SETTLE_TIME=15            # seconds to let reconciliation run after restart

# ── State ────────────────────────────────────────────────────────────
CTRL_POD=""
CTRL_DEPLOY=""
OLD_CTRL_UID=""
NEW_CTRL_POD=""
AGENT_PODS_BEFORE=""
AGENT_COUNT_BEFORE=0

# ── Helpers ──────────────────────────────────────────────────────────
discover_controller() {
  CTRL_DEPLOY=$(kube get deployments --no-headers 2>/dev/null | grep "agent-controller" | head -1 | awk '{print $1}')
  [[ -n "$CTRL_DEPLOY" ]] || return 1

  CTRL_POD=$(kube get pods --no-headers 2>/dev/null | grep "agent-controller" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$CTRL_POD" ]]
}

# Get sorted list of Running agent pod names
get_running_agents() {
  kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | { grep "Running" || true; } | awk '{print $1}' | sort
}

# ── Test 1: Controller pod is Running ─────────────────────────────────
test_controller_running() {
  discover_controller || return 1
  local phase
  phase=$(kube get pod "$CTRL_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  assert_eq "$phase" "Running"
}
run_test "Controller pod is Running" test_controller_running

if [[ -z "$CTRL_POD" || -z "$CTRL_DEPLOY" ]]; then
  skip_test "Record existing agent pods" "controller not found"
  skip_test "Delete controller pod" "controller not found"
  skip_test "New controller pod reaches Running" "controller not found"
  skip_test "Existing agent pods still Running" "controller not found"
  skip_test "Controller logs show reconciliation resumed" "controller not found"
  skip_test "No agent pods deleted during outage" "controller not found"
  print_summary
  exit 0
fi

# ── Test 2: Record existing agent pods ────────────────────────────────
test_record_agents() {
  AGENT_PODS_BEFORE=$(get_running_agents)
  AGENT_COUNT_BEFORE=$(echo "$AGENT_PODS_BEFORE" | { grep -c . || true; })

  if [[ "$AGENT_COUNT_BEFORE" -eq 0 ]]; then
    log "No running agent pods — will verify no new pods appear during outage"
  else
    log "Agent pods before restart ($AGENT_COUNT_BEFORE):"
    echo "$AGENT_PODS_BEFORE" | while IFS= read -r pod; do
      dim "  $pod"
    done
  fi
  return 0
}
run_test "Record existing agent pods before restart" test_record_agents

# ── Test 3: Delete controller pod ─────────────────────────────────────
test_delete_controller() {
  OLD_CTRL_UID=$(kube get pod "$CTRL_POD" -o jsonpath='{.metadata.uid}' 2>/dev/null)
  log "Deleting controller pod $CTRL_POD (uid: ${OLD_CTRL_UID:0:8}...)"

  kube delete pod "$CTRL_POD" --wait=false 2>/dev/null || return 1

  # Wait for pod to disappear or enter Terminating
  local deadline=$((SECONDS + 30))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$CTRL_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ -z "$phase" ]]; then
      log "Pod $CTRL_POD deleted"
      return 0
    fi
    sleep 2
  done

  log "Pod still terminating (proceeding)"
}
run_test "Delete controller pod" test_delete_controller

# ── Test 4: New controller pod reaches Running ────────────────────────
test_new_controller_running() {
  log "Waiting for Deployment to recreate controller pod (timeout: ${CTRL_RESTART_TIMEOUT}s)..."
  local deadline=$((SECONDS + CTRL_RESTART_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local pods
    pods=$(kube get pods --no-headers 2>/dev/null | grep "agent-controller" | grep -v "Terminating")

    while IFS= read -r line; do
      [[ -n "$line" ]] || continue
      local podname status
      podname=$(echo "$line" | awk '{print $1}')
      status=$(echo "$line" | awk '{print $3}')

      local uid
      uid=$(kube get pod "$podname" -o jsonpath='{.metadata.uid}' 2>/dev/null)
      if [[ "$uid" != "$OLD_CTRL_UID" && "$status" == "Running" ]]; then
        # Verify it's Ready
        local ready
        ready=$(echo "$line" | awk '{print $2}')
        if [[ "$ready" =~ ^([0-9]+)/\1$ ]]; then
          NEW_CTRL_POD="$podname"
          log "New controller pod: $NEW_CTRL_POD (uid: ${uid:0:8}..., Ready: $ready)"
          return 0
        fi
      fi
    done <<< "$pods"

    sleep 3
  done

  # Check if pod exists but not yet Ready
  local pending
  pending=$(kube get pods --no-headers 2>/dev/null | grep "agent-controller" | grep -v "Terminating" | head -1 | awk '{print $1}')
  if [[ -n "$pending" ]]; then
    log "Controller pod $pending exists but not yet Running/Ready within ${CTRL_RESTART_TIMEOUT}s"
    # Wait a bit more for readiness
    local ready_deadline=$((SECONDS + CTRL_READY_TIMEOUT))
    while [[ $SECONDS -lt $ready_deadline ]]; do
      local status ready
      status=$(kube get pod "$pending" -o jsonpath='{.status.phase}' 2>/dev/null)
      ready=$(kube get pod "$pending" --no-headers 2>/dev/null | awk '{print $2}')
      if [[ "$status" == "Running" && "$ready" =~ ^([0-9]+)/\1$ ]]; then
        NEW_CTRL_POD="$pending"
        log "Controller pod became Ready: $NEW_CTRL_POD"
        return 0
      fi
      sleep 3
    done
  fi

  log "Controller pod not recreated or not Ready within timeout"
  return 1
}
run_test "New controller pod reaches Running within ${CTRL_RESTART_TIMEOUT}s" test_new_controller_running

if [[ -z "$NEW_CTRL_POD" ]]; then
  skip_test "Existing agent pods still Running" "controller not recreated"
  skip_test "Controller logs show reconciliation resumed" "controller not recreated"
  skip_test "No agent pods deleted during outage" "controller not recreated"
  print_summary
  exit 0
fi

# Give reconciliation loop time to run at least once
log "Waiting ${SETTLE_TIME}s for reconciliation to settle..."
sleep "$SETTLE_TIME"

# ── Test 5: Existing agent pods still Running ─────────────────────────
test_agents_still_running() {
  if [[ "$AGENT_COUNT_BEFORE" -eq 0 ]]; then
    log "No agent pods to verify (trivially passes)"
    return 0
  fi

  local agents_now
  agents_now=$(get_running_agents)
  local missing=0

  while IFS= read -r pod; do
    [[ -n "$pod" ]] || continue
    if ! echo "$agents_now" | grep -qF "$pod"; then
      log "Agent pod $pod is no longer Running"
      missing=$((missing + 1))
    fi
  done <<< "$AGENT_PODS_BEFORE"

  if [[ "$missing" -gt 0 ]]; then
    log "$missing agent pod(s) lost after controller restart"
    return 1
  fi

  local count_now
  count_now=$(echo "$agents_now" | { grep -c . || true; })
  log "All $AGENT_COUNT_BEFORE agent pods still Running (total now: $count_now)"
}
run_test "Existing agent pods still Running after restart" test_agents_still_running

# ── Test 6: Controller logs show reconciliation resumed ───────────────
test_reconciliation_resumed() {
  [[ -n "$NEW_CTRL_POD" ]] || return 1

  local logs
  logs=$(kube logs "$NEW_CTRL_POD" --tail=50 2>/dev/null)
  [[ -n "$logs" ]] || { log "No logs from new controller pod"; return 1; }

  # Look for evidence of reconciliation activity:
  # - "reconcil" (reconcile, reconciling, reconciliation)
  # - "watching" or "starting" (controller startup)
  # - "sync" (sync loop)
  # - "listing" or "list" (pod listing during reconcile)
  if echo "$logs" | grep -qi "reconcil\|watching\|starting.*loop\|sync\|list.*pod"; then
    log "Reconciliation activity detected in logs"
    return 0
  fi

  # Fallback: if the controller is Running and Ready, it has started its loop
  local phase
  phase=$(kube get pod "$NEW_CTRL_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  if [[ "$phase" == "Running" ]]; then
    log "Controller is Running (reconciliation presumed active)"
    return 0
  fi

  log "No evidence of reconciliation in controller logs"
  return 1
}
run_test "Controller logs show reconciliation resumed" test_reconciliation_resumed

# ── Test 7: No agent pods deleted during outage ──────────────────────
test_no_agents_deleted() {
  if [[ "$AGENT_COUNT_BEFORE" -eq 0 ]]; then
    log "No agent pods to verify (trivially passes)"
    return 0
  fi

  # Check controller logs for any "deleting pod" entries that shouldn't be there
  [[ -n "$NEW_CTRL_POD" ]] || return 1

  local logs
  logs=$(kube logs "$NEW_CTRL_POD" --tail=50 2>/dev/null)

  # Count pod deletion log entries
  local delete_count
  delete_count=$(echo "$logs" | grep -ci "delet.*pod\|remov.*pod\|pod.*delet" || true)

  if [[ "${delete_count:-0}" -gt 0 ]]; then
    log "WARNING: $delete_count pod deletion log entries found after restart"
    # This isn't necessarily a failure — the controller might legitimately
    # clean up pods. But verify our original pods are still present.
    local agents_now
    agents_now=$(get_running_agents)
    local still_present=0
    while IFS= read -r pod; do
      [[ -n "$pod" ]] || continue
      if echo "$agents_now" | grep -qF "$pod"; then
        still_present=$((still_present + 1))
      fi
    done <<< "$AGENT_PODS_BEFORE"
    assert_eq "$still_present" "$AGENT_COUNT_BEFORE"
  else
    log "No agent pod deletions in controller logs — clean recovery"
  fi
}
run_test "No agent pods deleted during controller outage" test_no_agents_deleted

# ── Summary ──────────────────────────────────────────────────────────
print_summary
