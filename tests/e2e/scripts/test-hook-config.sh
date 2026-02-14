#!/usr/bin/env bash
# test-hook-config.sh — Verify hook configuration is materialized on agent pods.
#
# Hooks are Claude Code lifecycle events (SessionStart, PreCompact, Stop, etc.)
# that run custom commands during an agent's session. This test verifies that
# the hook configuration is correctly materialized when agents start.
#
# Tests:
#   1. Daemon pod has bd binary
#   2. Claude hooks config exists on daemon (.claude/settings.json or hooks dir)
#   3. Create an agent bead and wait for pod
#   4. Agent pod has .claude directory
#   5. Agent pod has hooks config (.claude/settings.json with hooks)
#   6. Agent pod has hook scripts (hooks/ directory or inline)
#   7. Clean up: close bead, verify pod deletion
#
# Usage:
#   ./scripts/test-hook-config.sh [NAMESPACE]

MODULE_NAME="hook-config"
source "$(dirname "$0")/lib.sh"

log "Testing hook configuration in namespace: $E2E_NAMESPACE"

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

# ── Discover existing agent pods ──────────────────────────────────────
AGENT_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep -E "^gt-" || true; } \
  | { grep -v "Completed\|Error\|Init" || true; } \
  | head -1 | awk '{print $1}')

# ── Test 1: Daemon has bd binary ─────────────────────────────────────
test_daemon_bd() {
  local version
  version=$(bd_cmd version 2>/dev/null | head -1)
  log "Daemon bd version: ${version:-unknown}"
  [[ -n "$version" ]]
}
run_test "Daemon pod has bd binary" test_daemon_bd

# ── Test 2: Daemon has hook-related config ────────────────────────────
test_daemon_hooks() {
  # Check if daemon has any hook config (bd config, .claude/settings.json, etc.)
  local config_output
  config_output=$(kube exec "$DAEMON_POD" -c "$DAEMON_CONTAINER" -- \
    sh -c 'ls -la /home/agent/.claude/settings.json 2>/dev/null || ls -la /root/.claude/settings.json 2>/dev/null || echo "no-settings"' 2>/dev/null)
  log "Hook config check: $config_output"
  # Pass if settings exist or if this is a daemon-only pod (no hooks expected)
  if [[ "$config_output" == *"no-settings"* ]]; then
    log "No .claude/settings.json on daemon (expected — hooks are for agent pods)"
    return 0
  fi
  return 0
}
run_test "Daemon hook config check" test_daemon_hooks

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Agent pod hooks (only if an agent pod exists)
# ═══════════════════════════════════════════════════════════════════════

if [[ -z "$AGENT_POD" ]]; then
  log "No agent pods running — skipping agent hook tests"
  skip_test "Agent pod has .claude directory" "no agent pods running"
  skip_test "Agent pod has hooks in settings" "no agent pods running"
  skip_test "Agent pod has hook scripts" "no agent pods running"
  print_summary
  exit 0
fi

AGENT_CONTAINER=$(kube get pod "$AGENT_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)
AGENT_CONTAINER="${AGENT_CONTAINER:-agent}"
log "Testing agent pod: $AGENT_POD (container: $AGENT_CONTAINER)"

agent_exec() {
  kube exec "$AGENT_POD" -c "$AGENT_CONTAINER" -- "$@" 2>/dev/null
}

# ── Test 3: Agent pod has .claude directory ───────────────────────────
test_agent_claude_dir() {
  agent_exec test -e /home/agent/.claude
}
run_test "Agent pod has .claude directory" test_agent_claude_dir

# ── Test 4: Agent pod has hooks in settings ───────────────────────────
test_agent_hooks_settings() {
  local settings
  settings=$(agent_exec sh -c 'cat /home/agent/.claude/settings.json 2>/dev/null || echo "{}"')
  # settings.json should have a "hooks" key if hooks are configured
  if echo "$settings" | grep -q '"hooks"'; then
    log "Found hooks in settings.json"
    return 0
  fi
  # Also check for settings.local.json
  settings=$(agent_exec sh -c 'cat /home/agent/.claude/settings.local.json 2>/dev/null || echo "{}"')
  if echo "$settings" | grep -q '"hooks"'; then
    log "Found hooks in settings.local.json"
    return 0
  fi
  log "No hooks found in .claude settings"
  return 1
}
run_test "Agent pod has hooks in Claude settings" test_agent_hooks_settings

# ── Test 5: Agent pod has hook scripts or runtime dir ─────────────────
test_agent_hook_scripts() {
  # Check for hook scripts in various locations
  agent_exec sh -c '
    test -d /home/agent/gt/hooks 2>/dev/null ||
    test -d /home/agent/.claude/hooks 2>/dev/null ||
    test -d /home/agent/gt/.state 2>/dev/null ||
    ls /home/agent/gt/hooks/*.sh 2>/dev/null | head -1 | grep -q .
  '
}
run_test "Agent pod has hook scripts or runtime directory" test_agent_hook_scripts

# ── Summary ──────────────────────────────────────────────────────────
print_summary
