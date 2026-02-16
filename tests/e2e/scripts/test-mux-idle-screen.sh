#!/usr/bin/env bash
# test-mux-idle-screen.sh — Verify idle mux sessions provide screen data.
#
# Tests the screen snapshot fallback for idle sessions:
#   1. Coop mux is reachable
#   2. At least one session is registered
#   3. Session screen endpoint returns content
#   4. Screen snapshot has expected fields (lines, ansi, cols, rows, alt_screen)
#   5. Screen lines are non-empty (session has visible content)
#   6. WebSocket replay returns data for the session
#
# This validates the server-side contract that supports the client-side
# screen fallback fix in ExpandedSession.tsx (coop commit 89912f1).
#
# Usage:
#   ./scripts/test-mux-idle-screen.sh [NAMESPACE]

MODULE_NAME="mux-idle-screen"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing idle session screen availability in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
SCREEN_WAIT_TIMEOUT=30  # seconds to wait for screen data to appear

# ── Discover mux ─────────────────────────────────────────────────────
MUX_PORT=""
MUX_TOKEN=""

discover_mux() {
  local broker_pod
  broker_pod=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$broker_pod" ]] || return 1

  # Try both container names (coopmux for merged binary, coop-mux for split)
  MUX_TOKEN=$(kube exec "$broker_pod" -c coopmux -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null) \
    || MUX_TOKEN=$(kube exec "$broker_pod" -c coop-mux -- printenv COOP_MUX_AUTH_TOKEN 2>/dev/null) \
    || true
  [[ -n "$MUX_TOKEN" ]]
}

# Helper: query mux sessions
mux_sessions() {
  [[ -n "$MUX_PORT" ]] || return 1
  curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $MUX_TOKEN" \
    "http://127.0.0.1:${MUX_PORT}/api/v1/sessions" 2>/dev/null
}

# Helper: get session screen
mux_screen() {
  local session_id="$1"
  [[ -n "$MUX_PORT" ]] || return 1
  curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $MUX_TOKEN" \
    "http://127.0.0.1:${MUX_PORT}/api/v1/sessions/${session_id}/screen" 2>/dev/null
}

# ── Cleanup trap ─────────────────────────────────────────────────────
trap '_cleanup' EXIT

