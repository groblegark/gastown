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
WORKSPACE="/home/agent/gt"
SESSION_RESUME="${GT_SESSION_RESUME:-1}"

# Export platform version for bd/gt version commands
if [ -f /etc/platform-version ]; then
    export BD_PLATFORM_VERSION
    BD_PLATFORM_VERSION=$(cat /etc/platform-version)
fi

echo "[entrypoint] Starting ${ROLE} agent: ${AGENT} (rig: ${RIG:-none})"

# ── Workspace setup ──────────────────────────────────────────────────────

# Set global git config FIRST so safe.directory is set before any repo ops.
# The workspace volume mount is owned by root (EmptyDir/PVC) but we run as
# UID 1000 — git's dubious-ownership check would block all operations without this.
git config --global user.name "${GIT_AUTHOR_NAME:-${ROLE}}"
git config --global user.email "${ROLE}@gastown.local"
git config --global --add safe.directory '*'

# ── Git credentials ────────────────────────────────────────────────────
# If GIT_USERNAME and GIT_TOKEN are set (from ExternalSecret), configure
# git credential-store so clone/push to github.com works automatically.
if [ -n "${GIT_USERNAME:-}" ] && [ -n "${GIT_TOKEN:-}" ]; then
    CRED_FILE="${HOME}/.git-credentials"
    echo "https://${GIT_USERNAME}:${GIT_TOKEN}@github.com" > "${CRED_FILE}"
    chmod 600 "${CRED_FILE}"
    git config --global credential.helper "store --file=${CRED_FILE}"
    echo "[entrypoint] Git credentials configured for ${GIT_USERNAME}@github.com"
fi

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

# ── Gas Town workspace structure ───────────────────────────────────────
#
# gt prime detects the agent role from directory structure.
# The minimal required layout for a town-level workspace:
#
#   /home/agent/gt/              ← town root (WORKSPACE)
#   ├── mayor/town.json          ← primary workspace marker
#   ├── mayor/rigs.json          ← rig registry
#   ├── CLAUDE.md                ← town root identity anchor
#   └── .beads/config.yaml       ← daemon connection config

TOWN_NAME="${GT_TOWN_NAME:-town}"

# Create workspace marker (idempotent — skip if already exists on PVC).
if [ ! -f "${WORKSPACE}/mayor/town.json" ]; then
    echo "[entrypoint] Creating Gas Town workspace structure"
    mkdir -p "${WORKSPACE}/mayor"
    cat > "${WORKSPACE}/mayor/town.json" <<TOWNJSON
{"type":"town","version":2,"name":"${TOWN_NAME}","created_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
TOWNJSON
    cat > "${WORKSPACE}/mayor/rigs.json" <<RIGSJSON
{"version":1,"rigs":{}}
RIGSJSON
fi

# Create role-specific directories.
case "${ROLE}" in
    mayor|deacon)
        echo "[entrypoint] Town-level singleton: ${ROLE}"
        mkdir -p "${WORKSPACE}/${ROLE}"
        ;;
    crew)
        echo "[entrypoint] Crew member: ${AGENT}"
        mkdir -p "${WORKSPACE}/crew/${AGENT}"
        ;;
    polecat)
        echo "[entrypoint] Polecat: ${AGENT} (ephemeral)"
        ;;
    witness|refinery)
        echo "[entrypoint] Singleton: ${ROLE}"
        mkdir -p "${WORKSPACE}/${ROLE}"
        ;;
    *)
        echo "[entrypoint] WARNING: Unknown role '${ROLE}', proceeding with defaults"
        ;;
esac

# ── Rig-aware git setup ────────────────────────────────────────────────
#
# The controller sets GT_RIGS="name=giturl:prefix,name2=giturl2:prefix2"
# with rig metadata from daemon rig beads. The init container (if present)
# clones the agent's rig repo into {workspace}/{rig}/work/.
#
# This section:
#   1. Populates rigs.json from GT_RIGS so gt rig commands work
#   2. Creates rig directory structure for the agent's own rig
#   3. Verifies the init-clone wrote code to the expected location

if [ -n "${GT_RIGS:-}" ]; then
    echo "[entrypoint] Registering rigs from GT_RIGS"
    RIGS_JSON="${WORKSPACE}/mayor/rigs.json"

    # Build rigs.json entries from GT_RIGS env var.
    # Format: "rigname=https://github.com/org/repo.git:prefix,..."
    RIGS_OBJ="{}"
    IFS=',' read -ra RIG_ENTRIES <<< "${GT_RIGS}"
    for entry in "${RIG_ENTRIES[@]}"; do
        # Parse "name=url:prefix" — split on LAST colon since URL contains "://"
        rig_name="${entry%%=*}"
        url_prefix="${entry#*=}"
        rig_url="${url_prefix%:*}"
        rig_prefix="${url_prefix##*:}"

        if [ -z "${rig_name}" ] || [ -z "${rig_url}" ]; then
            continue
        fi

        NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
        RIGS_OBJ=$(echo "${RIGS_OBJ}" | jq \
            --arg name "${rig_name}" \
            --arg url "${rig_url}" \
            --arg prefix "${rig_prefix}" \
            --arg now "${NOW}" \
            '.[$name] = {git_url: $url, added_at: $now, beads: {repo: "", prefix: $prefix}}')

        echo "[entrypoint]   Registered rig: ${rig_name} (${rig_url}, prefix=${rig_prefix})"
    done

    # Write complete rigs.json
    echo "{\"version\":1,\"rigs\":${RIGS_OBJ}}" | jq . > "${RIGS_JSON}"
