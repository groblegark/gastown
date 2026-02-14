#!/usr/bin/env bash
# test-issue-crud.sh — Verify issue CRUD for each type: create, show, update, close.
#
# Tests creating beads of each important issue type one-by-one via the daemon,
# verifying the daemon API accepts them. This catches type validation regressions
# (e.g., "agent" type removed from core, custom types not configured).
#
# Tests:
#   Phase 1 — Built-in types:
#     1. Create + close a 'task' issue
#     2. Create + close a 'bug' issue
#     3. Create + close a 'feature' issue
#     4. Create + close an 'epic' issue
#
#   Phase 2 — Custom types (gastown-specific):
#     5. Create + close an 'agent' issue
#     6. Create + close a 'role' issue
#     7. Create + close a 'rig' issue
#
#   Phase 3 — Issue lifecycle:
#     8. Create issue → update to in_progress → verify status
#     9. Add labels to issue → verify labels
#    10. Close issue with reason → verify closed
#
# Usage:
#   ./scripts/test-issue-crud.sh [NAMESPACE]

MODULE_NAME="issue-crud"
source "$(dirname "$0")/lib.sh"

log "Testing issue CRUD in namespace: $E2E_NAMESPACE"

# ── Discover daemon pod ────────────────────────────────────────────────
DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_POD" ]]; then
  skip_all "no daemon pod found"
  exit 0
fi

# Determine which container has bd
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

# ── Helpers ────────────────────────────────────────────────────────────
bd_cmd() {
  kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- bd "$@" 2>&1
}

TEST_ID="e2e-crud-$(date +%s)"
CREATED_ISSUES=()

cleanup_issues() {
  for id in "${CREATED_ISSUES[@]}"; do
    bd_cmd close "$id" --reason="E2E cleanup" >/dev/null 2>&1 || true
  done
}
trap 'cleanup_issues; _cleanup' EXIT

# Test helper: create an issue of a given type, verify, close it
_test_create_type() {
  local issue_type="$1"
  local title="$TEST_ID $issue_type test"
  local output
  output=$(bd_cmd create \
    --title="$title" \
    --type="$issue_type" \
    --priority=4 2>&1)

  if ! echo "$output" | grep -q "Created issue"; then
    log "Create $issue_type failed: $output"
    return 1
  fi

  # Extract ID
  local issue_id
  issue_id=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$issue_id" ]] || return 1
  CREATED_ISSUES+=("$issue_id")
  log "Created $issue_type issue: $issue_id"

  # Verify it exists via show
  local show_output
  show_output=$(bd_cmd show "$issue_id" 2>&1)
  if ! echo "$show_output" | grep -qi "$issue_type"; then
    log "Show doesn't contain type $issue_type"
    # Not all show formats display type — check for the title instead
    echo "$show_output" | grep -q "$issue_type test" || return 1
  fi

  # Close it
  bd_cmd close "$issue_id" --reason="E2E $issue_type test cleanup" >/dev/null 2>&1 || true
  return 0
}

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: Built-in types
# ═══════════════════════════════════════════════════════════════════════

# ── Test 1: task ──────────────────────────────────────────────────────
test_create_task() { _test_create_type "task"; }
run_test "Create + close 'task' issue" test_create_task

# ── Test 2: bug ───────────────────────────────────────────────────────
test_create_bug() { _test_create_type "bug"; }
run_test "Create + close 'bug' issue" test_create_bug

# ── Test 3: feature ───────────────────────────────────────────────────
test_create_feature() { _test_create_type "feature"; }
run_test "Create + close 'feature' issue" test_create_feature

# ── Test 4: epic ──────────────────────────────────────────────────────
test_create_epic() { _test_create_type "epic"; }
run_test "Create + close 'epic' issue" test_create_epic

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Custom types (gastown-specific)
# ═══════════════════════════════════════════════════════════════════════

# ── Test 5: agent ─────────────────────────────────────────────────────
# Agent type requires types.custom configuration on the daemon.
# provision-namespace.sh should configure this.
test_create_agent() { _test_create_type "agent"; }
run_test "Create + close 'agent' issue (custom type)" test_create_agent

# ── Test 6: role ──────────────────────────────────────────────────────
test_create_role() { _test_create_type "role"; }
run_test "Create + close 'role' issue (custom type)" test_create_role

# ── Test 7: rig ───────────────────────────────────────────────────────
test_create_rig() { _test_create_type "rig"; }
run_test "Create + close 'rig' issue (custom type)" test_create_rig

# ═══════════════════════════════════════════════════════════════════════
# Phase 3: Issue lifecycle
# ═══════════════════════════════════════════════════════════════════════

# ── Test 8: Create → update to in_progress → verify ──────────────────
LIFECYCLE_ID=""

test_lifecycle_status() {
  local output
  output=$(bd_cmd create \
    --title="$TEST_ID lifecycle test" \
    --type=task \
    --priority=3 2>&1)

  LIFECYCLE_ID=$(echo "$output" | grep -oE '(hq|bd|gt|beads)-[a-z0-9.]+' | head -1)
  [[ -n "$LIFECYCLE_ID" ]] || return 1
  CREATED_ISSUES+=("$LIFECYCLE_ID")

  # Update to in_progress
  bd_cmd update "$LIFECYCLE_ID" --status=in_progress >/dev/null 2>&1 || return 1

  # Verify status changed
  local show_output
  show_output=$(bd_cmd show "$LIFECYCLE_ID" 2>&1)
  echo "$show_output" | grep -qi "in.progress"
}
run_test "Create → update to in_progress → verify status" test_lifecycle_status

# ── Test 9: Add labels → verify ──────────────────────────────────────
test_add_labels() {
  [[ -n "$LIFECYCLE_ID" ]] || return 1

  # Add labels
  bd_cmd update "$LIFECYCLE_ID" --add-label="e2e:test" --add-label="priority:high" >/dev/null 2>&1 || return 1

  # Verify labels
  local show_output
  show_output=$(bd_cmd show "$LIFECYCLE_ID" 2>&1)
  echo "$show_output" | grep -q "e2e:test"
}
run_test "Add labels to issue and verify" test_add_labels

# ── Test 10: Close with reason → verify closed ───────────────────────
test_close_with_reason() {
  [[ -n "$LIFECYCLE_ID" ]] || return 1

  bd_cmd close "$LIFECYCLE_ID" --reason="E2E lifecycle test complete" >/dev/null 2>&1 || return 1

  # Verify closed
  local show_output
  show_output=$(bd_cmd show "$LIFECYCLE_ID" 2>&1)
  echo "$show_output" | grep -qi "closed"
}
run_test "Close issue with reason → verify closed" test_close_with_reason

# ── Summary ──────────────────────────────────────────────────────────
print_summary
