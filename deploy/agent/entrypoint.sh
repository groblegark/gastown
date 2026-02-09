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
#   GT_SESSION_RESUME - set to "1" to auto-resume previous Claude session on restart

set -euo pipefail

ROLE="${GT_ROLE:-unknown}"
RIG="${GT_RIG:-}"
AGENT="${GT_AGENT:-unknown}"
COMMAND="${GT_COMMAND:-claude --dangerously-skip-permissions}"
WORKSPACE="/home/agent/gt"
SESSION_RESUME="${GT_SESSION_RESUME:-1}"

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

# ── Session persistence ──────────────────────────────────────────────────
#
# Persist Claude state (~/.claude) and coop session artifacts on the
# workspace PVC so they survive pod restarts.  The PVC is already mounted
# at /home/agent/gt.  We store session state under .state/ on the PVC
# and symlink the ephemeral home-directory paths into it.
#
#   PVC layout:
#     /home/agent/gt/.state/claude/     →  symlinked from ~/.claude
#     /home/agent/gt/.state/coop/       →  symlinked from $XDG_STATE_HOME/coop

STATE_DIR="${WORKSPACE}/.state"
CLAUDE_STATE="${STATE_DIR}/claude"
COOP_STATE="${STATE_DIR}/coop"

mkdir -p "${CLAUDE_STATE}" "${COOP_STATE}"

# Symlink ~/.claude → PVC-backed directory.
CLAUDE_DIR="${HOME}/.claude"
# Remove the ephemeral dir (or stale symlink) and replace with symlink.
rm -rf "${CLAUDE_DIR}"
ln -sfn "${CLAUDE_STATE}" "${CLAUDE_DIR}"
echo "[entrypoint] Linked ${CLAUDE_DIR} → ${CLAUDE_STATE} (PVC-backed)"

# Copy credentials from staging mount into PVC dir (K8s secret mount lives
# at /tmp/claude-credentials/credentials.json, set by helm chart).
CREDS_STAGING="/tmp/claude-credentials/credentials.json"
if [ -f "${CREDS_STAGING}" ]; then
    cp "${CREDS_STAGING}" "${CLAUDE_STATE}/.credentials.json"
    echo "[entrypoint] Copied Claude credentials to PVC state dir"
fi

# Set XDG_STATE_HOME so coop writes session artifacts to the PVC.
export XDG_STATE_HOME="${STATE_DIR}"
echo "[entrypoint] XDG_STATE_HOME=${XDG_STATE_HOME}"

# ── Claude settings ──────────────────────────────────────────────────────

# Write minimal settings.json for bypass permissions (idempotent).
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

# ── Session resume detection ─────────────────────────────────────────────
#
# If this is a restart (PVC already has Claude session logs from a previous
# run), discover the most recent session and add --resume/--continue flags.

RESUME_ARGS=""
if [ "${SESSION_RESUME}" = "1" ]; then
    # Claude stores session logs at ~/.claude/projects/<hash>/*.jsonl
    LATEST_LOG=$(find "${CLAUDE_STATE}/projects" -name '*.jsonl' -type f 2>/dev/null \
        | xargs ls -t 2>/dev/null | head -1)

    if [ -n "${LATEST_LOG}" ]; then
        # Extract conversation ID from the first line's sessionId field.
        CONV_ID=$(head -1 "${LATEST_LOG}" 2>/dev/null \
            | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('sessionId',d.get('conversationId','')))" 2>/dev/null || true)

        if [ -n "${CONV_ID}" ]; then
            RESUME_ARGS="--resume ${CONV_ID}"
            echo "[entrypoint] Resuming previous session: ${CONV_ID}"
        else
            RESUME_ARGS="--continue"
            echo "[entrypoint] Resuming most recent session (no conversation ID found)"
        fi
    else
        echo "[entrypoint] No previous session logs found — starting fresh"
    fi
fi

# ── Start screen session with Claude ─────────────────────────────────────

SESSION_NAME="agent"

echo "[entrypoint] Starting screen session '${SESSION_NAME}' in ${WORKSPACE}"

# Build the full command with resume args and optional startup prompt.
# Resume args are inserted before any startup prompt.
if [ -n "${RESUME_ARGS}" ]; then
    COMMAND="${COMMAND} ${RESUME_ARGS}"
fi
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