fi

# Verify init-clone result for code-needing roles.
if [ -n "${RIG}" ]; then
    CLONE_DIR="${WORKSPACE}/${RIG}/work"
    if [ -d "${CLONE_DIR}/.git" ]; then
        echo "[entrypoint] Rig repo found at ${CLONE_DIR}"

        # Create rig directory structure that gt prime expects.
        # For crew: {workspace}/{rig}/crew/{agent}/ symlinked to clone
        # For polecat: work directly in clone dir
        # For witness/refinery: work in clone dir
        case "${ROLE}" in
            crew)
                CREW_DIR="${WORKSPACE}/${RIG}/crew/${AGENT}"
                if [ ! -d "${CREW_DIR}" ]; then
                    mkdir -p "$(dirname "${CREW_DIR}")"
                    ln -sfn "${CLONE_DIR}" "${CREW_DIR}"
                    echo "[entrypoint] Linked crew workspace: ${CREW_DIR} → ${CLONE_DIR}"
                fi
                ;;
            polecat)
                # Install post-commit hook for early-push pattern.
                # In K8s there's no shared bare repo — branches must be pushed
                # to origin so refinery/witness can discover them via git fetch.
                HOOKS_DIR="${CLONE_DIR}/.git/hooks"
                mkdir -p "${HOOKS_DIR}"
                cat > "${HOOKS_DIR}/post-commit" <<'HOOK'
#!/bin/sh
# Auto-push polecat branch after each commit so refinery can see progress.
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
if [ -n "${branch}" ] && [ "${branch}" != "HEAD" ] && [ "${branch}" != "main" ]; then
    git push origin "${branch}" --force-with-lease 2>/dev/null &
fi
HOOK
                chmod +x "${HOOKS_DIR}/post-commit"
                echo "[entrypoint] Installed post-commit auto-push hook for polecat"
                ;;
        esac
    elif [ "${ROLE}" = "polecat" ] || [ "${ROLE}" = "crew" ] || [ "${ROLE}" = "refinery" ]; then
        echo "[entrypoint] WARNING: No git clone found at ${CLONE_DIR} (init container may not have run)"
    fi
fi

# ── Daemon connection via gt connect ────────────────────────────────────
#
# If BD_DAEMON_HOST is set and .beads/config.yaml doesn't exist yet,
# use gt connect --url to persist the daemon connection config.
# This lets bd and gt commands talk to the remote daemon.

if [ -n "${BD_DAEMON_HOST:-}" ]; then
    # Use the HTTP port for daemon connection (bd CLI auto-detects HTTP URLs).
    DAEMON_HTTP_PORT="${BD_DAEMON_HTTP_PORT:-9080}"
    DAEMON_URL="http://${BD_DAEMON_HOST}:${DAEMON_HTTP_PORT}"
    echo "[entrypoint] Connecting to daemon at ${DAEMON_URL}"
    # gt connect needs to be in the workspace dir
    cd "${WORKSPACE}"
    gt connect --url "${DAEMON_URL}" --token "${BD_DAEMON_TOKEN:-}" 2>&1 || {
        echo "[entrypoint] WARNING: gt connect failed, creating config manually"
        mkdir -p "${WORKSPACE}/.beads"
        cat > "${WORKSPACE}/.beads/config.yaml" <<BEADSCFG
daemon-host: "${DAEMON_URL}"
daemon-token: "${BD_DAEMON_TOKEN:-}"
BEADSCFG
    }
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

# Persist ~/.claude on PVC.
CLAUDE_DIR="${HOME}/.claude"
# If ~/.claude is a mount point (subPath mount from controller, bd-48ary),
# it's already PVC-backed — skip the symlink dance.  Otherwise, replace
# the ephemeral dir/stale symlink with a symlink to the PVC directory.
if mountpoint -q "${CLAUDE_DIR}" 2>/dev/null; then
    echo "[entrypoint] ${CLAUDE_DIR} is a mount point (subPath) — already PVC-backed"
else
    rm -rf "${CLAUDE_DIR}"
    ln -sfn "${CLAUDE_STATE}" "${CLAUDE_DIR}"
    echo "[entrypoint] Linked ${CLAUDE_DIR} → ${CLAUDE_STATE} (PVC-backed)"
fi

# Seed credentials from K8s secret mount if PVC doesn't have them yet.
# IMPORTANT: Don't overwrite PVC credentials on restart — the refresh loop
# rotates refresh tokens, so the PVC copy is newer than the K8s secret.
CREDS_STAGING="/tmp/claude-credentials/credentials.json"
CREDS_PVC="${CLAUDE_STATE}/.credentials.json"
if [ -f "${CREDS_STAGING}" ] && [ ! -f "${CREDS_PVC}" ]; then
    cp "${CREDS_STAGING}" "${CREDS_PVC}"
    echo "[entrypoint] Seeded Claude credentials from K8s secret"
