#!/usr/bin/env bash
# test-redis-health.sh — Verify Redis pod health.
#
# Tests:
#   1. Redis pod is ready (1/1)
#   2. Redis PING returns PONG
#   3. Redis service URL is reachable
#
# Usage:
#   ./scripts/test-redis-health.sh [NAMESPACE]

MODULE_NAME="redis-health"
source "$(dirname "$0")/lib.sh"

log "Testing Redis health in namespace: $E2E_NAMESPACE"

# ── Discover Redis ───────────────────────────────────────────────────
REDIS_POD=$(kube get pods --no-headers 2>/dev/null | grep "redis" | head -1 | awk '{print $1}')
REDIS_SVC=$(kube get svc --no-headers 2>/dev/null | grep "redis" | head -1 | awk '{print $1}')

log "Redis pod: ${REDIS_POD:-none}"
log "Redis svc: ${REDIS_SVC:-none}"

# ── Test 1: Pod is ready ────────────────────────────────────────────
test_pod_ready() {
  [[ -n "$REDIS_POD" ]] || return 1
  local status
  status=$(kube get pod "$REDIS_POD" --no-headers 2>/dev/null | awk '{print $2}')
  assert_eq "$status" "1/1"
}
run_test "Redis pod is ready (1/1)" test_pod_ready

# ── Test 2: Service exists ──────────────────────────────────────────
test_service_exists() {
  [[ -n "$REDIS_SVC" ]]
}
run_test "Redis service exists" test_service_exists

# ── Test 3: PING returns PONG (via kubectl exec) ────────────────────
test_redis_ping() {
  [[ -n "$REDIS_POD" ]] || return 1
  local pong
  pong=$(kube exec "$REDIS_POD" -- redis-cli PING 2>/dev/null)
  assert_eq "$pong" "PONG"
}
run_test "Redis PING returns PONG" test_redis_ping

# ── Test 4: Redis port reachable via port-forward ────────────────────
REDIS_PORT=""

test_redis_port() {
  if [[ -n "$REDIS_SVC" ]]; then
    REDIS_PORT=$(start_port_forward "svc/$REDIS_SVC" 6379) || return 1
  else
    REDIS_PORT=$(start_port_forward "pod/$REDIS_POD" 6379) || return 1
  fi
  # redis-cli or nc
  if command -v redis-cli >/dev/null 2>&1; then
    local pong
    pong=$(redis-cli -h 127.0.0.1 -p "$REDIS_PORT" PING 2>/dev/null)
    assert_eq "$pong" "PONG"
  else
    # TCP connection test — Redis inline commands need \r\n line ending
    printf "PING\r\n" | nc -w 3 127.0.0.1 "$REDIS_PORT" 2>/dev/null | grep -q "PONG"
  fi
}
run_test "Redis port (6379) reachable via port-forward" test_redis_port

# ── Test 5: Redis INFO shows expected version ────────────────────────
test_redis_info() {
  [[ -n "$REDIS_POD" ]] || return 1
  local version
  version=$(kube exec "$REDIS_POD" -- redis-cli INFO server 2>/dev/null | grep "redis_version" | head -1)
  [[ -n "$version" ]]
}
run_test "Redis INFO returns version" test_redis_info

# ── Summary ──────────────────────────────────────────────────────────
print_summary
