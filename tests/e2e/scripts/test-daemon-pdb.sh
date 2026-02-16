#!/usr/bin/env bash
# test-daemon-pdb.sh — verify PodDisruptionBudget enforcement.
#
# Tests:
#   1. PDB exists for daemon deployment
#   2. PDB maxUnavailable is configured
#   3. PDB status shows expected pods
#   4. PDB allows at least 1 disruption (when all replicas healthy)
#   5. PDB currentHealthy >= desiredHealthy
#   6. Redis PDB exists (if redis deployed)
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-daemon-pdb.sh

set -euo pipefail
MODULE_NAME="daemon-pdb"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing PodDisruptionBudget enforcement in $NS"

# ── Discover PDBs ──────────────────────────────────────────────────
DAEMON_PDB=$(kube get pdb --no-headers 2>/dev/null | grep "daemon" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_PDB" ]]; then
  skip_all "no daemon PDB found"
  exit 0
fi

log "Daemon PDB: $DAEMON_PDB"

# ── Test functions ─────────────────────────────────────────────────────

test_pdb_exists() {
  [[ -n "$DAEMON_PDB" ]]
}

test_pdb_max_unavailable() {
  local max_unavail
  max_unavail=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.spec.maxUnavailable}' 2>/dev/null)
  local min_avail
  min_avail=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.spec.minAvailable}' 2>/dev/null)
  log "maxUnavailable: ${max_unavail:-<unset>}, minAvailable: ${min_avail:-<unset>}"
  # At least one of these should be set
  [[ -n "$max_unavail" || -n "$min_avail" ]]
}

test_pdb_expected_pods() {
  local expected current
  expected=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.status.expectedPods}' 2>/dev/null)
  current=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.status.currentHealthy}' 2>/dev/null)
  log "Expected pods: ${expected:-?}, Current healthy: ${current:-?}"
  assert_gt "${expected:-0}" 0
}

test_pdb_allows_disruption() {
  local allowed
  allowed=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.status.disruptionsAllowed}' 2>/dev/null)
  log "Disruptions allowed: ${allowed:-0}"
  # When all replicas are healthy and maxUnavailable=1, should allow >= 1
  assert_gt "${allowed:-0}" 0
}

test_pdb_healthy_ge_desired() {
  local healthy desired
  healthy=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.status.currentHealthy}' 2>/dev/null)
  desired=$(kube get pdb "$DAEMON_PDB" -o jsonpath='{.status.desiredHealthy}' 2>/dev/null)
  log "Healthy: ${healthy:-0}, Desired: ${desired:-0}"
  assert_ge "${healthy:-0}" "${desired:-0}"
}

test_redis_pdb() {
  local redis_pdb
  redis_pdb=$(kube get pdb --no-headers 2>/dev/null | grep "redis" | head -1 | awk '{print $1}')
  if [[ -z "$redis_pdb" ]]; then
    log "No Redis PDB found (redis may not be deployed)"
    return 0
  fi
  local healthy
  healthy=$(kube get pdb "$redis_pdb" -o jsonpath='{.status.currentHealthy}' 2>/dev/null)
  log "Redis PDB $redis_pdb: healthy=${healthy:-0}"
  assert_gt "${healthy:-0}" 0
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Daemon PDB exists" test_pdb_exists
run_test "PDB has disruption budget configured" test_pdb_max_unavailable
run_test "PDB status shows expected pods" test_pdb_expected_pods
run_test "PDB allows at least 1 disruption" test_pdb_allows_disruption
run_test "PDB currentHealthy >= desiredHealthy" test_pdb_healthy_ge_desired
run_test "Redis PDB is healthy (if deployed)" test_redis_pdb

print_summary