elif [ -f "${CREDS_PVC}" ]; then
    echo "[entrypoint] Using existing PVC credentials (preserved from refresh)"
fi

# Set XDG_STATE_HOME so coop writes session artifacts to the PVC.
export XDG_STATE_HOME="${STATE_DIR}"
echo "[entrypoint] XDG_STATE_HOME=${XDG_STATE_HOME}"

# ── Dev tools PATH ─────────────────────────────────────────────────────
#
# The agent image includes a full dev toolchain (Go, Python, Rust Analyzer,
# kubectl, AWS CLI, etc.) installed directly. Add Go bin to PATH.

if [ -d "/usr/local/go/bin" ]; then
    export PATH="/usr/local/go/bin:${PATH}"
    echo "[entrypoint] Added /usr/local/go/bin to PATH"
fi

# ── Claude settings ──────────────────────────────────────────────────────
#
# User-level settings (permissions + LSP plugins) written to ~/.claude/settings.json.
# LSP plugins are always enabled — gopls and rust-analyzer are built into the image.
# Hooks come from config bead materialization if available, otherwise static.

# Start with base settings JSON (permissions + LSP plugins).
SETTINGS_JSON='{"permissions":{"allow":["Bash(*)","Read(*)","Write(*)","Edit(*)","Glob(*)","Grep(*)","WebFetch(*)","WebSearch(*)"],"deny":[]}}'

# Enable LSP plugins (gopls + rust-analyzer are always present in the agent image).
PLUGINS_JSON=""
if command -v gopls &>/dev/null; then
    PLUGINS_JSON="${PLUGINS_JSON}\"gopls-lsp@claude-plugins-official\":true,"
    echo "[entrypoint] Enabling gopls LSP plugin"
fi
if command -v rust-analyzer &>/dev/null; then
    PLUGINS_JSON="${PLUGINS_JSON}\"rust-analyzer-lsp@claude-plugins-official\":true,"
    echo "[entrypoint] Enabling rust-analyzer LSP plugin"
fi

if [ -n "${PLUGINS_JSON}" ]; then
    PLUGINS_JSON="{${PLUGINS_JSON%,}}"
    SETTINGS_JSON=$(echo "${SETTINGS_JSON}" | jq --argjson p "${PLUGINS_JSON}" '. + {enabledPlugins: $p}')
fi

echo "${SETTINGS_JSON}" | jq . > "${CLAUDE_DIR}/settings.json"

# Try config bead materialization (writes to workspace .claude/settings.json).
# This queries the daemon for claude-hooks config beads and merges them by
# specificity (global → role → agent). Falls back to static hooks if no
# config beads exist or daemon is unreachable.
MATERIALIZE_SCOPE="${GT_TOWN_NAME:-town}/${GT_RIG:-}/${ROLE}/${AGENT}"
MATERIALIZED=0

if command -v gt &>/dev/null; then
    echo "[entrypoint] Materializing hooks from config beads (scope: ${MATERIALIZE_SCOPE})"
    cd "${WORKSPACE}"
    if gt config materialize --hooks --scope="${MATERIALIZE_SCOPE}" 2>&1; then
        # Verify the file was written with actual hooks content
        if grep -q '"hooks"' "${WORKSPACE}/.claude/settings.json" 2>/dev/null; then
            MATERIALIZED=1
            echo "[entrypoint] Hooks materialized from config beads"
        fi
    fi
fi

if [ "${MATERIALIZED}" = "0" ]; then
    echo "[entrypoint] No config beads found, writing static hooks"
    # Write project-level settings with hooks to workspace .claude/settings.json.
    # These must match the canonical templates in internal/claude/config/.
    # Interactive roles (mayor, crew) check mail on UserPromptSubmit.
    # Autonomous roles (polecat, witness, refinery, deacon) check mail on SessionStart.
    mkdir -p "${WORKSPACE}/.claude"

    case "${ROLE}" in
        polecat|witness|refinery|deacon|boot)
            HOOK_TYPE="autonomous"
            ;;
        *)
            HOOK_TYPE="interactive"
            ;;
    esac

    if [ "${HOOK_TYPE}" = "autonomous" ]; then
        cat > "${WORKSPACE}/.claude/settings.json" <<'HOOKS'
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && gt prime --hook && (case \"$GT_ROLE\" in polecat|witness|refinery|deacon|boot) gt mail check --inject;; esac || true) && (gt nudge deacon session-started || true)"
          }
        ]
      }
    ],
    "PreCompact": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && gt prime --hook"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && _stdin=$(cat) && (gt decision auto-close --inject || true) && (gt mail check --inject || true) && (echo \"$_stdin\" | bd decision check --inject || true) && (echo \"$_stdin\" | gt decision turn-clear || true)"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && (gt inject drain --quiet || true) && (gt nudge drain --quiet || true)"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && _stdin=$(cat) && echo \"$_stdin\" | bd bus emit --hook=Stop"
          }
        ]
      }
    ]
  }
}
HOOKS
    else
        cat > "${WORKSPACE}/.claude/settings.json" <<'HOOKS'
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && gt prime --hook && (case \"$GT_ROLE\" in polecat|witness|refinery|deacon|boot) gt mail check --inject;; esac || true) && (gt nudge deacon session-started || true)"
          }
        ]
      }
    ],
    "PreCompact": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && gt prime --hook"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && _stdin=$(cat) && (gt decision auto-close --inject || true) && (gt mail check --inject || true) && (echo \"$_stdin\" | bd decision check --inject || true) && (echo \"$_stdin\" | gt decision turn-clear || true)"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && (gt inject drain --quiet || true) && (gt nudge drain --quiet || true)"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "export PATH=\"$HOME/.local/bin:$HOME/go/bin:$PATH\" && _stdin=$(cat) && echo \"$_stdin\" | bd bus emit --hook=Stop"
          }
        ]
      }
    ]
  }
}
HOOKS
    fi
