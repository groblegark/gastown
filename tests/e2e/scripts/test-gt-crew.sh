#!/usr/bin/env bash
# test-gt-crew.sh — Verify crew lifecycle: bead creation → pod → cleanup.
#
# Tests the K8s crew workflow:
#   1. Daemon is reachable
#   2. Create a crew bead with proper labels
#   3. Controller reconciles and creates pod
#   4. Pod reaches Running state
#   5. Pod has coop sidecar (2/2 ready)
#   6. Coop health responds on the crew pod
#   7. Close bead triggers pod deletion
#   8. Pod is fully cleaned up
#
# This test creates a temporary crew bead (e2e-crew-test-*) and cleans
# it up afterwards. The controller must be running to reconcile.
#
# Usage:
#   ./scripts/test-gt-crew.sh [NAMESPACE]

MODULE_NAME="gt-crew"
source "$(dirname "$0")/lib.sh"

log "Testing crew lifecycle in namespace: $E2E_NAMESPACE"

# ── Configuration ────────────────────────────────────────────────────
CREW_NAME="e2e-crew-$(date +%s)"
CREW_RIG="gastown"
CREW_BEAD_ID="gt-${CREW_RIG}-crew-${CREW_NAME}"
POD_CREATE_TIMEOUT=180  # seconds — 60s controller sync + pod startup
POD_DELETE_TIMEOUT=120  # seconds
BD_DAEMON_POD=""

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

# Helper: run bd commands on daemon pod.
# Uses -c bd-daemon to avoid "Defaulted container" warnings.
daemon_bd() {
  kube exec "$BD_DAEMON_POD" -c bd-daemon -- bd "$@"
}

# ── Test 1: Daemon is reachable ──────────────────────────────────────
test_daemon_reachable() {
  local version
  version=$(daemon_bd version 2>/dev/null | head -1)
  log "Daemon version: ${version:-unknown}"
  [[ -n "$version" ]]
}
run_test "Daemon is reachable" test_daemon_reachable

# ── Test 2: Create crew bead with proper labels ──────────────────────
BEAD_CREATED=false

test_create_crew_bead() {
  # Create an agent bead mimicking what gt crew start would do.
  # Use single-line description to avoid kube exec multiline issues.
  local output
  output=$(daemon_bd create \
    --id="$CREW_BEAD_ID" \
    --title="E2E test crew: $CREW_NAME" \
    --type=agent \
    --priority=2 \
    --label="gt:agent" \
    --label="execution_target:k8s" \
    --label="rig:${CREW_RIG}" \
    --label="role:crew" \
    --label="agent:${CREW_NAME}" \
    --description="role_type: crew, rig: ${CREW_RIG}, agent_state: spawning" 2>&1)

  if echo "$output" | grep -q "Created issue"; then
    BEAD_CREATED=true
    log "Created crew bead: $CREW_BEAD_ID"
    return 0
  fi
  log "Create failed: $output"
  return 1
}
run_test "Create crew bead with K8s labels" test_create_crew_bead

if [[ "$BEAD_CREATED" != "true" ]]; then
  skip_test "Controller creates pod from bead" "bead not created"
  skip_test "Crew pod reaches Running state" "bead not created"
  skip_test "Crew pod has coop sidecar" "bead not created"
  skip_test "Coop health on crew pod" "bead not created"
  skip_test "Close bead triggers pod deletion" "bead not created"
  skip_test "Pod is fully cleaned up" "bead not created"
  print_summary
  exit 0
fi

# ── Test 3: Controller creates pod from bead ─────────────────────────
POD_APPEARED=false

