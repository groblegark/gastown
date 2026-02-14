#!/usr/bin/env bash
# test-daemon-rpc.sh — Validate daemon RPC endpoints via Connect-RPC HTTP API.
#
# Tests:
#   1. /healthz returns 200 with JSON (no auth)
#   2. /readyz returns 200 (no auth)
#   3. /metrics returns JSON (no auth)
#   4. Connect-RPC Health returns valid JSON (auth)
#   5. Connect-RPC List returns valid JSON (auth)
#   6. Connect-RPC List response has expected fields (auth)
#   7. Connect-RPC ConfigList returns valid JSON (auth)
#   8. No fatal/panic in daemon logs
#
# Usage:
#   ./scripts/test-daemon-rpc.sh [NAMESPACE]

MODULE_NAME="daemon-rpc"
source "$(dirname "$0")/lib.sh"

log "Testing daemon RPC endpoints in namespace: $E2E_NAMESPACE"

# ── Discover daemon pod ────────────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|redis" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "No daemon pod found"
  exit 0
fi

log "Daemon pod: $DAEMON_POD"

# ── Get auth token ─────────────────────────────────────────────────────
DAEMON_TOKEN=""
TOKEN_SECRET=$(kube get secrets --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "daemon-token" | head -1)
if [[ -n "$TOKEN_SECRET" ]]; then
  DAEMON_TOKEN=$(kube get secret "$TOKEN_SECRET" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null)
fi

# ── Port-forward to HTTP API ──────────────────────────────────────────
HTTP_PORT=$(start_port_forward "pod/$DAEMON_POD" 9080) || {
  skip_all "Cannot port-forward to daemon HTTP API"
  exit 0
}
log "HTTP API on localhost:$HTTP_PORT"

BASE="http://127.0.0.1:${HTTP_PORT}"

# Helper: POST to Connect-RPC endpoint with auth
rpc_call() {
  local method="$1"
  local body="${2:-{}}"
  local args=(-s --connect-timeout 5 -X POST -H "Content-Type: application/json")
  if [[ -n "$DAEMON_TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $DAEMON_TOKEN")
  fi
  args+=(-d "$body")
  curl "${args[@]}" "${BASE}/bd.v1.BeadsService/${method}" 2>/dev/null
}

# ── Test 1: /healthz returns 200 with JSON ─────────────────────────────
HEALTH_RESP=""

test_healthz() {
  local code
  HEALTH_RESP=$(curl -s --connect-timeout 5 "${BASE}/healthz" 2>/dev/null)
  code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "${BASE}/healthz" 2>/dev/null)
  assert_eq "$code" "200" || return 1
  echo "$HEALTH_RESP" | python3 -m json.tool >/dev/null 2>&1
}
run_test "/healthz returns 200 with valid JSON" test_healthz

# ── Test 2: /healthz has status, version, uptime ──────────────────────
test_health_fields() {
  [[ -n "$HEALTH_RESP" ]] || return 1
  assert_contains "$HEALTH_RESP" "status" || return 1
  assert_contains "$HEALTH_RESP" "version" || return 1
  assert_contains "$HEALTH_RESP" "uptime"
}
run_test "/healthz has status, version, uptime" test_health_fields

# ── Test 3: /readyz returns 200 ────────────────────────────────────────
test_readyz() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "${BASE}/readyz" 2>/dev/null)
  assert_eq "$code" "200"
}
run_test "/readyz returns 200" test_readyz

# ── Test 4: /metrics returns JSON ─────────────────────────────────────
test_metrics() {
  local resp
  resp=$(curl -s --connect-timeout 5 "${BASE}/metrics" 2>/dev/null)
  [[ -n "$resp" ]] || return 1
  echo "$resp" | python3 -m json.tool >/dev/null 2>&1
}
run_test "/metrics returns valid JSON" test_metrics

# ── Test 5: Connect-RPC Health endpoint ────────────────────────────────
if [[ -z "$DAEMON_TOKEN" ]]; then
  skip_test "RPC Health returns valid JSON" "no daemon token found"
  skip_test "RPC List returns valid JSON" "no daemon token found"
  skip_test "RPC List has expected fields" "no daemon token found"
  skip_test "RPC ConfigList returns valid JSON" "no daemon token found"
else
  RPC_HEALTH=""

  test_rpc_health() {
    RPC_HEALTH=$(rpc_call "Health")
    [[ -n "$RPC_HEALTH" ]] || return 1
    echo "$RPC_HEALTH" | python3 -m json.tool >/dev/null 2>&1
  }
  run_test "RPC Health returns valid JSON" test_rpc_health

  # ── Test 6: Connect-RPC List ─────────────────────────────────────────
  LIST_RESP=""

  test_rpc_list() {
    LIST_RESP=$(rpc_call "List" '{}')
    [[ -n "$LIST_RESP" ]] || return 1
    echo "$LIST_RESP" | python3 -m json.tool >/dev/null 2>&1
  }
  run_test "RPC List returns valid JSON" test_rpc_list

  # ── Test 7: List response has expected fields ────────────────────────
  test_list_fields() {
    [[ -n "$LIST_RESP" ]] || return 1
    local has_fields
    has_fields=$(echo "$LIST_RESP" | python3 -c "
import sys, json
d = json.load(sys.stdin)
# Response is either a list or an object with issues/beads key
items = d if isinstance(d, list) else d.get('issues', d.get('beads', d.get('items', [])))
if not items:
    print('empty')
else:
    b = items[0]
    has_id = 'id' in b
    has_title = 'title' in b
    has_status = 'status' in b
    print('ok' if (has_id and has_title and has_status) else 'missing')
" 2>/dev/null)
    if [[ "$has_fields" == "empty" ]]; then
      log "  No issues returned (empty database)"
      return 0
    fi
    assert_eq "$has_fields" "ok"
  }
  run_test "RPC List has expected fields (id, title, status)" test_list_fields

  # ── Test 8: Connect-RPC ConfigList ──────────────────────────────────
  test_rpc_config() {
    local resp
    resp=$(rpc_call "ConfigList" '{}')
    [[ -n "$resp" ]] || return 1
    echo "$resp" | python3 -m json.tool >/dev/null 2>&1
  }
  run_test "RPC ConfigList returns valid JSON" test_rpc_config
fi

# ── Test 9: No fatal/panic in daemon logs ─────────────────────────────
test_no_fatal() {
  local count
  count=$(kube logs "$DAEMON_POD" -c daemon --tail=100 2>/dev/null \
    | grep -ci "fatal\|panic" || true)
  assert_eq "${count:-0}" "0"
}
run_test "No fatal/panic in daemon logs (last 100)" test_no_fatal

# ── Summary ───────────────────────────────────────────────────────────
print_summary