fi

# Write CLAUDE.md with role-specific context if not already present.
# This is the static identity anchor — gt prime (via SessionStart hook) adds
# dynamic context (hooked work, advice, mail) on top of this.
if [ ! -f "${WORKSPACE}/CLAUDE.md" ]; then
    case "${ROLE}" in
        polecat)
            cat > "${WORKSPACE}/CLAUDE.md" <<CLAUDEMD
# Polecat Context

> **Recovery**: Run \`gt prime\` after compaction, clear, or new session

## Your Role: POLECAT (Worker: ${AGENT} in ${RIG:-unknown})

You are polecat **${AGENT}** — a worker agent in the ${RIG:-unknown} rig.
You work on assigned issues and submit completed work to the merge queue.

## Polecat Lifecycle (EPHEMERAL)

\`\`\`
SPAWN → WORK → gt done → DEATH
\`\`\`

**Key insight**: You are born with work. You do ONE task. Then you die.
There is no "next assignment." When \`gt done\` runs, you cease to exist.

## Key Commands

### Session & Context
- \`gt prime\` — Load full context after compaction/clear/new session
- \`gt hook\` — Check your hooked molecule (primary work source)

### Your Work
- \`bd show <issue>\` — View specific issue details
- \`bd ready\` — See your workflow steps

### Progress
- \`bd update <id> --status=in_progress\` — Claim work
- \`bd close <step-id>\` — Mark molecule STEP complete (NOT your main issue!)

### Completion
- \`gt done\` — Signal work ready for merge queue

## Work Protocol

Your work follows the **mol-polecat-work** molecule.

**FIRST: Check your steps with \`bd ready\`.** Do NOT use Claude's internal task tools.

\`\`\`bash
bd ready                   # See your workflow steps — DO THIS FIRST
# ... work on current step ...
bd close <step-id>         # Mark step complete
bd ready                   # See next step
\`\`\`

When all steps are done, run \`gt done\`.

## Communication

\`\`\`bash
# To your Witness
gt mail send ${RIG:-unknown}/witness -s "Question" -m "..."

# To the Mayor (cross-rig issues)
gt mail send mayor/ -s "Need coordination" -m "..."
\`\`\`

---
Polecat: ${AGENT} | Rig: ${RIG:-unknown} | Working directory: ${WORKSPACE}
CLAUDEMD
            ;;
        mayor)
            cat > "${WORKSPACE}/CLAUDE.md" <<CLAUDEMD
# Mayor Context

> **Recovery**: Run \`gt prime\` after compaction, clear, or new session

Full context is injected by \`gt prime\` at session start.

## Quick Reference

- Check mail: \`gt mail inbox\`
- Check rigs: \`gt rig list\`
- Start patrol: \`gt patrol start\`
CLAUDEMD
            ;;
        *)
            cat > "${WORKSPACE}/CLAUDE.md" <<CLAUDEMD
# Gas Town Agent: ${ROLE}

> **Recovery**: Run \`gt prime\` after compaction, clear, or new session

You are the **${ROLE}** agent in a Gas Town rig${RIG:+ (rig: ${RIG})}.
Agent name: ${AGENT}

Full context is injected by \`gt prime\` at session start.

## Quick Reference

- \`gt prime\` — Load full context
- \`gt hook\` — Check hooked work
- \`gt mail inbox\` — Check messages
CLAUDEMD
            ;;
    esac
fi

# ── Append dev tools section to CLAUDE.md ──────────────────────────────
# Guard: only append once (prevents duplication on pod restarts)
if ! grep -q "## Development Tools" "${WORKSPACE}/CLAUDE.md" 2>/dev/null; then
    cat >> "${WORKSPACE}/CLAUDE.md" <<'DEVTOOLS'

## Development Tools

All tools are installed directly in the agent image — use them from the command line.

| Tool | Command | Notes |
|------|---------|-------|
| Go | `go build`, `go test` | + `gopls` LSP server |
| Node.js | `node`, `npm`, `npx` | |
| Python 3 | `python3`, `pip`, `python3 -m venv` | |
| Rust | `rust-analyzer` | LSP server (no compiler — use `rustup` if needed) |
| AWS CLI | `aws` | |
| Docker CLI | `docker` | Client only (no daemon) |
| kubectl | `kubectl` | |
| RWX CLI | `rwx` | |
| git | `git` | HTTPS + SSH protocols |
| Build tools | `make`, `gcc`, `g++` | |
| Utilities | `curl`, `jq`, `unzip`, `ssh` | |
DEVTOOLS
fi

# ── Skip Claude onboarding wizard ─────────────────────────────────────────

printf '{"hasCompletedOnboarding":true,"lastOnboardingVersion":"2.1.37","preferredTheme":"dark","bypassPermissionsModeAccepted":true}\n' > "${HOME}/.claude.json"

# ── Start coop + Claude ──────────────────────────────────────────────────
#
# We keep bash as PID 1 (no exec) so the pod survives if Claude/coop exit
# (e.g. user sends Ctrl+C which delivers SIGINT to Claude via the PTY).
# On child exit we clean up FIFO pipes and restart with --resume.
# SIGTERM from K8s is forwarded to coop for graceful shutdown.

cd "${WORKSPACE}"

COOP_CMD="coop --agent=claude --port 8080 --port-health 9090 --cols 200 --rows 50"

# Coop log level (overridable via pod env).
export COOP_LOG_LEVEL="${COOP_LOG_LEVEL:-info}"

# ── Auto-bypass startup prompts ────────────────────────────────────────
# Coop v0.4.0 no longer auto-dismisses setup prompts (bypass, trust, etc).
# This background function polls the coop /api/v1/agent endpoint and uses
# the high-level /api/v1/agent/respond API to accept setup prompts.
#
# IMPORTANT: Coop's screen parser can false-positive on "bypass permissions"
# text in the status bar after the agent is past setup. We only auto-respond
# during the first ~20s of startup, and verify the screen actually shows the
# bypass dialog (contains "No, exit" which is unique to the real prompt).
auto_bypass_startup() {
    false_positive_count=0
    for i in $(seq 1 30); do
        sleep 2
        state=$(curl -sf http://localhost:8080/api/v1/agent 2>/dev/null) || continue
        agent_state=$(echo "${state}" | jq -r '.state // empty' 2>/dev/null)
        prompt_type=$(echo "${state}" | jq -r '.prompt.type // empty' 2>/dev/null)
        subtype=$(echo "${state}" | jq -r '.prompt.subtype // empty' 2>/dev/null)

        # Handle interactive prompts while agent is in "starting" state.
        if [ "${agent_state}" = "starting" ]; then
            screen=$(curl -sf http://localhost:8080/api/v1/screen/text 2>/dev/null)

            # Handle "Resume Session" picker — press Escape to start fresh.
            if echo "${screen}" | grep -q "Resume Session"; then
                echo "[entrypoint] Detected resume session picker, pressing Escape to start fresh"
                curl -sf -X POST http://localhost:8080/api/v1/input/keys \
                    -H 'Content-Type: application/json' \
                    -d '{"keys":["Escape"]}' 2>&1 || true
                sleep 3
                continue
            fi

            # Handle "Detected a custom API key" prompt (bd-e2ege).
            # Claude Code detects sk-ant-* tokens in credentials and prompts.
            # Select "Yes" (option 1) to use it — the token is valid even though
            # it's stored in the OAuth credential slot.
            if echo "${screen}" | grep -q "Detected a custom API key"; then
                echo "[entrypoint] Detected API key prompt, selecting 'Yes' to use it"
                # Navigate to option 1 (Yes) with Up arrow, then Enter to confirm
                curl -sf -X POST http://localhost:8080/api/v1/input/keys \
                    -H 'Content-Type: application/json' \
                    -d '{"keys":["Up","Return"]}' 2>&1 || true
                sleep 3
                continue
            fi
        fi

        if [ "${prompt_type}" = "setup" ]; then
            # Verify this is a real setup prompt by checking the screen for
            # the actual dialog text, not just the status bar mention.
            screen=$(curl -sf http://localhost:8080/api/v1/screen 2>/dev/null)
            if echo "${screen}" | grep -q "No, exit"; then
                echo "[entrypoint] Auto-accepting setup prompt (subtype: ${subtype})"
                # Option 2 = "Yes, I accept" for bypass; option 1 = "No, exit"
                curl -sf -X POST http://localhost:8080/api/v1/agent/respond \
                    -H 'Content-Type: application/json' \
                    -d '{"option":2}' 2>&1 || true
                false_positive_count=0
                # Give agent time to process the response before next check
                sleep 5
                continue
            else
                false_positive_count=$((false_positive_count + 1))
                # If we see setup state without dialog 5+ times, it's a false positive
                # from the status bar text on a resumed session
                if [ "${false_positive_count}" -ge 5 ]; then
                    echo "[entrypoint] Skipping false-positive setup prompt (no dialog after ${false_positive_count} checks)"
                    return 0
                fi
                continue
            fi
        fi
        # If agent is past setup prompts, we're done
        agent_state=$(echo "${state}" | jq -r '.state // empty' 2>/dev/null)
        if [ "${agent_state}" = "idle" ] || [ "${agent_state}" = "working" ]; then
            return 0
        fi
    done
    echo "[entrypoint] WARNING: auto-bypass timed out after 60s"
}

# ── Inject initial work prompt ────────────────────────────────────────
# After auto-bypass completes and Claude is idle, send the initial work
# prompt via coop's nudge API. This is the coop equivalent of the tmux
# NudgeSession() call in session_manager.go:310-340.
#
# The nudge tells Claude to check its hook and begin working. Without this,
# K8s-spawned polecats boot to an empty welcome screen and sit idle.
#
# Uses POST /api/v1/agent/nudge (reliable delivery — coop queues the message
# and injects it when Claude is ready for input, unlike raw /api/v1/input).
inject_initial_prompt() {
    # Wait for agent to be past setup and idle
    for i in $(seq 1 60); do
        sleep 2
        state=$(curl -sf http://localhost:8080/api/v1/agent 2>/dev/null) || continue
        agent_state=$(echo "${state}" | jq -r '.state // empty' 2>/dev/null)
        if [ "${agent_state}" = "idle" ]; then
            break
        fi
        # If agent is already working (hook triggered it), no nudge needed
        if [ "${agent_state}" = "working" ]; then
            echo "[entrypoint] Agent already working, skipping initial prompt"
            return 0
        fi
    done

    # Build nudge message based on role
    local nudge_msg=""
    case "${ROLE}" in
        polecat)
            nudge_msg="Work is on your hook. Run \`gt hook\` now and begin immediately. If no hook is set, run \`bd ready\` to find available work."
            ;;
        mayor)
            nudge_msg="Run \`gt prime\` to load context, then check \`gt mail inbox\` and \`gt rig list\` to begin your patrol."
            ;;
        witness|refinery|deacon)
            nudge_msg="Run \`gt prime\` to load context, then check \`gt mail inbox\` for pending work."
            ;;
        *)
            nudge_msg="Run \`gt prime\` to load your context and begin working."
            ;;
    esac

    echo "[entrypoint] Injecting initial work prompt (role: ${ROLE})"
    response=$(curl -sf -X POST http://localhost:8080/api/v1/agent/nudge \
        -H 'Content-Type: application/json' \
        -d "{\"message\": \"${nudge_msg}\"}" 2>&1) || {
        echo "[entrypoint] WARNING: nudge failed: ${response}"
        return 0
    }

    delivered=$(echo "${response}" | jq -r '.delivered // false' 2>/dev/null)
    if [ "${delivered}" = "true" ]; then
        echo "[entrypoint] Initial prompt delivered successfully"
    else
        reason=$(echo "${response}" | jq -r '.reason // "unknown"' 2>/dev/null)
        echo "[entrypoint] WARNING: nudge not delivered: ${reason}"
    fi
}

# ── OAuth credential refresh ────────────────────────────────────────────
# Claude OAuth access tokens expire after ~8 hours. This background loop
# uses the refresh_token to obtain a fresh access_token before expiry.
# Runs every 5 minutes, refreshes when within 1 hour of expiry.
#
# Token endpoint: https://platform.claude.com/v1/oauth/token
# Client ID: from Claude Code source (public OAuth client).
OAUTH_TOKEN_URL="https://platform.claude.com/v1/oauth/token"
OAUTH_CLIENT_ID="9d1c250a-e61b-44d9-88ed-5944d1962f5e"
CREDS_FILE="${CLAUDE_STATE}/.credentials.json"

refresh_credentials() {
    sleep 30  # Let Claude start first
    local consecutive_failures=0
    local max_failures=5  # Exit after 5 consecutive failures (25 min of retries)
    while true; do
        sleep 300  # Check every 5 minutes

        # Read current credentials
        if [ ! -f "${CREDS_FILE}" ]; then
            continue
        fi

        expires_at=$(jq -r '.claudeAiOauth.expiresAt // 0' "${CREDS_FILE}" 2>/dev/null)
        refresh_token=$(jq -r '.claudeAiOauth.refreshToken // empty' "${CREDS_FILE}" 2>/dev/null)

        if [ -z "${refresh_token}" ] || [ "${expires_at}" = "0" ]; then
            continue
        fi

        # Check if within 1 hour of expiry (3600000ms)
        now_ms=$(date +%s)000
        remaining_ms=$((expires_at - now_ms))
        if [ "${remaining_ms}" -gt 3600000 ]; then
            consecutive_failures=0  # Token still valid, reset counter
            continue  # More than 1 hour left, skip
        fi

        echo "[entrypoint] OAuth token expires in $((remaining_ms / 60000))m, refreshing..."

        response=$(curl -sf "${OAUTH_TOKEN_URL}" \
            -H 'Content-Type: application/json' \
            -d "{\"grant_type\":\"refresh_token\",\"refresh_token\":\"${refresh_token}\",\"client_id\":\"${OAUTH_CLIENT_ID}\"}" 2>/dev/null) || {
            consecutive_failures=$((consecutive_failures + 1))
            echo "[entrypoint] WARNING: OAuth refresh request failed (attempt ${consecutive_failures}/${max_failures})"
            if [ "${consecutive_failures}" -ge "${max_failures}" ]; then
                echo "[entrypoint] FATAL: OAuth refresh failed ${max_failures} consecutive times, terminating pod"
                kill -TERM $$ 2>/dev/null || kill -TERM 1 2>/dev/null
                exit 1
            fi
            continue
        }

        # Validate response has required fields
        new_access_token=$(echo "${response}" | jq -r '.access_token // empty' 2>/dev/null)
        new_refresh_token=$(echo "${response}" | jq -r '.refresh_token // empty' 2>/dev/null)
        expires_in=$(echo "${response}" | jq -r '.expires_in // 0' 2>/dev/null)

        if [ -z "${new_access_token}" ] || [ -z "${new_refresh_token}" ]; then
            consecutive_failures=$((consecutive_failures + 1))
            echo "[entrypoint] WARNING: OAuth refresh returned invalid response (attempt ${consecutive_failures}/${max_failures})"
            if [ "${consecutive_failures}" -ge "${max_failures}" ]; then
                echo "[entrypoint] FATAL: OAuth refresh failed ${max_failures} consecutive times, terminating pod"
                kill -TERM $$ 2>/dev/null || kill -TERM 1 2>/dev/null
                exit 1
            fi
            continue
        fi

        # Success — reset failure counter
        consecutive_failures=0

        # Compute new expiresAt (current time + expires_in seconds, in ms)
        new_expires_at=$(( $(date +%s) * 1000 + expires_in * 1000 ))

        # Read current creds and update tokens (preserves other fields like scopes)
        jq --arg at "${new_access_token}" \
           --arg rt "${new_refresh_token}" \
           --argjson ea "${new_expires_at}" \
           '.claudeAiOauth.accessToken = $at | .claudeAiOauth.refreshToken = $rt | .claudeAiOauth.expiresAt = $ea' \
           "${CREDS_FILE}" > "${CREDS_FILE}.tmp" && mv "${CREDS_FILE}.tmp" "${CREDS_FILE}"

        echo "[entrypoint] OAuth credentials refreshed (expires in $((expires_in / 3600))h)"
    done
}

# ── Monitor agent exit and shut down coop ──────────────────────────────
# Coop v0.4.0 stays alive in "awaiting shutdown" after the agent exits.
# This monitor polls the agent state and sends a shutdown request when
# the agent is in "exited" state, so the entrypoint's `wait` can return.
monitor_agent_exit() {
    # Wait for agent to start first
    sleep 10
    while true; do
        sleep 5
        state=$(curl -sf http://localhost:8080/api/v1/agent 2>/dev/null) || break
        agent_state=$(echo "${state}" | jq -r '.state // empty' 2>/dev/null)
        if [ "${agent_state}" = "exited" ]; then
            echo "[entrypoint] Agent exited, requesting coop shutdown"
            curl -sf -X POST http://localhost:8080/api/v1/shutdown 2>/dev/null || true
            return 0
        fi
    done
}

# ── Mux registration ──────────────────────────────────────────────────────
# If COOP_MUX_URL is set, register this agent's coop instance with the
# multiplexer so it appears in the mux dashboard and aggregated WebSocket.
MUX_SESSION_ID=""
register_with_mux() {
    local mux_url="${COOP_MUX_URL}"
    if [ -z "${mux_url}" ]; then
        return 0
    fi

    # Wait for local coop to be healthy
    for i in $(seq 1 30); do
        sleep 2
        curl -sf http://localhost:8080/api/v1/health >/dev/null 2>&1 && break
    done

    # Use pod hostname as session ID (stable across coop restarts within same pod)
    local session_id="${HOSTNAME:-$(hostname)}"
    local coop_url="http://${POD_IP:-$(hostname -i 2>/dev/null || echo localhost)}:8080"
    local auth_token="${COOP_AUTH_TOKEN:-${COOP_BROKER_TOKEN:-}}"
    local mux_auth="${COOP_MUX_AUTH_TOKEN:-${auth_token}}"

    echo "[entrypoint] Registering with mux: id=${session_id} url=${coop_url}"

    # Build registration payload
    local payload
    payload=$(jq -n \
        --arg url "${coop_url}" \
        --arg id "${session_id}" \
        --arg role "${GT_ROLE:-unknown}" \
        --arg agent "${GT_AGENT:-unknown}" \
        --arg pod "${HOSTNAME:-}" \
        --arg ip "${POD_IP:-}" \
        '{url: $url, id: $id, metadata: {role: $role, agent: $agent, k8s: {pod: $pod, ip: $ip}}}')

    # Add auth_token to payload if set
    if [ -n "${auth_token}" ]; then
        payload=$(echo "${payload}" | jq --arg t "${auth_token}" '.auth_token = $t')
    fi

    local result
    result=$(curl -sf -X POST "${mux_url}/api/v1/sessions" \
        -H 'Content-Type: application/json' \
        ${mux_auth:+-H "Authorization: Bearer ${mux_auth}"} \
        -d "${payload}" 2>&1) || {
        echo "[entrypoint] WARNING: mux registration failed: ${result}"
        return 0
    }

    MUX_SESSION_ID="${session_id}"
    echo "[entrypoint] Registered with mux as '${session_id}'"
}

deregister_from_mux() {
    if [ -z "${COOP_MUX_URL}" ] || [ -z "${MUX_SESSION_ID}" ]; then
        return 0
    fi
    local mux_auth="${COOP_MUX_AUTH_TOKEN:-${COOP_AUTH_TOKEN:-}}"
    curl -sf -X DELETE "${COOP_MUX_URL}/api/v1/sessions/${MUX_SESSION_ID}" \
        ${mux_auth:+-H "Authorization: Bearer ${mux_auth}"} >/dev/null 2>&1 || true
    echo "[entrypoint] Deregistered from mux (${MUX_SESSION_ID})"
    MUX_SESSION_ID=""
}

# ── Signal forwarding ─────────────────────────────────────────────────────
# Forward SIGTERM from K8s to coop so it can do graceful shutdown.
COOP_PID=""
forward_signal() {
    deregister_from_mux
    if [ -n "${COOP_PID}" ]; then
        echo "[entrypoint] Forwarding $1 to coop (pid ${COOP_PID})"
        kill -"$1" "${COOP_PID}" 2>/dev/null || true
        wait "${COOP_PID}" 2>/dev/null || true
    fi
    exit 0
}
trap 'forward_signal TERM' TERM
trap 'forward_signal INT' INT

# Start credential refresh in background (survives coop restarts).
refresh_credentials &

# ── Restart loop ──────────────────────────────────────────────────────────
# Max restarts to avoid infinite crash loop. Reset on successful long-lived run.
MAX_RESTARTS="${COOP_MAX_RESTARTS:-10}"
restart_count=0
MIN_RUNTIME_SECS=30  # If coop runs longer than this, reset the restart counter.

while true; do
    if [ "${restart_count}" -ge "${MAX_RESTARTS}" ]; then
        echo "[entrypoint] Max restarts (${MAX_RESTARTS}) reached, exiting"
        exit 1
    fi

    # Clean up stale FIFO pipes before each start (coop creates them per session).
    if [ -d "${COOP_STATE}/sessions" ]; then
        find "${COOP_STATE}/sessions" -name 'hook.pipe' -delete 2>/dev/null || true
    fi

    # Find latest session log for resume (respects GT_SESSION_RESUME=0 to disable).
    # After MAX_STALE_RETRIES consecutive resume failures we start fresh to avoid
    # an infinite loop of stale-session retirements.
    RESUME_FLAG=""
    MAX_STALE_RETRIES=2
    STALE_COUNT=$( (find "${CLAUDE_STATE}/projects" -maxdepth 2 -name '*.jsonl.stale' -type f 2>/dev/null || true) | wc -l | tr -d ' ')
    if [ "${SESSION_RESUME}" = "1" ] && [ -d "${CLAUDE_STATE}/projects" ] && [ "${STALE_COUNT:-0}" -lt "${MAX_STALE_RETRIES}" ]; then
        # Only look for top-level session logs, NOT subagent logs in subagents/ dirs.
        # Subagent .jsonl files cause Claude to show a "Resume Session" picker UI
        # which hangs because there's no user to interact with it.
        LATEST_LOG=$( (find "${CLAUDE_STATE}/projects" -maxdepth 2 -name '*.jsonl' -not -path '*/subagents/*' -type f -printf '%T@ %p\n' 2>/dev/null || true) \
            | sort -rn | head -1 | cut -d' ' -f2-)
        if [ -n "${LATEST_LOG}" ]; then
            RESUME_FLAG="--resume ${LATEST_LOG}"
        fi
    elif [ "${STALE_COUNT:-0}" -ge "${MAX_STALE_RETRIES}" ]; then
        echo "[entrypoint] Skipping resume: ${STALE_COUNT} stale session(s) found (max ${MAX_STALE_RETRIES}), starting fresh"
    fi

    start_time=$(date +%s)

    if [ -n "${RESUME_FLAG}" ]; then
        echo "[entrypoint] Starting coop + claude (${ROLE}/${AGENT}) with resume"
        ${COOP_CMD} ${RESUME_FLAG} -- claude --dangerously-skip-permissions &
        COOP_PID=$!
        (auto_bypass_startup && inject_initial_prompt) &
        monitor_agent_exit &
        wait "${COOP_PID}" 2>/dev/null && exit_code=0 || exit_code=$?
        COOP_PID=""

        # If resume failed, retire the stale session log so the next iteration
        # starts fresh.  The log is renamed (not deleted) so the agent can still
        # review it at <path>.stale if needed.
        if [ "${exit_code}" -ne 0 ] && [ -n "${LATEST_LOG}" ] && [ -f "${LATEST_LOG}" ]; then
            echo "[entrypoint] Resume failed (exit ${exit_code}), retiring stale session log"
            mv "${LATEST_LOG}" "${LATEST_LOG}.stale"
            echo "[entrypoint]   renamed: ${LATEST_LOG} -> ${LATEST_LOG}.stale"
        fi
    else
        echo "[entrypoint] Starting coop + claude (${ROLE}/${AGENT})"
        ${COOP_CMD} -- claude --dangerously-skip-permissions &
        COOP_PID=$!
        (auto_bypass_startup && inject_initial_prompt) &
        monitor_agent_exit &
        wait "${COOP_PID}" 2>/dev/null && exit_code=0 || exit_code=$?
        COOP_PID=""
    fi

    elapsed=$(( $(date +%s) - start_time ))
    echo "[entrypoint] Coop exited with code ${exit_code} after ${elapsed}s"

    # If coop ran long enough, reset the restart counter.
    if [ "${elapsed}" -ge "${MIN_RUNTIME_SECS}" ]; then
        restart_count=0
    fi

    restart_count=$((restart_count + 1))
    echo "[entrypoint] Restarting (attempt ${restart_count}/${MAX_RESTARTS}) in 2s..."
    sleep 2
done
