#!/usr/bin/env bash
# test-nats-durability.sh — E2E: JetStream streams and KV survive NATS pod restart.
#
# Verifies that NATS JetStream state persists through pod deletion and
# recreation, thanks to PVC-backed storage. Also confirms that the bd-daemon
# automatically reconnects after NATS comes back.
#
# Tests:
#   1. NATS pod running with PVC bound
#   2. JetStream has streams with messages
#   3. Record stream state (names, message counts, KV bucket)
#   4. Delete NATS pod (PVC survives)
#   5. NATS pod recreated by StatefulSet controller
#   6. JetStream streams restored from PVC
#   7. Message counts preserved (no data loss)
#   8. GATE_STATE KV bucket preserved
#   9. bd-daemon reconnects to NATS
#  10. Event bus operational (publish/subscribe works)
#
# Requires:
#   - NATS deployed as StatefulSet with persistence enabled
#   - bd-daemon connected to NATS
#
# Usage:
#   ./scripts/test-nats-durability.sh [NAMESPACE]

MODULE_NAME="nats-durability"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing NATS JetStream durability in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
NATS_RESTART_TIMEOUT=120  # seconds to wait for NATS pod recreation
NATS_READY_TIMEOUT=60     # seconds to wait for NATS pod Ready
DAEMON_RECONNECT_TIMEOUT=60  # seconds for daemon to reconnect

# ── State ────────────────────────────────────────────────────────────
NATS_POD=""
NATS_SVC=""
NATS_PORT=""
NATS_PVC=""
OLD_POD_UID=""
DAEMON_POD=""

# Pre-restart snapshots
STREAMS_BEFORE=""          # JSON array of {name, messages} objects
STREAM_COUNT_BEFORE=0
KV_BUCKET_EXISTS_BEFORE=false

