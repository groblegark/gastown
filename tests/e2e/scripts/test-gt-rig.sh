#!/usr/bin/env bash
# test-gt-rig.sh — Verify rig-level agent presence and health.
#
# Tests that rig-level agents (witness, refinery) exist as beads and
# have corresponding pods managed by the controller. This validates
# the rig start/create → K8s pod lifecycle.
#
# Tests:
#   1. Daemon is reachable
#   2. At least one rig has agent beads
#   3. Rig agent beads have execution_target:k8s label
#   4. Rig agent pods are running
#   5. Rig agent pods have correct labels
#   6. Agent pods are managed by controller
#
# Note: This test validates existing rig state rather than creating
# a new rig (which requires config and is destructive). For full rig
# create/destroy lifecycle, use a dedicated staging namespace.
#
# Usage:
#   ./scripts/test-gt-rig.sh [NAMESPACE]

MODULE_NAME="gt-rig"
source "$(dirname "$0")/lib.sh"

log "Testing rig agent presence in namespace: $E2E_NAMESPACE"

# ── Find daemon pod ──────────────────────────────────────────────────
BD_DAEMON_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep "bd-daemon-daemon" || true; } \
  | { grep "Running" || true; } \
  | head -1 | awk '{print $1}')

if [[ -z "$BD_DAEMON_POD" ]]; then
  skip_all "no bd-daemon pod running"
  exit 0
fi

log "Using daemon pod: $BD_DAEMON_POD"

daemon_bd() {
  kube exec "$BD_DAEMON_POD" -- bd "$@" 2>/dev/null
}

# ── Test 1: Daemon is reachable ──────────────────────────────────────
test_daemon_reachable() {
  local version
  version=$(daemon_bd version 2>/dev/null | head -1)
  [[ -n "$version" ]]
}
run_test "Daemon is reachable" test_daemon_reachable

# ── Discover rig agents ──────────────────────────────────────────────
# Rig agents are beads with gt:agent label and role in (witness, refinery, polecat, crew)
AGENT_BEADS=$(daemon_bd list --label="gt:agent" 2>/dev/null || true)
RIG_NAMES=""
AGENT_POD_LIST=""

# Extract rig names from running pods
AGENT_POD_LIST=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | awk '{print $1}')

# Identify unique rigs from pod names (gt-<rig>-<role>-<agent>)
for pod in $AGENT_POD_LIST; do
  # Skip town-level agents (gt-town-*)
  if [[ "$pod" == gt-town-* ]]; then continue; fi
  # Extract rig name (second segment)
  rig=$(echo "$pod" | cut -d- -f2)
  if [[ -n "$rig" && "$RIG_NAMES" != *"$rig"* ]]; then
    RIG_NAMES="${RIG_NAMES} ${rig}"
  fi
done

RIG_NAMES=$(echo "$RIG_NAMES" | xargs)  # trim whitespace

if [[ -z "$RIG_NAMES" ]]; then
  log "No rig-level agent pods found (only town-level)"
  skip_test "At least one rig has agent beads" "no rig agents deployed"
  skip_test "Rig agent beads have execution_target:k8s" "no rig agents deployed"
  skip_test "Rig agent pods are Running" "no rig agents deployed"
  skip_test "Rig agent pods have correct labels" "no rig agents deployed"
  skip_test "Agent pods are managed by controller" "no rig agents deployed"
  print_summary
  exit 0
fi

log "Found rigs: $RIG_NAMES"

# ── Test 2: At least one rig has agent beads ─────────────────────────
test_rig_beads_exist() {
  # List agent-type beads and check for rig: labels
  local agent_list
  agent_list=$(daemon_bd list --type=agent 2>/dev/null || true)
  for rig in $RIG_NAMES; do
    local bead_count
    bead_count=$(echo "$agent_list" | { grep -c "rig:${rig}" || true; })
    if [[ "${bead_count:-0}" -gt 0 ]]; then
      log "Rig '$rig' has $bead_count agent bead(s)"
      return 0
    fi
  done
  return 1
}
run_test "At least one rig has agent beads" test_rig_beads_exist

# ── Test 3: Rig agent beads have execution_target:k8s ────────────────
test_k8s_labels() {
  local agent_list
  agent_list=$(daemon_bd list --type=agent 2>/dev/null || true)
  local found=false
  for rig in $RIG_NAMES; do
    local k8s_beads
    k8s_beads=$(echo "$agent_list" | grep "rig:${rig}" | { grep -c "execution_target:k8s" || true; })
    if [[ "${k8s_beads:-0}" -gt 0 ]]; then
      log "Rig '$rig' has $k8s_beads K8s-targeted bead(s)"
      found=true
    fi
  done
  $found
}
run_test "Rig agent beads have execution_target:k8s label" test_k8s_labels

# ── Test 4: Rig agent pods are Running ───────────────────────────────
test_rig_pods_running() {
  local all_running=true
  for pod in $AGENT_POD_LIST; do
    # Skip town-level
    if [[ "$pod" == gt-town-* ]]; then continue; fi
    local phase
    phase=$(kube get pod "$pod" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" != "Running" ]]; then
      log "Pod $pod is $phase (not Running)"
      all_running=false
    fi
  done
  $all_running
}
run_test "Rig agent pods are Running" test_rig_pods_running

# ── Test 5: Rig agent pods have correct labels ───────────────────────
test_rig_pod_labels() {
  local checked=0
  local all_ok=true
  for pod in $AGENT_POD_LIST; do
    if [[ "$pod" == gt-town-* ]]; then continue; fi
    local rig role agent
    rig=$(kube get pod "$pod" -o jsonpath='{.metadata.labels.gastown\.io/rig}' 2>/dev/null)
    role=$(kube get pod "$pod" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
    agent=$(kube get pod "$pod" -o jsonpath='{.metadata.labels.gastown\.io/agent}' 2>/dev/null)
    if [[ -z "$rig" || -z "$role" ]]; then
      log "Pod $pod missing rig/role labels (rig=${rig:-unset}, role=${role:-unset})"
      all_ok=false
    else
      checked=$((checked + 1))
    fi
  done
  log "Checked $checked pod(s) for labels"
  [[ $checked -gt 0 ]] && $all_ok
}
run_test "Rig agent pods have correct labels" test_rig_pod_labels

# ── Test 6: Agent pods have gastown app label (controller-created) ────
test_controller_managed() {
  local checked=0
  local all_managed=true
  for pod in $AGENT_POD_LIST; do
    if [[ "$pod" == gt-town-* ]]; then continue; fi
    local app_name
    app_name=$(kube get pod "$pod" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/name}' 2>/dev/null)
    if [[ "$app_name" == "gastown" ]]; then
      checked=$((checked + 1))
    else
      log "Pod $pod missing app.kubernetes.io/name=gastown label"
      all_managed=false
    fi
  done
  log "Checked $checked pod(s) for gastown app label"
  [[ $checked -gt 0 ]] && $all_managed
}
run_test "Agent pods have gastown app label (controller-created)" test_controller_managed

# ── Summary ──────────────────────────────────────────────────────────
print_summary
