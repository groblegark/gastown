#!/usr/bin/env bash
# test-agent-entrypoint.sh — Verify agent pod entrypoint completed correctly.
#
# Tests:
#   1. Agent pod exists and all containers ready
#   2. Workspace directory exists (/home/agent/gt)
#   3. Beads config exists (.beads/ — daemon connected)
#   4. Claude config exists (.claude/ — hooks materialized)
#   5. Coop process is running inside agent container
#   6. Git repo initialized in workspace
#   7. Role-specific config exists (mayor/town.json, crew/, etc.)
#   8. Coop health endpoint responds
#
# Usage:
#   ./scripts/test-agent-entrypoint.sh [NAMESPACE]

MODULE_NAME="agent-entrypoint"
source "$(dirname "$0")/lib.sh"

log "Testing agent entrypoint in namespace: $E2E_NAMESPACE"

# ── Discover agent pod ─────────────────────────────────────────────────
# Look for any agent pod (crew, polecat, mayor)
AGENT_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep -E "^gt-" || true; } \
  | { grep -v "Completed\|Error\|Init" || true; } \
  | head -1 | awk '{print $1}')

if [[ -z "$AGENT_POD" ]]; then
  skip_all "No agent pod found"
  exit 0
fi

log "Agent pod: $AGENT_POD"

# Discover the container name (usually "agent")
AGENT_CONTAINER=$(kube get pod "$AGENT_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)
AGENT_CONTAINER="${AGENT_CONTAINER:-agent}"
log "Container: $AGENT_CONTAINER"

# Helper: run command inside the agent container
agent_exec() {
  kube exec "$AGENT_POD" -c "$AGENT_CONTAINER" -- "$@" 2>/dev/null
}

# ── Test 1: Agent pod all containers ready ─────────────────────────────
test_pod_ready() {
  local ready_count total_count
  ready_count=$(kube get pod "$AGENT_POD" -o jsonpath='{.status.containerStatuses[?(@.ready==true)].name}' 2>/dev/null | wc -w | tr -d ' ')
  total_count=$(kube get pod "$AGENT_POD" -o jsonpath='{.status.containerStatuses[*].name}' 2>/dev/null | wc -w | tr -d ' ')
  [[ "$total_count" -gt 0 ]] && assert_eq "$ready_count" "$total_count"
}
run_test "Agent pod containers ready (all)" test_pod_ready

# ── Test 2: Workspace directory exists ─────────────────────────────────
test_workspace_dir() {
  agent_exec test -d /home/agent/gt
}
run_test "Workspace directory exists (/home/agent/gt)" test_workspace_dir

# ── Test 3: Beads config exists (daemon connected) ─────────────────────
test_beads_config() {
  agent_exec sh -c 'test -d /home/agent/.beads || test -d /home/agent/gt/.beads'
}
run_test "Beads config exists (.beads/)" test_beads_config

# ── Test 4: Claude config exists (hooks materialized) ──────────────────
test_claude_config() {
  # .claude may be a symlink to /home/agent/gt/.state/claude
  agent_exec sh -c 'test -e /home/agent/.claude'
}
run_test "Claude config exists (~/.claude)" test_claude_config

# ── Test 5: Coop process is running ────────────────────────────────────
test_coop_running() {
  agent_exec sh -c 'ps aux 2>/dev/null | grep -q "[c]oop" || pgrep coop >/dev/null 2>&1'
}
run_test "Coop process is running" test_coop_running

# ── Test 6: Git repo initialized in workspace ──────────────────────────
test_git_init() {
  agent_exec sh -c 'test -d /home/agent/gt/.git'
}
run_test "Git repo initialized in /home/agent/gt" test_git_init

# ── Test 7: Role-specific config exists ────────────────────────────────
test_role_config() {
  # Mayor creates mayor/ dir; crew creates crew/ dir; etc.
  agent_exec sh -c '
    test -d /home/agent/gt/mayor 2>/dev/null ||
    test -d /home/agent/gt/crew 2>/dev/null ||
    test -d /home/agent/gt/polecat 2>/dev/null ||
    test -f /home/agent/gt/CLAUDE.md 2>/dev/null
  '
}
run_test "Role-specific config exists (mayor/, crew/, or CLAUDE.md)" test_role_config

# ── Test 8: Coop health endpoint responds ──────────────────────────────
test_coop_health() {
  local code
  code=$(agent_exec sh -c 'curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/api/v1/health 2>/dev/null')
  assert_eq "$code" "200"
}
run_test "Coop health endpoint responds (200)" test_coop_health

# ── Summary ───────────────────────────────────────────────────────────
print_summary
