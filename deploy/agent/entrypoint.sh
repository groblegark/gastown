#!/bin/bash
# Generic Gas Town agent entrypoint: starts a screen session with Claude.
#
# This entrypoint handles all agent roles (mayor, deacon, crew, polecat,
# witness, refinery). The controller sets role-specific env vars before
# the pod starts; this script reads GT_ROLE to configure the workspace
# and launch Claude with the correct context.
#
# Required environment variables (set by pod manager):
#   GT_ROLE       - agent role (mayor, deacon, crew, polecat, witness, refinery)
#   GT_RIG        - rig name (empty for town-level roles)
#   GT_AGENT      - agent name
#
# Optional:
#   GT_COMMAND    - command to run in screen (default: "claude --dangerously-skip-permissions")
#   BD_DAEMON_HOST - beads daemon URL
#   BD_DAEMON_PORT - beads daemon port

set -euo pipefail

ROLE="${GT_ROLE:-unknown}"
RIG="${GT_RIG:-}"
AGENT="${GT_AGENT:-unknown}"
COMMAND="${GT_COMMAND:-claude --dangerously-skip-permissions}"
WORKSPACE="/home/agent/gt"

echo "[entrypoint] Starting ${ROLE} agent: ${AGENT} (rig: ${RIG:-none})"

# ── Workspace setup ──────────────────────────────────────────────────────

# Initialize git repo in workspace if not already present.
# Persistent roles (mayor, crew, etc.) keep state across restarts via PVC.
if [ ! -d "${WORKSPACE}/.git" ]; then
    echo "[entrypoint] Initializing git repo in ${WORKSPACE}"
    cd "${WORKSPACE}"
    git init -q
    git config user.name "${GIT_AUTHOR_NAME:-${ROLE}}"
    git config user.email "${ROLE}@gastown.local"
else
    echo "[entrypoint] Git repo already exists in ${WORKSPACE}"
    cd "${WORKSPACE}"
fi

# Initialize beads if not already present and bd binary exists.
if [ ! -d "${WORKSPACE}/.beads" ] && command -v bd &>/dev/null; then
    echo "[entrypoint] Initializing beads in ${WORKSPACE}"
    bd init --non-interactive 2>/dev/null || true
fi

# ── Role-specific setup ──────────────────────────────────────────────────

case "${ROLE}" in
    mayor|deacon)
        echo "[entrypoint] Town-level singleton: ${ROLE}"
        # Mayor/deacon maintain persistent state in the PVC workspace.
        # Create role-specific working directory.
        mkdir -p "${WORKSPACE}/${ROLE}"
        ;;
    crew)
        echo "[entrypoint] Crew member: ${AGENT}"
        mkdir -p "${WORKSPACE}/crew/${AGENT}"
        ;;
    polecat)
        echo "[entrypoint] Polecat: ${AGENT} (ephemeral)"
        # Polecats use EmptyDir — no persistent state.
        ;;
    witness|refinery)
        echo "[entrypoint] Singleton: ${ROLE}"
        mkdir -p "${WORKSPACE}/${ROLE}"
        ;;
    *)
        echo "[entrypoint] WARNING: Unknown role '${ROLE}', proceeding with defaults"
        ;;
esac

# ── Read agent config from ConfigMap mount if present ────────────────────

CONFIG_DIR="/etc/agent-pod"
if [ -f "${CONFIG_DIR}/prompt" ]; then
    STARTUP_PROMPT="$(cat "${CONFIG_DIR}/prompt")"
    echo "[entrypoint] Loaded startup prompt from ConfigMap"
fi

# ── Claude settings ──────────────────────────────────────────────────────

# Create Claude settings directory
CLAUDE_DIR="${HOME}/.claude"
mkdir -p "${CLAUDE_DIR}"

# Write minimal settings.json for bypass permissions
cat > "${CLAUDE_DIR}/settings.json" <<'SETTINGS'
{
  "permissions": {
    "allow": [
      "Bash(*)",
      "Read(*)",
      "Write(*)",
      "Edit(*)",
      "Glob(*)",
      "Grep(*)",
      "WebFetch(*)",
      "WebSearch(*)"
    ],
    "deny": []
  }
}
SETTINGS

# Write CLAUDE.md with role context if not already present
if [ ! -f "${WORKSPACE}/CLAUDE.md" ]; then
    cat > "${WORKSPACE}/CLAUDE.md" <<CLAUDEMD
# Gas Town Agent: ${ROLE}

You are the **${ROLE}** agent in a Gas Town rig${RIG:+ (rig: ${RIG})}.
Agent name: ${AGENT}

Run \`gt prime\` for full context.
CLAUDEMD
fi

# ── Start screen session with Claude ─────────────────────────────────────

SESSION_NAME="agent"

echo "[entrypoint] Starting screen session '${SESSION_NAME}' in ${WORKSPACE}"

# Build the full command with optional startup prompt
if [ -n "${STARTUP_PROMPT:-}" ]; then
    FULL_COMMAND="${COMMAND} \"${STARTUP_PROMPT}\""
else
    FULL_COMMAND="${COMMAND}"
fi

# Start screen session as the agent user
cd "${WORKSPACE}"
screen -dmS "${SESSION_NAME}" bash -c "${FULL_COMMAND}"

echo "[entrypoint] Agent ${ROLE}/${AGENT} ready. Screen session: ${SESSION_NAME}"

# ── Wait for screen session to exit ──────────────────────────────────────

while true; do
    if ! screen -list "${SESSION_NAME}" 2>/dev/null | grep -q "${SESSION_NAME}"; then
        echo "[entrypoint] Screen session '${SESSION_NAME}' ended"
        break
    fi
    sleep 10
done

echo "[entrypoint] Agent container exiting"
