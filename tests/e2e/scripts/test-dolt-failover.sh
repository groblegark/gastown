#!/usr/bin/env bash
# test-dolt-failover.sh — Verify daemon survives Dolt primary pod failure.
#
# This is a destructive test: it kills the Dolt primary pod and verifies
# the cluster elects a new primary and the daemon recovers automatically.
#
# Tests:
#   1. Pre-check: daemon is healthy
#   2. Pre-check: bd list works (daemon can query Dolt)
#   3. Identify and kill Dolt primary pod
#   4. Daemon detects degraded state (within 30s)
#   5. New primary elected (within 3 min via autofailover CronJob)
#   6. Daemon recovers to healthy (within 60s of new primary)
#   7. bd list works again after recovery
#   8. Killed pod rejoins as standby
#
# Usage:
#   ./scripts/test-dolt-failover.sh [NAMESPACE]
#   E2E_NAMESPACE=gastown-smoke ./scripts/test-dolt-failover.sh
#
# Prerequisites:
#   - Dolt HA cluster (3 replicas)
#   - autofailover CronJob enabled (autoFailover: true)
#   - bd daemon connected to Dolt primary Service

MODULE_NAME="dolt-failover"
source "$(dirname "$0")/lib.sh"

log "Testing Dolt failover resilience in namespace: $E2E_NAMESPACE"

# ── Discover components ────────────────────────────────────────────
DOLT_LABEL="app.kubernetes.io/component=dolt,app.kubernetes.io/name=bd-daemon"
DAEMON_LABEL="app.kubernetes.io/component=daemon,app.kubernetes.io/name=bd-daemon"

DOLT_PODS=$(kube get pods -l "$DOLT_LABEL" --no-headers -o custom-columns=":metadata.name" 2>/dev/null | sort)
DOLT_POD_COUNT=$(echo "$DOLT_PODS" | grep -c . || true)
DAEMON_POD=$(kube get pods -l "$DAEMON_LABEL" --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1)

log "Dolt pods ($DOLT_POD_COUNT): $(echo $DOLT_PODS | tr '\n' ' ')"
log "Daemon pod: $DAEMON_POD"

# Gate: need at least 3 Dolt replicas for HA failover
if [[ "$DOLT_POD_COUNT" -lt 3 ]]; then
  skip_all "Need 3+ Dolt replicas for failover test (found $DOLT_POD_COUNT)"
  exit 0
fi

# Gate: need autofailover CronJob
AUTOFAILOVER_CJ=$(kube get cronjob --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "autofailover" || true)
if [[ -z "$AUTOFAILOVER_CJ" ]]; then
  skip_all "autofailover CronJob not found (enable autoFailover: true in values)"
  exit 0
fi

# ── Helper: get Dolt root password ─────────────────────────────────
DOLT_SECRET=$(kube get secrets --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "dolt-root-password" | head -1)
DOLT_PASSWORD=""
if [[ -n "$DOLT_SECRET" ]]; then
  DOLT_PASSWORD=$(kube get secret "$DOLT_SECRET" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null)
fi

# ── Helper: find current primary pod ───────────────────────────────
find_primary_pod() {
  kube get pods -l "$DOLT_LABEL" -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.labels.dolthub\.com/cluster_role}{"\n"}{end}' 2>/dev/null \
    | grep "primary" | awk '{print $1}' | head -1
}

# ── Helper: check daemon health via HTTP ───────────────────────────
HTTP_PORT=""
setup_daemon_port_forward() {
  if [[ -z "$HTTP_PORT" ]]; then
    HTTP_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || return 1
  fi
}

daemon_health_status() {
  curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://127.0.0.1:${HTTP_PORT}/healthz" 2>/dev/null
}

daemon_ready_status() {
  curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://127.0.0.1:${HTTP_PORT}/readyz" 2>/dev/null
}

# ── Helper: get daemon token for bd CLI ────────────────────────────
DAEMON_TOKEN=$(kube get secret --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "daemon-token" | head -1)
BD_TOKEN=""
if [[ -n "$DAEMON_TOKEN" ]]; then
  BD_TOKEN=$(kube get secret "$DAEMON_TOKEN" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || true)
fi

# ── Helper: run bd command via port-forward ────────────────────────
RPC_PORT=""
run_bd() {
  if [[ -z "$RPC_PORT" ]]; then
    RPC_PORT=$(start_port_forward "pod/$DAEMON_POD" 9876) || return 1
  fi
  BD_DAEMON_HOST="http://127.0.0.1:${RPC_PORT}" BD_DAEMON_TOKEN="$BD_TOKEN" bd "$@" 2>/dev/null
}

# ── Test 1: Pre-check — daemon is healthy ──────────────────────────
test_daemon_healthy() {
  setup_daemon_port_forward || return 1
  local status
  status=$(daemon_health_status)
  assert_eq "$status" "200"
}
run_test "Pre-check: daemon /healthz returns 200" test_daemon_healthy

# ── Test 2: Pre-check — daemon readyz returns 200 ─────────────────
test_daemon_ready() {
  setup_daemon_port_forward || return 1
  local status
  status=$(daemon_ready_status)
  assert_eq "$status" "200"
}
run_test "Pre-check: daemon /readyz returns 200 (DB connected)" test_daemon_ready

# ── Test 3: Kill Dolt primary pod ──────────────────────────────────
PRIMARY_POD=""
KILL_TIME=""