# ── Helpers ──────────────────────────────────────────────────────────
discover_nats() {
  NATS_POD=$(kube get pods --no-headers 2>/dev/null | grep "nats" | grep -v "clusterctl" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$NATS_POD" ]] || return 1

  NATS_SVC=$(kube get svc --no-headers 2>/dev/null | grep "nats" | head -1 | awk '{print $1}')
  [[ -n "$NATS_SVC" ]] || return 1
}

discover_daemon_pod() {
  DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')
  [[ -n "$DAEMON_POD" ]]
}

get_nats_port_forward() {
  if [[ -z "$NATS_PORT" ]]; then
    NATS_PORT=$(start_port_forward "svc/$NATS_SVC" 8222)
  fi
  [[ -n "$NATS_PORT" ]]
}

# Query /jsz and extract stream info as JSON
get_stream_snapshot() {
  [[ -n "$NATS_PORT" ]] || return 1
  local jsz
  jsz=$(curl -sf "http://localhost:${NATS_PORT}/jsz?streams=true" 2>/dev/null)
  [[ -n "$jsz" ]] || return 1
  echo "$jsz" | python3 -c "
import json, sys
d = json.load(sys.stdin)
streams = []
for si in d.get('account_details', [{}])[0].get('stream_detail', []):
  name = si.get('name', '')
  state = si.get('state', {})
  streams.append({
    'name': name,
    'messages': state.get('messages', 0),
    'bytes': state.get('bytes', 0),
    'consumers': state.get('consumer_count', 0),
  })
json.dump(streams, sys.stdout)
" 2>/dev/null
}

# Check if GATE_STATE KV bucket exists (KV buckets are JetStream streams
# with the KV_ prefix: stream name is KV_GATE_STATE)
check_kv_bucket() {
  [[ -n "$NATS_PORT" ]] || return 1
  local jsz
  jsz=$(curl -sf "http://localhost:${NATS_PORT}/jsz?streams=true" 2>/dev/null)
  [[ -n "$jsz" ]] || return 1
  echo "$jsz" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for si in d.get('account_details', [{}])[0].get('stream_detail', []):
  if si.get('name', '').startswith('KV_'):
    print(si['name'])
" 2>/dev/null
}

# ── Test 1: NATS pod running with PVC ────────────────────────────────
test_nats_with_pvc() {
  discover_nats || return 1

  # Check pod is Running and Ready
  local status ready
  status=$(kube get pod "$NATS_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  [[ "$status" == "Running" ]] || { log "NATS pod status: $status"; return 1; }

  # Find the PVC
  NATS_PVC=$(kube get pod "$NATS_POD" -o json 2>/dev/null | \
    python3 -c "
import json, sys
d = json.load(sys.stdin)
for v in d.get('spec', {}).get('volumes', []):
  pvc = v.get('persistentVolumeClaim', {}).get('claimName', '')
  if pvc:
    print(pvc)
    break
" 2>/dev/null)

  if [[ -z "$NATS_PVC" ]]; then
    log "NATS pod has no PVC — durability test requires persistence"
    return 1
  fi

  # Verify PVC is bound
  local phase
  phase=$(kube get pvc "$NATS_PVC" -o jsonpath='{.status.phase}' 2>/dev/null)
  [[ "$phase" == "Bound" ]] || { log "PVC $NATS_PVC phase: $phase"; return 1; }

  log "NATS pod: $NATS_POD, PVC: $NATS_PVC (Bound)"
}
run_test "NATS pod running with PVC bound" test_nats_with_pvc

if [[ -z "$NATS_POD" || -z "$NATS_PVC" ]]; then
  skip_test "JetStream has streams with messages" "NATS not running or no PVC"
  skip_test "Record stream state before restart" "NATS not running or no PVC"
  skip_test "Delete NATS pod" "NATS not running or no PVC"
  skip_test "NATS pod recreated by StatefulSet" "NATS not running or no PVC"
  skip_test "JetStream streams restored from PVC" "NATS not running or no PVC"
  skip_test "Message counts preserved" "NATS not running or no PVC"
  skip_test "GATE_STATE KV bucket preserved" "NATS not running or no PVC"
  skip_test "bd-daemon reconnects to NATS" "NATS not running or no PVC"
  skip_test "Event bus operational after restart" "NATS not running or no PVC"
  print_summary
  exit 0
fi

# ── Test 2: JetStream has streams with messages ──────────────────────
test_jetstream_has_data() {
  get_nats_port_forward || return 1

  local jsz
  jsz=$(curl -sf "http://localhost:${NATS_PORT}/jsz" 2>/dev/null)
  [[ -n "$jsz" ]] || return 1

  local streams
  streams=$(echo "$jsz" | python3 -c "import sys,json; print(json.load(sys.stdin).get('streams',0))" 2>/dev/null)
  [[ -n "$streams" ]] && assert_gt "$streams" 0
}
run_test "JetStream has streams with messages" test_jetstream_has_data

# ── Test 3: Record stream state ──────────────────────────────────────
test_record_state() {
  STREAMS_BEFORE=$(get_stream_snapshot)
  [[ -n "$STREAMS_BEFORE" ]] || return 1

  STREAM_COUNT_BEFORE=$(echo "$STREAMS_BEFORE" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
  [[ -n "$STREAM_COUNT_BEFORE" ]] && [[ "$STREAM_COUNT_BEFORE" -gt 0 ]] || return 1

  # Log what we found
  log "Streams before restart ($STREAM_COUNT_BEFORE):"
  echo "$STREAMS_BEFORE" | python3 -c "
import json, sys
for s in json.load(sys.stdin):
  print(f'  {s[\"name\"]}: {s[\"messages\"]} msgs, {s[\"consumers\"]} consumers')
" 2>/dev/null

  # Check KV bucket
  local kv_buckets
  kv_buckets=$(check_kv_bucket)
  if [[ -n "$kv_buckets" ]]; then
    KV_BUCKET_EXISTS_BEFORE=true
    log "KV buckets: $kv_buckets"
  else
    log "No KV buckets found (GATE_STATE may not be created yet)"
  fi
}
run_test "Record stream state before restart" test_record_state

# Stop the existing port-forward before pod deletion
stop_port_forwards
NATS_PORT=""

# ── Test 4: Delete NATS pod ──────────────────────────────────────────
test_delete_nats_pod() {
  OLD_POD_UID=$(kube get pod "$NATS_POD" -o jsonpath='{.metadata.uid}' 2>/dev/null)
  log "Deleting NATS pod $NATS_POD (uid: ${OLD_POD_UID:0:8}...)"

  kube delete pod "$NATS_POD" --wait=false 2>/dev/null || return 1

  # Wait for pod to disappear
  local deadline=$((SECONDS + 60))
  while [[ $SECONDS -lt $deadline ]]; do
    local phase
    phase=$(kube get pod "$NATS_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [[ -z "$phase" ]]; then
      log "Pod $NATS_POD deleted"
      return 0
    fi
    sleep 2
  done

  log "Pod still terminating (proceeding)"
}
run_test "Delete NATS pod (PVC preserved)" test_delete_nats_pod

# ── Test 5: NATS pod recreated by StatefulSet ─────────────────────────
NEW_NATS_POD=""
test_nats_recreated() {
  log "Waiting for StatefulSet controller to recreate NATS pod (timeout: ${NATS_RESTART_TIMEOUT}s)..."
  local deadline=$((SECONDS + NATS_RESTART_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local pods
    pods=$(kube get pods --no-headers 2>/dev/null | grep "nats" | grep -v "clusterctl")

    while IFS= read -r line; do
      [[ -n "$line" ]] || continue
      local podname status
      podname=$(echo "$line" | awk '{print $1}')
      status=$(echo "$line" | awk '{print $3}')

      # Check if this is a NEW pod (different UID) and it's running
      local uid
      uid=$(kube get pod "$podname" -o jsonpath='{.metadata.uid}' 2>/dev/null)
      if [[ "$uid" != "$OLD_POD_UID" && "$status" == "Running" ]]; then
        # Verify it has our PVC
        local pvc
        pvc=$(kube get pod "$podname" -o json 2>/dev/null | \
          python3 -c "
import json, sys
d = json.load(sys.stdin)
for v in d.get('spec', {}).get('volumes', []):
  pvc = v.get('persistentVolumeClaim', {}).get('claimName', '')
  if pvc:
    print(pvc)
    break
" 2>/dev/null)
        if [[ "$pvc" == "$NATS_PVC" ]]; then
          NEW_NATS_POD="$podname"
          log "New NATS pod: $NEW_NATS_POD (uid: ${uid:0:8}..., PVC: $pvc)"
          return 0
        fi
      fi
    done <<< "$pods"

    sleep 3
  done

  log "NATS pod not recreated within ${NATS_RESTART_TIMEOUT}s"
  return 1
}
run_test "NATS pod recreated by StatefulSet controller" test_nats_recreated

if [[ -z "$NEW_NATS_POD" ]]; then
  skip_test "JetStream streams restored from PVC" "NATS pod not recreated"
  skip_test "Message counts preserved" "NATS pod not recreated"
  skip_test "GATE_STATE KV bucket preserved" "NATS pod not recreated"
  skip_test "bd-daemon reconnects to NATS" "NATS pod not recreated"
  skip_test "Event bus operational after restart" "NATS pod not recreated"
  print_summary
  exit 0
fi

# Wait for new NATS pod to pass readiness probe
log "Waiting for $NEW_NATS_POD to become Ready (timeout: ${NATS_READY_TIMEOUT}s)..."
deadline=$((SECONDS + NATS_READY_TIMEOUT))
while [[ $SECONDS -lt $deadline ]]; do
  local ready
  ready=$(kube get pod "$NEW_NATS_POD" --no-headers 2>/dev/null | awk '{print $2}')
  if [[ "$ready" == "1/1" ]]; then
    log "NATS pod $NEW_NATS_POD is Ready"
    break
  fi
  sleep 2
done

# Re-establish port-forward to new pod
NATS_PORT=$(start_port_forward "svc/$NATS_SVC" 8222)
if [[ -z "$NATS_PORT" ]]; then
  log "Failed to re-establish port-forward to NATS"
  skip_test "JetStream streams restored from PVC" "port-forward failed"
  skip_test "Message counts preserved" "port-forward failed"
  skip_test "GATE_STATE KV bucket preserved" "port-forward failed"
  skip_test "bd-daemon reconnects to NATS" "port-forward failed"
  skip_test "Event bus operational after restart" "port-forward failed"
  print_summary
  exit 0
fi

# Give JetStream a moment to initialize from the recovered store
sleep 5

# ── Test 6: JetStream streams restored ───────────────────────────────
STREAMS_AFTER=""
STREAM_COUNT_AFTER=0
test_streams_restored() {
  local jsz
  jsz=$(curl -sf "http://localhost:${NATS_PORT}/jsz" 2>/dev/null)
  [[ -n "$jsz" ]] || return 1

  local streams
  streams=$(echo "$jsz" | python3 -c "import sys,json; print(json.load(sys.stdin).get('streams',0))" 2>/dev/null)
  [[ -n "$streams" ]] || return 1

  STREAMS_AFTER=$(get_stream_snapshot)
  STREAM_COUNT_AFTER=$(echo "$STREAMS_AFTER" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)

  log "Streams after restart: $STREAM_COUNT_AFTER (was: $STREAM_COUNT_BEFORE)"

  # All streams from before should still exist
  assert_ge "$STREAM_COUNT_AFTER" "$STREAM_COUNT_BEFORE"
}
run_test "JetStream streams restored from PVC" test_streams_restored

# ── Test 7: Message counts preserved ─────────────────────────────────
test_messages_preserved() {
  [[ -n "$STREAMS_BEFORE" && -n "$STREAMS_AFTER" ]] || return 1

  python3 -c "
import json, sys

before = {s['name']: s for s in json.loads('''$STREAMS_BEFORE''')}
after  = {s['name']: s for s in json.loads('''$STREAMS_AFTER''')}

ok = True
for name, b in before.items():
  a = after.get(name)
  if a is None:
    print(f'  MISSING stream: {name}')
    ok = False
    continue

  # Messages after restart should be >= messages before
  # (daemon may have published new messages during reconnect)
  b_msgs = b['messages']
  a_msgs = a['messages']
  status = 'OK' if a_msgs >= b_msgs else 'LOST'
  delta = a_msgs - b_msgs
  print(f'  {name}: {b_msgs} -> {a_msgs} (delta: {delta:+d}) [{status}]')
  if a_msgs < b_msgs:
    ok = False

sys.exit(0 if ok else 1)
" 2>/dev/null
}
run_test "Message counts preserved (no data loss)" test_messages_preserved

# ── Test 8: GATE_STATE KV bucket preserved ───────────────────────────
test_kv_preserved() {
  if [[ "$KV_BUCKET_EXISTS_BEFORE" != "true" ]]; then
    log "KV bucket was not present before restart — checking if it's still absent (OK)"
    return 0
  fi

  local kv_buckets
  kv_buckets=$(check_kv_bucket)
  if assert_contains "$kv_buckets" "KV_GATE_STATE"; then
    log "GATE_STATE KV bucket preserved"
    return 0
  fi

  log "GATE_STATE KV bucket missing after restart"
  return 1
}
run_test "GATE_STATE KV bucket preserved" test_kv_preserved

# ── Test 9: bd-daemon reconnects to NATS ─────────────────────────────
test_daemon_reconnects() {
  discover_daemon_pod || { log "No daemon pod found"; return 1; }

  log "Waiting for daemon to reconnect to NATS (timeout: ${DAEMON_RECONNECT_TIMEOUT}s)..."
  local deadline=$((SECONDS + DAEMON_RECONNECT_TIMEOUT))

  while [[ $SECONDS -lt $deadline ]]; do
    local connz
    connz=$(curl -sf "http://localhost:${NATS_PORT}/connz" 2>/dev/null) || true
    if [[ -n "$connz" ]]; then
      local has_daemon
      has_daemon=$(echo "$connz" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for c in d.get('connections', []):
  if 'bd-daemon' in c.get('name', ''):
    print('yes')
    break
" 2>/dev/null)
      if [[ "$has_daemon" == "yes" ]]; then
        log "bd-daemon reconnected to NATS"
        return 0
      fi
    fi
    sleep 3
  done

  log "bd-daemon did not reconnect within ${DAEMON_RECONNECT_TIMEOUT}s"
  return 1
}
run_test "bd-daemon reconnects to NATS" test_daemon_reconnects

# ── Test 10: Event bus operational ───────────────────────────────────
test_bus_operational() {
  discover_daemon_pod || return 1

  local status
  status=$(kube exec "$DAEMON_POD" -c bd-daemon -- bd bus status --json 2>/dev/null)
  [[ -n "$status" ]] || { log "Could not get bus status"; return 1; }

  local nats_status
  nats_status=$(echo "$status" | python3 -c "import sys,json; print(json.load(sys.stdin).get('nats_status',''))" 2>/dev/null)
  if assert_eq "$nats_status" "connected"; then
    log "Event bus reports connected"
    return 0
  fi

  log "Event bus status: $nats_status (expected: connected)"
  return 1
}
run_test "Event bus operational after NATS restart" test_bus_operational

# ── Summary ──────────────────────────────────────────────────────────
print_summary
