#!/usr/bin/env bash
# test-mail-delivery.sh — Verify mail system: create, send, check, archive.
#
# Mail is the agent-to-agent messaging system built on beads. Messages are
# stored as beads issues with metadata in labels (from:, thread:, msg-type:).
#
# Tests:
#   Phase 1 — Mail infrastructure:
#     1. Daemon has bd binary with mail support
#     2. Mail send creates a bead with mail labels
#     3. Mail list shows the sent message
#
#   Phase 2 — Mail lifecycle:
#     4. Create a mail message via bd create with mail labels
#     5. Message appears in recipient's mail list
#     6. Close (archive) the message
#     7. Archived message no longer in active list
#
# Usage:
#   ./scripts/test-mail-delivery.sh [NAMESPACE]

MODULE_NAME="mail-delivery"
source "$(dirname "$0")/lib.sh"

log "Testing mail delivery in namespace: $E2E_NAMESPACE"

# ── Discover daemon ────────────────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

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

log "Daemon pod: $DAEMON_POD (container: $DAEMON_CONTAINER)"

bd_cmd() {
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd "$@" 2>&1
}

TEST_ID="e2e-mail-$(date +%s)"
CREATED_ISSUES=()

cleanup_issues() {
  for id in "${CREATED_ISSUES[@]}"; do
    bd_cmd close "$id" --reason="E2E mail cleanup" >/dev/null 2>&1 || true
  done
}
trap 'cleanup_issues; _cleanup' EXIT

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: Mail infrastructure
# ═══════════════════════════════════════════════════════════════════════

# ── Test 1: bd has mail support ──────────────────────────────────────
HAS_GT=false

test_mail_support() {
  # Check if gt binary exists on the daemon
  if kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- which gt >/dev/null 2>&1; then
    HAS_GT=true
    log "gt binary found on daemon"
    return 0
  fi
  # Check if bd has mail-related label support
  local version
  version=$(bd_cmd version 2>/dev/null | head -1)
  log "bd version: $version (gt not available, using label-based mail)"
  [[ -n "$version" ]]
}
run_test "Daemon has bd binary (mail via labels)" test_mail_support

# ── Test 2: Create mail-like bead with mail labels ───────────────────
MAIL_BEAD_ID=""

test_create_mail_bead() {
  local output
  output=$(bd_cmd create \
    --title="$TEST_ID: test mail message" \
    --type=task \
    --priority=3 \
    --label="from:e2e-sender" \
    --label="to:e2e-receiver" \
    --label="msg-type:task" \
    --label="thread:$TEST_ID" \
    --description="E2E mail delivery test message body" 2>&1)

  if ! echo "$output" | grep -q "Created issue"; then
    log "Mail bead creation failed: $output"
    return 1
  fi

  MAIL_BEAD_ID=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$MAIL_BEAD_ID" ]] || return 1
  CREATED_ISSUES+=("$MAIL_BEAD_ID")
  log "Created mail bead: $MAIL_BEAD_ID"
}
run_test "Create bead with mail labels (from:, to:, msg-type:)" test_create_mail_bead

# ── Test 3: Mail bead has correct labels ─────────────────────────────
test_mail_labels() {
  [[ -n "$MAIL_BEAD_ID" ]] || return 1

  local show_output
  show_output=$(bd_cmd show "$MAIL_BEAD_ID" 2>&1)

  # Verify mail labels are present
  if ! echo "$show_output" | grep -q "from:e2e-sender"; then
    log "Missing from: label"
    return 1
  fi
  if ! echo "$show_output" | grep -q "to:e2e-receiver"; then
    log "Missing to: label"
    return 1
  fi
  if ! echo "$show_output" | grep -q "msg-type:task"; then
    log "Missing msg-type: label"
    return 1
  fi
  log "All mail labels present"
}
run_test "Mail bead has correct labels" test_mail_labels

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Mail search & lifecycle
# ═══════════════════════════════════════════════════════════════════════

# ── Test 4: Search by mail labels finds the message ──────────────────
test_search_by_label() {
  [[ -n "$MAIL_BEAD_ID" ]] || return 1

  # List issues filtered by the thread label
  local list_output
  list_output=$(bd_cmd list --label="thread:$TEST_ID" 2>&1)
  echo "$list_output" | grep -q "$MAIL_BEAD_ID"
}
run_test "Search by thread label finds mail bead" test_search_by_label

# ── Test 5: Search by recipient label ────────────────────────────────
test_search_by_recipient() {
  [[ -n "$MAIL_BEAD_ID" ]] || return 1

  local list_output
  list_output=$(bd_cmd list --label="to:e2e-receiver" 2>&1)
  echo "$list_output" | grep -q "$MAIL_BEAD_ID"
}
run_test "Search by to: label finds mail bead" test_search_by_recipient

# ── Test 6: Close (archive) the mail bead ────────────────────────────
test_archive_mail() {
  [[ -n "$MAIL_BEAD_ID" ]] || return 1

  bd_cmd close "$MAIL_BEAD_ID" --reason="E2E mail archived" >/dev/null 2>&1 || return 1
  log "Archived mail bead: $MAIL_BEAD_ID"

  # Verify closed
  local show_output
  show_output=$(bd_cmd show "$MAIL_BEAD_ID" 2>&1)
  echo "$show_output" | grep -qi "closed"
}
run_test "Close (archive) mail bead" test_archive_mail

# ── Test 7: Archived mail not in active list ─────────────────────────
test_archived_not_active() {
  [[ -n "$MAIL_BEAD_ID" ]] || return 1

  # List open issues with the thread label — should NOT find our closed bead
  local list_output
  list_output=$(bd_cmd list --status=open --label="thread:$TEST_ID" 2>&1)

  if echo "$list_output" | grep -q "$MAIL_BEAD_ID"; then
    log "Archived mail still appears in open list!"
    return 1
  fi
  log "Archived mail correctly excluded from open list"
  return 0
}
run_test "Archived mail not in active (open) list" test_archived_not_active

# ── Summary ──────────────────────────────────────────────────────────
print_summary
