#!/bin/bash
# Agent container entrypoint: role-aware startup for all Gas Town agent roles.
#
# Required env:
#   GT_ROLE   - agent role: polecat, crew, mayor, deacon, witness, refinery
#   GT_RIG    - rig name
#   GT_AGENT  - agent name
#
# Optional:
#   GT_COMMAND     - override command (default: claude --dangerously-skip-permissions)
#   GT_SESSION     - session name (default: "claude")
#   GT_TOWN_ROOT   - town workspace root
#   GT_WORKSPACE   - workspace path override

set -euo pipefail

ROLE="${GT_ROLE:?GT_ROLE is required}"
RIG="${GT_RIG:?GT_RIG is required}"
AGENT="${GT_AGENT:-unknown}"
SESSION_NAME="${GT_SESSION:-claude}"
WORKSPACE="${GT_WORKSPACE:-/home/agent/gt}"

echo "[entrypoint] Starting ${ROLE} agent: ${AGENT} in rig ${RIG}"

# --- Workspace setup ---
setup_workspace() {
    mkdir -p "${WORKSPACE}"

    # If GT_TOWN_ROOT is set, use it as the working directory
    if [ -n "${GT_TOWN_ROOT:-}" ]; then
        mkdir -p "${GT_TOWN_ROOT}"
        cd "${GT_TOWN_ROOT}"
    else
        cd "${WORKSPACE}"
    fi

    # Create role-specific directory structure
    case "${ROLE}" in
        mayor|deacon)
            mkdir -p "${WORKSPACE}/${ROLE}"
            # Write CLAUDE.md if not present and configmap mounted
            if [ -f "/etc/agent-pod/CLAUDE.md" ] && [ ! -f "${WORKSPACE}/${ROLE}/CLAUDE.md" ]; then
                cp /etc/agent-pod/CLAUDE.md "${WORKSPACE}/${ROLE}/CLAUDE.md"
            fi
            ;;
        crew)
            mkdir -p "${WORKSPACE}/crew/${AGENT}"
            ;;
        polecat)
            # Polecats use ephemeral workspace, no special setup
            ;;
    esac
}

# --- Claude settings ---
setup_claude_settings() {
    local settings_dir="${HOME}/.claude"
    mkdir -p "${settings_dir}"

    # Only write settings if not already present
    if [ ! -f "${settings_dir}/settings.json" ]; then
        cat > "${settings_dir}/settings.json" << 'SETTINGS'
{
  "permissions": {
    "allow": [
      "Bash(*)",
      "Read(*)",
      "Write(*)",
      "Edit(*)",
      "Glob(*)",
      "Grep(*)"
    ]
  }
}
SETTINGS
    fi
}

# --- Start agent ---
start_agent() {
    local cmd="${GT_COMMAND:-claude --dangerously-skip-permissions}"

    case "${ROLE}" in
        mayor)
            echo "[entrypoint] Starting mayor with command: ${cmd}"
            ;;
        deacon)
            echo "[entrypoint] Starting deacon with command: ${cmd}"
            ;;
        crew)
            echo "[entrypoint] Starting crew agent with command: ${cmd}"
            ;;
        polecat)
            echo "[entrypoint] Starting polecat with command: ${cmd}"
            ;;
        witness|refinery)
            echo "[entrypoint] Starting ${ROLE} with command: ${cmd}"
            ;;
        *)
            echo "[entrypoint] WARNING: Unknown role '${ROLE}', using default command"
            ;;
    esac

    # exec replaces this process — agent becomes PID 1
    echo "[entrypoint] Agent ready. Role: ${ROLE}, Rig: ${RIG}, Agent: ${AGENT}"
    exec ${cmd}
}

# --- Main ---
setup_workspace
setup_claude_settings
start_agent
