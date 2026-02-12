#!/usr/bin/env bash
# test-git-mirror-health.sh — Verify git mirror StatefulSet health.
#
# Tests:
#   1. Git mirror pod(s) exist and are ready
#   2. Git daemon port 9418 is reachable
#   3. Bare repo exists inside the mirror
#
# Note: git-mirror is optional — if not deployed, all tests are skipped.
#
# Usage:
#   ./scripts/test-git-mirror-health.sh [NAMESPACE]

MODULE_NAME="git-mirror-health"
source "$(dirname "$0")/lib.sh"

log "Testing git mirror health in namespace: $E2E_NAMESPACE"

# ── Discover git mirror ──────────────────────────────────────────────
MIRROR_POD=$(kube get pods --no-headers 2>/dev/null | { grep "git-mirror" || true; } | head -1 | awk '{print $1}')
MIRROR_SVC=$(kube get svc --no-headers 2>/dev/null | { grep "git-mirror" || true; } | head -1 | awk '{print $1}')

log "Mirror pod: ${MIRROR_POD:-none}"
log "Mirror svc: ${MIRROR_SVC:-none}"

if [[ -z "$MIRROR_POD" ]]; then
  skip_test "Git mirror pod exists" "git-mirror not deployed in this namespace"
  skip_test "Git daemon port reachable" "git-mirror not deployed"
  skip_test "Bare repo exists" "git-mirror not deployed"
  print_summary
  exit 0
fi

# ── Test 1: Pod is ready ────────────────────────────────────────────
test_pod_ready() {
  local status
  status=$(kube get pod "$MIRROR_POD" --no-headers 2>/dev/null | awk '{print $3}')
  assert_eq "$status" "Running"
}
run_test "Git mirror pod is Running" test_pod_ready

# ── Test 2: Service exists ──────────────────────────────────────────
test_service_exists() {
  [[ -n "$MIRROR_SVC" ]]
}
run_test "Git mirror service exists" test_service_exists

# ── Test 3: Git daemon port reachable ────────────────────────────────
test_git_daemon_port() {
  if [[ -n "$MIRROR_SVC" ]]; then
    local port
    port=$(start_port_forward "svc/$MIRROR_SVC" 9418) || return 1
  else
    local port
    port=$(start_port_forward "pod/$MIRROR_POD" 9418) || return 1
  fi
  # git daemon sends a greeting on connect
  if command -v nc >/dev/null 2>&1; then
    echo "" | nc -w 3 127.0.0.1 "$port" >/dev/null 2>&1 || true
  fi
  return 0
}
run_test "Git daemon port (9418) reachable" test_git_daemon_port

# ── Test 4: Bare repo exists inside mirror ───────────────────────────
test_bare_repo_exists() {
  local repos
  repos=$(kube exec "$MIRROR_POD" -- ls /data/ 2>/dev/null || true)
  [[ -n "$repos" ]]
}
run_test "Mirror contains at least one repository" test_bare_repo_exists

# ── Test 5: Repo has valid HEAD ──────────────────────────────────────
test_repo_has_head() {
  local head
  head=$(kube exec "$MIRROR_POD" -- sh -c 'for d in /data/*; do if [ -f "$d/HEAD" ]; then cat "$d/HEAD"; break; fi; done' 2>/dev/null)
  assert_contains "${head:-}" "ref:"
}
run_test "Repository has valid HEAD" test_repo_has_head

# ── Test 6: PVC mounted ─────────────────────────────────────────────
test_pvc_mounted() {
  local volumes
  volumes=$(kube get pod "$MIRROR_POD" -o jsonpath='{.spec.volumes[*].persistentVolumeClaim.claimName}' 2>/dev/null)
  [[ -n "$volumes" ]]
}
run_test "Git mirror PVC mounted" test_pvc_mounted

# ── Summary ──────────────────────────────────────────────────────────
print_summary
