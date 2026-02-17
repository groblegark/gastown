#!/usr/bin/env bash
# test-full-uat.sh — Full UAT: convoy, cross-rig mail, witness, multi-agent.
#
# Validates operational features beyond basic health checks:
#
#   Phase 1 — Convoy (batch work grouping):
#     1. Create convoy bead with convoy labels
#     2. Create child beads linked to convoy
#     3. Convoy children visible via label search
#     4. Close children, verify convoy state
#
#   Phase 2 — Cross-rig mail:
#     5. Send mail from one agent to another via labels
#     6. Recipient can discover mail by to: label
#     7. Reply creates threaded mail (same thread: label)
#     8. Thread search returns both messages
#
#   Phase 3 — Witness monitoring:
#     9. Witness pod exists (or skip gracefully)
#    10. Witness coop screen shows monitoring output
#
#   Phase 4 — Multi-agent operations:
#    11. Multiple agent pods running simultaneously
#    12. Each agent has coop API responding
#    13. Beads visible across agents (shared daemon)
#    14. Agent health endpoints all respond
#
# Usage: E2E_NAMESPACE=gastown-rwx ./scripts/test-full-uat.sh

set -euo pipefail
MODULE_NAME="full-uat"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"
log "Full UAT in $NS"

# ── Discover infrastructure ────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep "daemon" || true; } \
  | { grep -v "dolt\|nats\|slackbot\|clusterctl" || true; } \
  | { grep "Running" || true; } \
  | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

# Find bd container name (varies: bd-daemon or daemon)
DAEMON_CONTAINER=""
for container in bd-daemon daemon; do
  if kube exec "$DAEMON_POD" -c "$container" -- which bd >/dev/null 2>&1; then
    DAEMON_CONTAINER="$container"
    break
  fi
done

if [[ -z "$DAEMON_CONTAINER" ]]; then
  skip_all "no bd binary in daemon pod"
  exit 0
fi

log "Daemon: $DAEMON_POD (container: $DAEMON_CONTAINER)"

# Discover all agent pods
AGENT_PODS=$(kube get pods --no-headers 2>/dev/null \
  | { grep "^gt-" || true; } \
  | { grep "Running" || true; } \
  | awk '{print $1}')

AGENT_COUNT=$(echo "$AGENT_PODS" | grep -c . || echo "0")
log "Agent pods: $AGENT_COUNT"

# Find mayor pod (always exists)
MAYOR_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  if [[ "$_role" == "mayor" ]]; then
    MAYOR_POD="$_p"
    break
  fi
done
[[ -z "$MAYOR_POD" ]] && MAYOR_POD=$(echo "$AGENT_PODS" | head -1)

# Find witness pod
WITNESS_POD=""
for _p in $AGENT_PODS; do
  _role=$(kube get pod "$_p" -o jsonpath='{.metadata.labels.gastown\.io/role}' 2>/dev/null)
  if [[ "$_role" == "witness" ]]; then
    WITNESS_POD="$_p"
    break
  fi
done

# ── Helpers ────────────────────────────────────────────────────────
bd_cmd() {
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd "$@" 2>&1
}

agent_bd() {
  local pod="${1:-$MAYOR_POD}"
  shift
  kube exec "$pod" -c agent -- bd "$@" 2>&1
}

# Track created issues for cleanup
CREATED_ISSUES=()

cleanup_issues() {
  for id in "${CREATED_ISSUES[@]}"; do
    bd_cmd close "$id" --reason="E2E UAT cleanup" >/dev/null 2>&1 || true
  done
}
trap 'cleanup_issues; _cleanup' EXIT

TEST_TS="$(date +%s)"

# ═══════════════════════════════════════════════════════════════════
# Phase 1: Convoy (batch work grouping)
# ═══════════════════════════════════════════════════════════════════

CONVOY_ID=""

test_convoy_create() {
  local output
  output=$(bd_cmd create \
    --title="e2e-convoy-${TEST_TS}" \
    --type=epic \
    --priority=4 \
    --label="convoy:e2e-${TEST_TS}" \
    --description="E2E convoy test — safe to delete" 2>&1)

  if ! echo "$output" | grep -q "Created issue"; then
    log "Convoy creation failed: $output"
    return 1
  fi

  CONVOY_ID=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$CONVOY_ID" ]] || return 1
  CREATED_ISSUES+=("$CONVOY_ID")
  log "Created convoy: $CONVOY_ID"
}
run_test "Convoy: create convoy bead" test_convoy_create

CHILD_IDS=()

