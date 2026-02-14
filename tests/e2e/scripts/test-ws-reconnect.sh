#!/usr/bin/env bash
# test-ws-reconnect.sh — WebSocket reconnection after broker restart.
#
# Tests that the mux WebSocket endpoint is functional and that connections
# can be re-established after a broker pod restart.
#
# Tests:
#   Phase 1 — WebSocket Connectivity:
#     1. Coop broker pod running (health 200)
#     2. WebSocket connects and receives events
#     3. WebSocket receives session list event
#
#   Phase 2 — Restart Recovery:
#     4. Restart broker pod (delete and wait for replacement)
#     5. Health API functional after restart
#     6. WebSocket connects after restart
#     7. WebSocket receives events after restart
#
# Usage:
#   ./scripts/test-ws-reconnect.sh [NAMESPACE]

MODULE_NAME="ws-reconnect"
source "$(dirname "$0")/lib.sh"

log "Testing WebSocket reconnection in namespace: $E2E_NAMESPACE"

# ── Configuration ────────────────────────────────────────────────────
BROKER_RESTART_TIMEOUT=120

# ── Discover coop broker ─────────────────────────────────────────────
BROKER_POD=""
BROKER_SVC=""
BROKER_CONTAINER=""
BROKER_TOKEN=""
BROKER_PORT=""

discover_broker() {
  BROKER_POD=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$BROKER_POD" ]] || return 1

  BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  [[ -n "$BROKER_SVC" ]] || return 1

  BROKER_CONTAINER=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)

  # Get auth token: try COOP_MUX_AUTH_TOKEN (merged coopmux), then BROKER_TOKEN
  BROKER_TOKEN=""
  if [[ -n "$BROKER_CONTAINER" ]]; then
    BROKER_TOKEN=$(kube exec "$BROKER_POD" -c "$BROKER_CONTAINER" -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null || echo "")
    if [[ -z "$BROKER_TOKEN" ]]; then
      BROKER_TOKEN=$(kube exec "$BROKER_POD" -c "$BROKER_CONTAINER" -- printenv BROKER_TOKEN 2>/dev/null || echo "")
    fi
  fi

  log "Broker pod: $BROKER_POD (container: ${BROKER_CONTAINER:-unknown})"
  log "Broker svc: $BROKER_SVC"
  if [[ -n "$BROKER_TOKEN" ]]; then
    log "Broker token: set (${#BROKER_TOKEN} chars)"
  else
    log "Broker token: NOT SET"
  fi
}

# Helper: WebSocket connect + receive via python3 websocket-client.
# Prints "connected" on success, then "event: <type>" for each received frame.
# Returns 0 if connection succeeds.
ws_connect() {
  local port="$1"
  local token="${2:-}"
  local path="${3:-/ws/mux}"
  local recv_timeout="${4:-5}"

  python3 << PYEOF
import websocket, json, sys

url = "ws://127.0.0.1:${port}${path}"
headers = []
token = "${token}"
if token:
    headers.append("Authorization: Bearer " + token)

ws = websocket.WebSocket()
ws.settimeout(${recv_timeout})
try:
    ws.connect(url, header=headers)
except Exception as e:
    # Retry with token as query param
    if token:
        try:
            ws.connect(url + "?token=" + token)
        except Exception as e2:
            print("error: " + str(e2), file=sys.stderr)
            sys.exit(1)
    else:
        print("error: " + str(e), file=sys.stderr)
        sys.exit(1)

print("connected")
# Try to receive up to 2 frames
for _ in range(2):
    try:
        frame = ws.recv()
        data = json.loads(frame)
        etype = data.get("event", data.get("type", "unknown"))
        print("event: " + etype)
    except (websocket.WebSocketTimeoutException, Exception):
        break
ws.close()
PYEOF
}

# ── Check python3 websocket module ────────────────────────────────────
if ! python3 -c "import websocket" 2>/dev/null; then
  log "python3 websocket module not available — installing"
  pip3 install --quiet websocket-client 2>/dev/null || true
  if ! python3 -c "import websocket" 2>/dev/null; then
    skip_all "python3 websocket-client module required but not installable"
    print_summary
    exit 0
  fi
fi

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: WebSocket Connectivity
# ═══════════════════════════════════════════════════════════════════════

# ── Test 1: Coop broker pod running ──────────────────────────────────
test_broker_running() {
  discover_broker || return 1
  BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
  local resp
  resp=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/health" 2>/dev/null)
  assert_eq "$resp" "200"
}
run_test "Coop broker pod running (health 200)" test_broker_running