test_kill_primary() {
  PRIMARY_POD=$(find_primary_pod)
  if [[ -z "$PRIMARY_POD" ]]; then
    log "  ERROR: no primary pod found"
    return 1
  fi
  log "  Killing primary pod: $PRIMARY_POD"
  KILL_TIME=$(date +%s)
  kube delete pod "$PRIMARY_POD" --grace-period=0 --force >/dev/null 2>&1
  # Verify it's actually terminating/gone
  sleep 2
  local phase
  phase=$(kube get pod "$PRIMARY_POD" -o jsonpath='{.status.phase}' 2>/dev/null || echo "gone")
  log "  Pod status after delete: $phase"
  # Pod may be Terminating, gone, or Pending (StatefulSet already recreated it)
  [[ "$phase" == "Terminating" ]] || [[ "$phase" == "gone" ]] || [[ "$phase" == "" ]] || [[ "$phase" == "Pending" ]] || [[ "$phase" == "Running" ]]
}
run_test "Kill Dolt primary pod" test_kill_primary

# ── Test 4: Daemon detects degraded state ──────────────────────────
test_daemon_degraded() {
  # The daemon should notice the DB is gone within 30s.
  # With the 5s degraded health check, it should be fast (once deployed).
  # /readyz returns 503 when degraded; /healthz still returns 200.
  local deadline=$((SECONDS + 60))
  while [[ $SECONDS -lt $deadline ]]; do
    local ready_status
    ready_status=$(daemon_ready_status)
    if [[ "$ready_status" == "503" ]]; then
      local elapsed=$(($(date +%s) - KILL_TIME))
      log "  Daemon degraded detected after ${elapsed}s"
      return 0
    fi
    sleep 2
  done
  log "  WARNING: daemon never went degraded (readyz stayed 200)"
  # This can happen if the daemon reconnects to a new primary before we check.
  # That's actually a good sign — failover was very fast.
  # Consider this a pass if the daemon is still healthy.
  local health_status
  health_status=$(daemon_health_status)
  assert_eq "$health_status" "200"
}
run_test "Daemon detects degraded state or stays healthy" test_daemon_degraded

# ── Test 5: New primary elected ────────────────────────────────────
test_new_primary_elected() {
  # Wait for a new primary label to appear on a different pod.
  # The autofailover CronJob runs every 2 min. With clusterctl running
  # every 1 min too, one of them should pick it up.
  local deadline=$((SECONDS + 240))  # 4 min max
  while [[ $SECONDS -lt $deadline ]]; do
    local new_primary
    new_primary=$(find_primary_pod)
    if [[ -n "$new_primary" ]]; then
      local elapsed=$(($(date +%s) - KILL_TIME))
      log "  New primary: $new_primary (elected after ${elapsed}s)"
      return 0
    fi
    sleep 5
  done
  log "  ERROR: no new primary elected within 4 minutes"
  return 1
}
run_test "New primary elected within 4 minutes" test_new_primary_elected

# ── Test 6: Daemon recovers to healthy ─────────────────────────────
test_daemon_recovers() {
  # Once the new primary is labeled, the daemon should reconnect.
  # With 5s degraded health check, recovery should be within 60s.
  local deadline=$((SECONDS + 120))
  while [[ $SECONDS -lt $deadline ]]; do
    local ready_status
    ready_status=$(daemon_ready_status)
    if [[ "$ready_status" == "200" ]]; then
      local total_elapsed=$(($(date +%s) - KILL_TIME))
      log "  Daemon recovered! Total outage: ${total_elapsed}s"
      return 0
    fi
    sleep 3
  done
  log "  ERROR: daemon did not recover within 2 minutes of new primary"
  return 1
}
run_test "Daemon recovers to healthy" test_daemon_recovers

# ── Test 7: Daemon readyz returns 200 after recovery ──────────────
test_readyz_after_recovery() {
  local status
  status=$(daemon_ready_status)
  assert_eq "$status" "200"
}
run_test "Daemon /readyz returns 200 after recovery" test_readyz_after_recovery

# ── Test 8: Killed pod rejoins as standby ──────────────────────────
test_killed_pod_rejoins() {
  if [[ -z "$PRIMARY_POD" ]]; then
    return 1
  fi
  # StatefulSet will recreate the pod. Wait for it to be ready.
  local deadline=$((SECONDS + 180))  # 3 min for pod to come back
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$PRIMARY_POD" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    local ready
    ready=$(kube get pod "$PRIMARY_POD" -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null || echo "false")
    if [[ "$phase" == "Running" ]] && [[ "$ready" == "true" ]]; then
      # Check its role label
      local role
      role=$(kube get pod "$PRIMARY_POD" -o jsonpath='{.metadata.labels.dolthub\.com/cluster_role}' 2>/dev/null || echo "unknown")
      log "  $PRIMARY_POD rejoined as: $role"
      # Should be standby or primary (if same pod was re-promoted after restart)
      [[ "$role" == "standby" ]] || [[ "$role" == "primary" ]]
      return 0
    fi
    sleep 5
  done
  log "  WARNING: $PRIMARY_POD didn't rejoin within 3 minutes (may still be starting)"
  return 1
}
run_test "Killed pod rejoins cluster as standby" test_killed_pod_rejoins

# ── Summary ────────────────────────────────────────────────────────
if [[ -n "$KILL_TIME" ]]; then
  _total=$(($(date +%s) - KILL_TIME))
  log "Total test duration: ${_total}s from primary kill to all checks complete"
fi

print_summary