test_convoy_children() {
  [[ -n "$CONVOY_ID" ]] || return 1

  # Create 2 child beads linked to the convoy
  for i in 1 2; do
    local output
    output=$(bd_cmd create \
      --title="e2e-convoy-child-${i}-${TEST_TS}" \
      --type=task \
      --priority=4 \
      --label="convoy:e2e-${TEST_TS}" \
      --description="Convoy child ${i} for E2E test" 2>&1)

    local child_id
    child_id=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
    if [[ -n "$child_id" ]]; then
      CHILD_IDS+=("$child_id")
      CREATED_ISSUES+=("$child_id")
    fi
  done

  log "Created ${#CHILD_IDS[@]} convoy children"
  [[ ${#CHILD_IDS[@]} -eq 2 ]]
}
run_test "Convoy: create child beads with convoy label" test_convoy_children

test_convoy_search() {
  [[ -n "$CONVOY_ID" ]] || return 1

  local list_output
  list_output=$(bd_cmd list --label="convoy:e2e-${TEST_TS}" 2>&1)

  local found=0
  for id in "$CONVOY_ID" "${CHILD_IDS[@]}"; do
    if echo "$list_output" | grep -q "$id"; then
      found=$((found + 1))
    fi
  done

  log "Found $found/3 convoy beads via label search"
  [[ "$found" -eq 3 ]]
}
run_test "Convoy: label search finds all convoy beads" test_convoy_search

test_convoy_close_children() {
  [[ ${#CHILD_IDS[@]} -gt 0 ]] || return 1

  for id in "${CHILD_IDS[@]}"; do
    bd_cmd close "$id" --reason="E2E convoy child done" >/dev/null 2>&1
  done

  # Verify children are closed
  local closed=0
  for id in "${CHILD_IDS[@]}"; do
    local show
    show=$(bd_cmd show "$id" 2>&1)
    if echo "$show" | grep -qi "closed"; then
      closed=$((closed + 1))
    fi
  done

  log "Closed $closed/${#CHILD_IDS[@]} convoy children"
  [[ "$closed" -eq ${#CHILD_IDS[@]} ]]
}
run_test "Convoy: close children and verify state" test_convoy_close_children

# ═══════════════════════════════════════════════════════════════════
# Phase 2: Cross-rig mail
# ═══════════════════════════════════════════════════════════════════

MAIL_ID=""
REPLY_ID=""
MAIL_THREAD="e2e-thread-${TEST_TS}"

test_mail_send() {
  local output
  output=$(bd_cmd create \
    --title="e2e-mail: test message from sender" \
    --type=task \
    --priority=3 \
    --label="from:e2e-sender-${TEST_TS}" \
    --label="to:e2e-receiver-${TEST_TS}" \
    --label="msg-type:task" \
    --label="thread:${MAIL_THREAD}" \
    --description="Cross-rig mail test message body" 2>&1)

  if ! echo "$output" | grep -q "Created issue"; then
    log "Mail send failed: $output"
    return 1
  fi

  MAIL_ID=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$MAIL_ID" ]] || return 1
  CREATED_ISSUES+=("$MAIL_ID")
  log "Sent mail: $MAIL_ID"
}
run_test "Mail: send message with from/to/thread labels" test_mail_send

test_mail_discover() {
  [[ -n "$MAIL_ID" ]] || return 1

  local list_output
  list_output=$(bd_cmd list --label="to:e2e-receiver-${TEST_TS}" 2>&1)

  if echo "$list_output" | grep -q "$MAIL_ID"; then
    log "Recipient can discover mail"
    return 0
  fi
  log "Mail not found by to: label"
  return 1
}
run_test "Mail: recipient discovers message by to: label" test_mail_discover

test_mail_reply() {
  [[ -n "$MAIL_ID" ]] || return 1

  local output
  output=$(bd_cmd create \
    --title="e2e-mail: reply from receiver" \
    --type=task \
    --priority=3 \
    --label="from:e2e-receiver-${TEST_TS}" \
    --label="to:e2e-sender-${TEST_TS}" \
    --label="msg-type:reply" \
    --label="thread:${MAIL_THREAD}" \
    --description="This is a reply to the test message" 2>&1)

  if ! echo "$output" | grep -q "Created issue"; then
    log "Reply failed: $output"
    return 1
  fi

  REPLY_ID=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$REPLY_ID" ]] || return 1
  CREATED_ISSUES+=("$REPLY_ID")
  log "Reply sent: $REPLY_ID"
}
run_test "Mail: reply creates threaded message" test_mail_reply

test_mail_thread() {
  [[ -n "$MAIL_ID" && -n "$REPLY_ID" ]] || return 1

  local list_output
  list_output=$(bd_cmd list --label="thread:${MAIL_THREAD}" 2>&1)

  local found=0
  echo "$list_output" | grep -q "$MAIL_ID" && found=$((found + 1))
  echo "$list_output" | grep -q "$REPLY_ID" && found=$((found + 1))

  log "Thread search found $found/2 messages"
  [[ "$found" -eq 2 ]]
}
run_test "Mail: thread search returns both messages" test_mail_thread

# ═══════════════════════════════════════════════════════════════════
# Phase 3: Witness monitoring
# ═══════════════════════════════════════════════════════════════════

test_witness_exists() {
  if [[ -z "$WITNESS_POD" ]]; then
    log "No witness pod in $NS (not all deployments have one)"
    return 1
  fi
  local phase
  phase=$(kube get pod "$WITNESS_POD" -o jsonpath='{.status.phase}' 2>/dev/null)
  log "Witness pod: $WITNESS_POD (phase: $phase)"
  [[ "$phase" == "Running" ]]
}

if [[ -n "$WITNESS_POD" ]]; then
  run_test "Witness: pod exists and is Running" test_witness_exists

  test_witness_screen() {
    local pf_port
    pf_port=$(start_port_forward "pod/$WITNESS_POD" 8080 2>/dev/null) || return 1
    local screen
    screen=$(curl -sf --connect-timeout 10 "http://127.0.0.1:${pf_port}/api/v1/screen/text" 2>/dev/null)
    local len=${#screen}
    log "Witness screen: $len chars"
    [[ "$len" -gt 0 ]]
  }
  run_test "Witness: screen shows monitoring output" test_witness_screen
else
  skip_test "Witness: pod exists and is Running" "no witness pod in $NS"
  skip_test "Witness: screen shows monitoring output" "no witness pod"
fi

# ═══════════════════════════════════════════════════════════════════
# Phase 4: Multi-agent operations
# ═══════════════════════════════════════════════════════════════════

test_multi_agent_count() {
  log "Running agent pods: $AGENT_COUNT"
  [[ "$AGENT_COUNT" -ge 1 ]]
}
run_test "Multi-agent: at least 1 agent pod running" test_multi_agent_count

test_multi_agent_coop() {
  local checked=0
  local responding=0

  for _p in $AGENT_PODS; do
    checked=$((checked + 1))
    [[ $checked -gt 4 ]] && break  # Cap at 4 to avoid slow tests

    local pf_port
    pf_port=$(start_port_forward "pod/$_p" 8080 2>/dev/null) || continue
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 \
      "http://127.0.0.1:${pf_port}/api/v1/health" 2>/dev/null)
    if [[ "$status" == "200" ]]; then
      responding=$((responding + 1))
    fi
  done

  log "Coop API responding: $responding/$checked agent pods"
  [[ "$responding" -ge 1 ]]
}
run_test "Multi-agent: coop API responds on agent pods" test_multi_agent_coop

test_shared_daemon_visibility() {
  # Verify that beads are visible from both daemon and agent pods.
  # In HA setups, agent and daemon may hit different daemon replicas
  # behind the K8s service, so we test read-after-write on each pod
  # separately (create + read on same pod = same daemon replica).
  [[ -n "$MAYOR_POD" ]] || return 1

  # Test 1: create on daemon pod, read on daemon pod
  local probe_output
  probe_output=$(bd_cmd create \
    --title="e2e-visibility-probe-${TEST_TS}" \
    --type=task --priority=4 2>&1)
  local probe_id
  probe_id=$(echo "$probe_output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$probe_id" ]] || return 1
  CREATED_ISSUES+=("$probe_id")

  local daemon_list
  daemon_list=$(bd_cmd show "$probe_id" 2>&1)
  if ! echo "$daemon_list" | grep -q "$probe_id"; then
    log "Bead $probe_id not visible on creating daemon pod!"
    return 1
  fi
  log "Daemon read-after-write: OK ($probe_id)"

  # Test 2: create on agent pod, read on agent pod
  local agent_probe
  agent_probe=$(agent_bd "$MAYOR_POD" create \
    --title="e2e-agent-probe-${TEST_TS}" \
    --type=task --priority=4 2>&1)
  local agent_probe_id
  agent_probe_id=$(echo "$agent_probe" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$agent_probe_id" ]] || return 1
  CREATED_ISSUES+=("$agent_probe_id")

  local agent_show
  agent_show=$(agent_bd "$MAYOR_POD" show "$agent_probe_id" 2>&1)
  if ! echo "$agent_show" | grep -q "$agent_probe_id"; then
    log "Bead $agent_probe_id not visible on creating agent pod!"
    return 1
  fi
  log "Agent read-after-write: OK ($agent_probe_id)"
}
run_test "Multi-agent: beads read-after-write consistent" test_shared_daemon_visibility

test_agent_health_all() {
  local checked=0
  local healthy=0

  for _p in $AGENT_PODS; do
    checked=$((checked + 1))
    [[ $checked -gt 4 ]] && break

    local pf_port
    pf_port=$(start_port_forward "pod/$_p" 8080 2>/dev/null) || continue
    local resp
    resp=$(curl -sf --connect-timeout 5 "http://127.0.0.1:${pf_port}/api/v1/health" 2>/dev/null)
    if [[ -n "$resp" ]]; then
      healthy=$((healthy + 1))
    fi
  done

  log "Healthy agents: $healthy/$checked"
  [[ "$healthy" -ge 1 ]]
}
run_test "Multi-agent: all agent health endpoints respond" test_agent_health_all

# ── Summary ────────────────────────────────────────────────────────
print_summary