if [[ -z "$BROKER_PORT" ]]; then
  skip_test "WebSocket connects and receives events" "broker not reachable"
  skip_test "WebSocket receives session list event" "broker not reachable"
  skip_test "Restart broker pod" "broker not reachable"
  skip_test "Health API functional after restart" "broker not reachable"
  skip_test "WebSocket connects after restart" "broker not reachable"
  skip_test "WebSocket receives events after restart" "broker not reachable"
  print_summary
  exit 0
fi

# ── Test 2: WebSocket connects and receives events ───────────────────
WS_OUTPUT=""
WS_PATH="/ws/mux"
test_ws_connect() {
  WS_OUTPUT=$(ws_connect "$BROKER_PORT" "$BROKER_TOKEN" "$WS_PATH" 5 2>&1)
  local rc=$?
  log "WebSocket output: $(echo "$WS_OUTPUT" | head -3 | tr '\n' ' ')"
  echo "$WS_OUTPUT" | head -1 | grep -q "connected"
}
run_test "WebSocket connects and receives events" test_ws_connect

# ── Test 3: WebSocket receives session list event ────────────────────
test_ws_sessions_event() {
  [[ -n "$WS_OUTPUT" ]] || return 1
  # The mux sends a "sessions" event on connect
  echo "$WS_OUTPUT" | grep -q "event: sessions"
}
run_test "WebSocket receives session list event" test_ws_sessions_event

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Restart Recovery
# ═══════════════════════════════════════════════════════════════════════

# ── Test 4: Restart broker pod ───────────────────────────────────────
BROKER_POD_BEFORE=""
test_restart_broker() {
  BROKER_POD_BEFORE="$BROKER_POD"
  [[ -n "$BROKER_POD_BEFORE" ]] || return 1

  log "Deleting broker pod $BROKER_POD_BEFORE..."
  kube delete pod "$BROKER_POD_BEFORE" --wait=false 2>/dev/null || return 1

  # Stop existing port-forward (will reconnect below)
  stop_port_forwards
  BROKER_PORT=""

  # Wait for new broker pod to be Running with all containers ready
  log "Waiting for new broker pod (timeout: ${BROKER_RESTART_TIMEOUT}s)..."
  local deadline=$((SECONDS + BROKER_RESTART_TIMEOUT))
  local new_broker=""
  while [[ $SECONDS -lt $deadline ]]; do
    new_broker=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
    if [[ -n "$new_broker" && "$new_broker" != "$BROKER_POD_BEFORE" ]]; then
      local ready total
      ready=$(kube get pod "$new_broker" --no-headers 2>/dev/null | awk '{print $2}' | cut -d/ -f1)
      total=$(kube get pod "$new_broker" --no-headers 2>/dev/null | awk '{print $2}' | cut -d/ -f2)
      if [[ "${ready:-0}" -gt 0 && "$ready" == "$total" ]]; then
        log "New broker pod: $new_broker ($ready/$total)"
        BROKER_POD="$new_broker"
        break
      fi
    fi
    sleep 3
  done

  [[ -n "$new_broker" && "$new_broker" != "$BROKER_POD_BEFORE" ]] || { log "Broker did not restart"; return 1; }

  # Re-establish port-forward
  BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
  log "Port-forward re-established: localhost:$BROKER_PORT"
}
run_test "Restart broker pod" test_restart_broker

if [[ -z "$BROKER_PORT" ]]; then
  skip_test "Health API functional after restart" "broker not reachable after restart"
  skip_test "WebSocket connects after restart" "broker not reachable after restart"
  skip_test "WebSocket receives events after restart" "broker not reachable after restart"
  print_summary
  exit 0
fi

# ── Test 5: Health API functional after restart ──────────────────────
test_health_after_restart() {
  local resp
  resp=$(curl -sf --connect-timeout 5 \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/health" 2>/dev/null)
  log "Health response: $resp"
  assert_contains "$resp" "session_count"
}
run_test "Health API functional after restart" test_health_after_restart

# ── Test 6: WebSocket connects after restart ─────────────────────────
WS_OUTPUT_AFTER=""
test_ws_connect_after() {
  WS_OUTPUT_AFTER=$(ws_connect "$BROKER_PORT" "$BROKER_TOKEN" "$WS_PATH" 5 2>&1)
  log "WebSocket after restart: $(echo "$WS_OUTPUT_AFTER" | head -3 | tr '\n' ' ')"
  echo "$WS_OUTPUT_AFTER" | head -1 | grep -q "connected"
}
run_test "WebSocket connects after restart" test_ws_connect_after

# ── Test 7: WebSocket receives events after restart ──────────────────
test_ws_events_after() {
  [[ -n "$WS_OUTPUT_AFTER" ]] || return 1
  # Should receive at least the sessions event
  echo "$WS_OUTPUT_AFTER" | grep -q "event:"
}
run_test "WebSocket receives events after restart" test_ws_events_after

# ── Summary ──────────────────────────────────────────────────────────
print_summary
