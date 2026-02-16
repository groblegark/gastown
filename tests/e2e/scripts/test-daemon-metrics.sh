#!/usr/bin/env bash
# test-daemon-metrics.sh — verify Prometheus metrics and health/readiness probes.
#
# Tests:
#   1. /healthz returns 200
#   2. /readyz returns 200
#   3. /metrics returns valid JSON
#   4. Metrics include uptime_seconds
#   5. Metrics include operation counters
#   6. Metrics include goroutine/memory stats
#   7. No operations have 100% error rate
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-daemon-metrics.sh

set -euo pipefail
MODULE_NAME="daemon-metrics"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing daemon metrics and probes in $NS"

# ── Discover daemon ──────────────────────────────────────────────────
DAEMON_SVC=$(kube get svc --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|headless\|standby" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_SVC" ]]; then
  skip_all "no daemon service found"
  exit 0
fi

# ── Port-forward to daemon HTTP API ───────────────────────────────────
DAEMON_PORT=""

setup_daemon() {
  if [[ -z "$DAEMON_PORT" ]]; then
    DAEMON_PORT=$(start_port_forward "svc/$DAEMON_SVC" 9080) || return 1
  fi
}

json_field() {
  local json_str="$1" expr="$2"
  python3 -c "
import json, sys
d = json.loads(sys.stdin.read())
$expr
" <<< "$json_str" 2>/dev/null
}

# ── Test functions ─────────────────────────────────────────────────────

test_healthz() {
  setup_daemon || return 1
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://127.0.0.1:${DAEMON_PORT}/healthz" 2>/dev/null)
  log "/healthz → HTTP $code"
  assert_eq "$code" "200"
}

test_readyz() {
  [[ -n "$DAEMON_PORT" ]] || return 1
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://127.0.0.1:${DAEMON_PORT}/readyz" 2>/dev/null)
  log "/readyz → HTTP $code"
  assert_eq "$code" "200"
}

METRICS=""

test_metrics_valid_json() {
  [[ -n "$DAEMON_PORT" ]] || return 1
  METRICS=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${DAEMON_PORT}/metrics" 2>/dev/null)
  [[ -n "$METRICS" ]] || return 1
  # Verify it's valid JSON
  local valid
  valid=$(json_field "$METRICS" "print('yes')")
  assert_eq "$valid" "yes"
}

test_metrics_uptime() {
  [[ -n "$METRICS" ]] || return 1
  local uptime
  uptime=$(json_field "$METRICS" "print(d.get('uptime_seconds', -1))")
  log "uptime_seconds: $uptime"
  assert_gt "${uptime%.*}" 0
}

test_metrics_operations() {
  [[ -n "$METRICS" ]] || return 1
  local op_count
  op_count=$(json_field "$METRICS" "
ops = d.get('operations', [])
print(len(ops))
")
  log "Operation types tracked: $op_count"
  # Should have at least health and a few RPC operations
  assert_gt "${op_count:-0}" 0
}

test_metrics_system_stats() {
  [[ -n "$METRICS" ]] || return 1
  local result
  result=$(json_field "$METRICS" "
mem = d.get('memory_alloc_mb', -1)
goroutines = d.get('goroutine_count', -1)
print(f'{mem}|{goroutines}')
")
  local mem goroutines
  mem=$(echo "$result" | cut -d'|' -f1)
  goroutines=$(echo "$result" | cut -d'|' -f2)
  log "Memory: ${mem}MB, Goroutines: $goroutines"
  assert_gt "${mem%.*}" 0
  assert_gt "$goroutines" 0
}

test_no_total_failures() {
  [[ -n "$METRICS" ]] || return 1
  local result
  result=$(json_field "$METRICS" "
ops = d.get('operations', [])
bad = []
for op in ops:
    total = op.get('total_count', 0)
    errors = op.get('error_count', 0)
    if total > 0 and errors == total:
        bad.append(op.get('operation', '?'))
if bad:
    print('FAIL:' + ','.join(bad))
else:
    print('ok')
")
  if [[ "$result" == "ok" ]]; then
    return 0
  else
    log "Operations with 100% error rate: $result"
    return 1
  fi
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "/healthz returns 200" test_healthz
run_test "/readyz returns 200" test_readyz
run_test "/metrics returns valid JSON" test_metrics_valid_json
run_test "Metrics include uptime_seconds" test_metrics_uptime
run_test "Metrics include operation counters" test_metrics_operations
run_test "Metrics include goroutine/memory stats" test_metrics_system_stats
run_test "No operations have 100% error rate" test_no_total_failures

print_summary