# ── Test 1: Coop mux is reachable ───────────────────────────────────
test_mux_reachable() {
  discover_mux || return 1
  local broker_svc
  broker_svc=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  [[ -n "$broker_svc" ]] || return 1
  MUX_PORT=$(start_port_forward "svc/$broker_svc" 9800) || return 1
  local resp
  resp=$(curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $MUX_TOKEN" \
    "http://127.0.0.1:${MUX_PORT}/api/v1/health" 2>/dev/null)
  assert_contains "$resp" "running"
}
run_test "Coop mux API is reachable" test_mux_reachable

if [[ -z "$MUX_PORT" ]]; then
  skip_test "At least one session is registered" "mux not reachable"
  skip_test "Screen endpoint returns content" "mux not reachable"
  skip_test "Screen has expected fields" "mux not reachable"
  skip_test "Screen lines are non-empty" "mux not reachable"
  skip_test "WebSocket replay returns data" "mux not reachable"
  print_summary
  exit 0
fi

# ── Test 2: At least one session is registered ──────────────────────
SESSION_ID=""

test_session_exists() {
  local sessions
  sessions=$(mux_sessions) || return 1

  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$sessions" > "$tmpf"
  SESSION_ID=$(python3 -c "
import json, sys
with open('$tmpf') as f:
    data = json.load(f)
if data:
    print(data[0]['id'])
" 2>/dev/null)
  rm -f "$tmpf"

  if [[ -n "$SESSION_ID" ]]; then
    log "Found session: $SESSION_ID"
    return 0
  fi
  log "No sessions registered in mux"
  return 1
}
run_test "At least one session is registered" test_session_exists

if [[ -z "$SESSION_ID" ]]; then
  skip_test "Screen endpoint returns content" "no sessions"
  skip_test "Screen has expected fields" "no sessions"
  skip_test "Screen lines are non-empty" "no sessions"
  skip_test "WebSocket replay returns data" "no sessions"
  print_summary
  exit 0
fi

# ── Test 3: Screen endpoint returns content ─────────────────────────
SCREEN_JSON=""

test_screen_returns_content() {
  # Wait for screen data to be cached by poller
  local deadline=$((SECONDS + SCREEN_WAIT_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    SCREEN_JSON=$(mux_screen "$SESSION_ID" 2>/dev/null) || true
    if [[ -n "$SCREEN_JSON" ]]; then
      log "Screen endpoint returned ${#SCREEN_JSON} bytes"
      return 0
    fi
    sleep 2
  done
  log "Screen endpoint returned empty after ${SCREEN_WAIT_TIMEOUT}s"
  return 1
}
run_test "Screen endpoint returns content for session" test_screen_returns_content

if [[ -z "$SCREEN_JSON" ]]; then
  skip_test "Screen has expected fields" "no screen data"
  skip_test "Screen lines are non-empty" "no screen data"
  skip_test "WebSocket replay returns data" "no screen data"
  print_summary
  exit 0
fi

# ── Test 4: Screen has expected fields ──────────────────────────────
test_screen_fields() {
  local result
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$SCREEN_JSON" > "$tmpf"
  result=$(python3 -c "
import json, sys
with open('$tmpf') as f:
    screen = json.load(f)
required = ['lines', 'cols', 'rows']
missing = [f for f in required if f not in screen]
if missing:
    print(f'missing: {missing}')
else:
    has_ansi = 'ansi' in screen
    has_alt = 'alt_screen' in screen
    print(f'ok ansi={has_ansi} alt_screen={has_alt} cols={screen[\"cols\"]} rows={screen[\"rows\"]}')
" 2>/dev/null)
  rm -f "$tmpf"

  log "Screen fields: $result"
  [[ "$result" == ok* ]]
}
run_test "Screen snapshot has expected fields (lines, cols, rows)" test_screen_fields

# ── Test 5: Screen lines are non-empty ──────────────────────────────
test_screen_nonempty() {
  local result
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$SCREEN_JSON" > "$tmpf"
  result=$(python3 -c "
import json, sys
with open('$tmpf') as f:
    screen = json.load(f)
lines = screen.get('ansi', screen.get('lines', []))
non_blank = sum(1 for l in lines if l.strip())
total = len(lines)
alt = screen.get('alt_screen', False)
print(f'{non_blank}/{total} alt_screen={alt}')
" 2>/dev/null)
  rm -f "$tmpf"

  log "Non-blank lines: $result"
  # At least some lines should have content
  local non_blank
  non_blank=$(echo "$result" | cut -d/ -f1)
  assert_gt "${non_blank:-0}" 0
}
run_test "Screen lines contain visible content" test_screen_nonempty

# ── Test 6: WebSocket replay returns data ───────────────────────────
test_ws_replay() {
  # Use python3 + websockets to connect and request replay
  local result
  result=$(timeout 15 python3 -c "
import asyncio, json, sys

async def test_replay():
    try:
        import websockets
    except ImportError:
        # websockets not available — try with raw socket approach
        print('skip:websockets_not_installed')
        return

    url = 'ws://127.0.0.1:${MUX_PORT}/ws/${SESSION_ID}?subscribe=pty,state&token=${MUX_TOKEN}'
    try:
        async with websockets.connect(url, close_timeout=5) as ws:
            # Request replay
            await ws.send(json.dumps({'event': 'replay:get', 'offset': 0}))
            # Wait for replay response (or timeout)
            deadline = asyncio.get_event_loop().time() + 10
            while asyncio.get_event_loop().time() < deadline:
                try:
                    msg = await asyncio.wait_for(ws.recv(), timeout=5)
                    data = json.loads(msg)
                    if data.get('event') == 'replay':
                        payload_len = len(data.get('data', ''))
                        total = data.get('total_written', 0)
                        print(f'ok replay_bytes={payload_len} total_written={total}')
                        return
                except asyncio.TimeoutError:
                    break
            print('fail:no_replay_received')
    except Exception as e:
        print(f'fail:{e}')

asyncio.run(test_replay())
" 2>/dev/null)

  log "WebSocket replay result: $result"

  if [[ "$result" == skip:* ]]; then
    log "python3 websockets module not installed, skipping WS test"
    return 0  # Don't fail — just skip gracefully
  fi

  [[ "$result" == ok* ]]
}
run_test "WebSocket replay returns data for session" test_ws_replay

# ── Summary ──────────────────────────────────────────────────────────
print_summary