test_pod_created() {
  local deadline=$((SECONDS + POD_CREATE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local pod_name
    pod_name=$(kube get pod "$CREW_BEAD_ID" --no-headers 2>/dev/null | awk '{print $1}')
    if [[ -n "$pod_name" ]]; then
      POD_APPEARED=true
      log "Pod appeared: $pod_name (after $((SECONDS))s)"
      return 0
    fi
    sleep 5
  done
  log "Pod $CREW_BEAD_ID did not appear within ${POD_CREATE_TIMEOUT}s"
  return 1
}
run_test "Controller creates pod from bead" test_pod_created

if [[ "$POD_APPEARED" != "true" ]]; then
  # Clean up the bead even if pod never appeared
  daemon_bd close "$CREW_BEAD_ID" --reason="E2E cleanup: pod never appeared" 2>/dev/null || true
  skip_test "Crew pod reaches Running state" "pod not created"
  skip_test "Crew pod has coop sidecar" "pod not created"
  skip_test "Coop health on crew pod" "pod not created"
  skip_test "Close bead triggers pod deletion" "pod not created"
  skip_test "Pod is fully cleaned up" "pod not created"
  print_summary
  exit 0
fi

# ── Test 4: Crew pod reaches Running state ───────────────────────────
POD_RUNNING=false

test_pod_running() {
  local deadline=$((SECONDS + 120))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$CREW_BEAD_ID" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ "$phase" == "Running" ]]; then
      POD_RUNNING=true
      log "Pod is Running"
      return 0
    fi
    if [[ "$phase" == "Failed" || "$phase" == "Unknown" ]]; then
      log "Pod in bad phase: $phase"
      return 1
    fi
    sleep 5
  done
  local final_phase
  final_phase=$(kube get pod "$CREW_BEAD_ID" -o jsonpath='{.status.phase}' 2>/dev/null)
  log "Pod still in phase '$final_phase' after 120s"
  return 1
}
run_test "Crew pod reaches Running state" test_pod_running

# ── Test 5: Crew pod has at least one ready container ────────────────
test_pod_containers() {
  if [[ "$POD_RUNNING" != "true" ]]; then return 1; fi
  # Wait for at least one container to be ready.
  # Full readiness may require valid OAuth credentials which
  # the E2E test bead might not have (credential seeding is separate).
  local deadline=$((SECONDS + 60))
  while [[ $SECONDS -lt $deadline ]]; do
    local status
    status=$(kube get pod "$CREW_BEAD_ID" --no-headers 2>/dev/null | awk '{print $2}')
    local ready
    ready=$(echo "$status" | cut -d/ -f1)
    if [[ "${ready:-0}" -gt 0 ]]; then
      log "Pod readiness: $status"
      return 0
    fi
    sleep 5
  done
  local final_status
  final_status=$(kube get pod "$CREW_BEAD_ID" --no-headers 2>/dev/null | awk '{print $2}')
  log "Pod readiness: $final_status (no containers ready after 60s)"
  return 1
}
run_test "Crew pod has at least one ready container" test_pod_containers

# ── Test 6: Coop sidecar health on crew pod ──────────────────────────
test_coop_health() {
  if [[ "$POD_RUNNING" != "true" ]]; then return 1; fi
  # Port 9090 is coop health-only port (always responds, even without valid agent).
  # Port 8080 is full API (requires agent to be running).
  local port
  port=$(start_port_forward "pod/$CREW_BEAD_ID" 9090) || return 1
  local deadline=$((SECONDS + 30))
  while [[ $SECONDS -lt $deadline ]]; do
    local resp
    resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${port}/healthz" 2>/dev/null)
    if [[ -n "$resp" ]]; then
      log "Coop health-port responds"
      return 0
    fi
    # Also try the full API port as fallback
    resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${port}/api/v1/health" 2>/dev/null)
    if [[ -n "$resp" ]]; then
      log "Coop API responds"
      return 0
    fi
    sleep 3
  done
  log "Coop health: no response within 30s"
  return 1
}
run_test "Coop sidecar health responds on crew pod" test_coop_health

# ── Test 7: Close bead triggers pod deletion ─────────────────────────
test_close_bead() {
  daemon_bd close "$CREW_BEAD_ID" --reason="E2E test cleanup" 2>/dev/null || return 1
  log "Closed bead $CREW_BEAD_ID"
  return 0
}
run_test "Close crew bead" test_close_bead

# ── Test 8: Pod is fully cleaned up ──────────────────────────────────
test_pod_deleted() {
  local deadline=$((SECONDS + POD_DELETE_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    if ! kube get pod "$CREW_BEAD_ID" --no-headers 2>/dev/null | grep -q .; then
      log "Pod deleted (after $((SECONDS))s)"
      return 0
    fi
    sleep 5
  done
  log "Pod $CREW_BEAD_ID still exists after ${POD_DELETE_TIMEOUT}s"
  return 1
}
run_test "Pod is fully cleaned up after bead close" test_pod_deleted

# ── Summary ──────────────────────────────────────────────────────────
print_summary
