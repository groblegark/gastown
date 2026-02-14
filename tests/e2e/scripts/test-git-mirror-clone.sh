#!/usr/bin/env bash
# test-git-mirror-clone.sh — Verify agent can clone from git mirror.
#
# Tests:
#   1. Git clone from mirror service succeeds (via kubectl exec)
#   2. Cloned repo has valid HEAD
#   3. Partial clone with --depth 1 works
#   4. Clone from agent pod perspective works
#
# Note: git-mirror is optional — if not deployed, all tests are skipped.
#
# Usage:
#   ./scripts/test-git-mirror-clone.sh [NAMESPACE]

MODULE_NAME="git-mirror-clone"
source "$(dirname "$0")/lib.sh"

log "Testing git mirror clone in namespace: $E2E_NAMESPACE"

# ── Discover git mirror ────────────────────────────────────────────────
MIRROR_POD=$(kube get pods --no-headers 2>/dev/null | { grep "git-mirror" || true; } | head -1 | awk '{print $1}')
MIRROR_SVC=$(kube get svc --no-headers 2>/dev/null | { grep "git-mirror" || true; } | head -1 | awk '{print $1}')

log "Mirror pod: ${MIRROR_POD:-none}"
log "Mirror svc: ${MIRROR_SVC:-none}"

if [[ -z "$MIRROR_POD" ]]; then
  skip_all "git-mirror not deployed in this namespace"
  exit 0
fi

# ── Discover repo name ─────────────────────────────────────────────────
REPO_NAME=""
REPO_NAME=$(kube exec "$MIRROR_POD" -- sh -c 'for d in /data/*; do if [ -d "$d" ]; then basename "$d"; break; fi; done' 2>/dev/null)

if [[ -z "$REPO_NAME" ]]; then
  skip_all "No repositories found in git mirror"
  exit 0
fi

log "First repo: $REPO_NAME"

# Build the git:// clone URL using the service name
MIRROR_GIT_URL="git://${MIRROR_SVC}.${E2E_NAMESPACE}.svc.cluster.local:9418/${REPO_NAME}"
log "Clone URL: $MIRROR_GIT_URL"

# ── Discover agent pod for exec ────────────────────────────────────────
AGENT_POD=$(kube get pods --no-headers 2>/dev/null \
  | grep -E "^gt-" \
  | grep -v "Completed\|Error\|Init" \
  | head -1 | awk '{print $1}')

# Use agent pod if available, otherwise fall back to mirror pod itself
EXEC_POD="${AGENT_POD:-$MIRROR_POD}"
EXEC_CONTAINER=""
if [[ -n "$AGENT_POD" ]]; then
  # Agent pods have a single "agent" container
  EXEC_CONTAINER="-c agent"
fi

log "Exec pod: $EXEC_POD ${EXEC_CONTAINER:+(container: agent)}"

# Helper: exec into the test pod
test_exec() {
  kube exec "$EXEC_POD" $EXEC_CONTAINER -- "$@" 2>/dev/null
}

# ── Test 1: Git clone from mirror succeeds ─────────────────────────────
CLONE_DIR="/tmp/e2e-clone-test-$$"

test_git_clone() {
  test_exec sh -c "rm -rf $CLONE_DIR && git clone $MIRROR_GIT_URL $CLONE_DIR"
}
run_test "Git clone from mirror succeeds" test_git_clone

# ── Test 2: Cloned repo has valid HEAD ─────────────────────────────────
test_valid_head() {
  local head
  head=$(test_exec sh -c "cat $CLONE_DIR/.git/HEAD 2>/dev/null || git -C $CLONE_DIR rev-parse HEAD 2>/dev/null")
  [[ -n "$head" ]] && (assert_contains "$head" "ref:" || assert_match "$head" "^[0-9a-f]{7,}")
}
run_test "Cloned repo has valid HEAD" test_valid_head

# ── Test 3: Cloned repo has commits ───────────────────────────────────
test_has_commits() {
  local count
  count=$(test_exec sh -c "git -C $CLONE_DIR rev-list --count HEAD 2>/dev/null")
  [[ -n "$count" ]] && assert_gt "$count" 0
}
run_test "Cloned repo has commits" test_has_commits

# ── Test 4: Partial clone (--depth 1) works ────────────────────────────
SHALLOW_DIR="/tmp/e2e-shallow-test-$$"

test_shallow_clone() {
  test_exec sh -c "rm -rf $SHALLOW_DIR && git clone --depth 1 $MIRROR_GIT_URL $SHALLOW_DIR"
}
run_test "Shallow clone (--depth 1) works" test_shallow_clone

# ── Test 5: Shallow clone has exactly 1 commit ────────────────────────
test_shallow_depth() {
  local count
  count=$(test_exec sh -c "git -C $SHALLOW_DIR rev-list --count HEAD 2>/dev/null")
  assert_eq "$count" "1"
}
run_test "Shallow clone has exactly 1 commit" test_shallow_depth

# ── Test 6: Clone from agent pod (if different from mirror pod) ────────
if [[ -n "$AGENT_POD" && "$AGENT_POD" != "$MIRROR_POD" ]]; then
  AGENT_CLONE_DIR="/tmp/e2e-agent-clone-$$"

  test_agent_clone() {
    kube exec "$AGENT_POD" -c agent -- sh -c "rm -rf $AGENT_CLONE_DIR && git clone $MIRROR_GIT_URL $AGENT_CLONE_DIR" 2>/dev/null
  }
  run_test "Agent pod can clone from mirror" test_agent_clone

  # Cleanup agent clone
  kube exec "$AGENT_POD" -c agent -- rm -rf "$AGENT_CLONE_DIR" 2>/dev/null || true
else
  skip_test "Agent pod can clone from mirror" "no separate agent pod available"
fi

# ── Cleanup ───────────────────────────────────────────────────────────
test_exec sh -c "rm -rf $CLONE_DIR $SHALLOW_DIR" 2>/dev/null || true

# ── Summary ───────────────────────────────────────────────────────────
print_summary
