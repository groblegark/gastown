#!/bin/bash
set -euo pipefail

# =============================================================================
# HELM IMAGE TAG BUMPER
# =============================================================================
#
# Updates image tags in fics-helm-chart values files after a gastown image build.
# Reads platform-versions.env for the source of truth, then updates the target
# helm values file.
#
# Usage:
#   ./scripts/bump-helm-images.sh                    # dry-run, show changes
#   ./scripts/bump-helm-images.sh --apply            # apply changes
#   ./scripts/bump-helm-images.sh --apply --commit   # apply + git commit + push
#   ./scripts/bump-helm-images.sh --env gastown-uat  # target a different env
#
# What it updates:
#   - gastown.coopBroker.image.tag          → PLATFORM_VERSION
#   - gastown.agentController.image.tag     → PLATFORM_VERSION
#   - gastown.agentController.agentImage.tag → PLATFORM_VERSION
#   - gastown.bd-daemon.image.tag           → BEADS_VERSION
#
# Requires:
#   - platform-versions.env in repo root (source of truth)
#   - fics-helm-chart checked out at HELM_CHART_DIR
#
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HELM_CHART_DIR="${HELM_CHART_DIR:-$HOME/book/fics-helm-chart}"
ENV_NAME="gastown-next"
APPLY=false
AUTO_COMMIT=false

usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Update helm chart image tags from platform-versions.env.

Options:
  --apply        Apply changes (default: dry-run)
  --commit       Commit and push changes (implies --apply)
  --env NAME     Target environment (default: gastown-next)
  --helm-dir DIR Path to fics-helm-chart (default: ~/book/fics-helm-chart)
  --help         Show this help

Examples:
  $(basename "$0")                         # Dry-run for gastown-next
  $(basename "$0") --apply                 # Apply to gastown-next
  $(basename "$0") --apply --commit        # Apply, commit, and push
  $(basename "$0") --env gastown-uat       # Target gastown-uat
EOF
    exit 0
}

# Parse args
while [[ $# -gt 0 ]]; do
    case "$1" in
        --apply)     APPLY=true ;;
        --commit)    APPLY=true; AUTO_COMMIT=true ;;
        --env)       ENV_NAME="$2"; shift ;;
        --helm-dir)  HELM_CHART_DIR="$2"; shift ;;
        --help|-h)   usage ;;
        *)           echo -e "${RED}Unknown option: $1${NC}"; usage ;;
    esac
    shift
done

# Validate prerequisites
if [[ ! -f "$REPO_ROOT/platform-versions.env" ]]; then
    echo -e "${RED}Error: platform-versions.env not found at $REPO_ROOT${NC}"
    exit 1
fi

VALUES_FILE="$HELM_CHART_DIR/charts/gastown/values/${ENV_NAME}.yaml"
if [[ ! -f "$VALUES_FILE" ]]; then
    echo -e "${RED}Error: Values file not found: $VALUES_FILE${NC}"
    echo "Available environments:"
    ls "$HELM_CHART_DIR/charts/gastown/values/" 2>/dev/null | sed 's/\.yaml$//' | sed 's/^/  /'
    exit 1
fi

# Load versions
source "$REPO_ROOT/platform-versions.env"

echo -e "${CYAN}Platform Versions:${NC}"
echo "  PLATFORM_VERSION = $PLATFORM_VERSION"
echo "  BEADS_VERSION    = $BEADS_VERSION"
echo "  COOP_VERSION     = $COOP_VERSION"
echo ""
echo -e "${CYAN}Target: ${NC}$VALUES_FILE"
echo ""

# Track changes
CHANGES=0

# Helper: update a specific tag line in context
# Uses awk to find the right tag based on surrounding context (repository line)
update_tag() {
    local repo_pattern="$1"
    local new_tag="$2"
    local description="$3"

    # Find the current tag value after the repo line
    local current_tag
    current_tag=$(awk -v repo="$repo_pattern" '
        $0 ~ repo { found=1; next }
        found && /tag:/ { gsub(/.*tag: *"?/, ""); gsub(/".*/, ""); print; exit }
    ' "$VALUES_FILE")

    if [[ -z "$current_tag" ]]; then
        echo -e "  ${YELLOW}⚠ Not found: $description${NC}"
        return
    fi

    if [[ "$current_tag" == "$new_tag" ]]; then
        echo -e "  ${GREEN}✓ $description: $current_tag (unchanged)${NC}"
        return
    fi

    echo -e "  ${YELLOW}→ $description: $current_tag → $new_tag${NC}"
    CHANGES=$((CHANGES + 1))

    if [[ "$APPLY" == true ]]; then
        # Use awk to do the contextual replacement
        awk -v repo="$repo_pattern" -v new_tag="$new_tag" '
            $0 ~ repo { found=1; print; next }
            found && /tag:/ {
                sub(/tag: *"[^"]*"/, "tag: \"" new_tag "\"")
                found=0
            }
            { print }
        ' "$VALUES_FILE" > "${VALUES_FILE}.tmp"
        mv "${VALUES_FILE}.tmp" "$VALUES_FILE"
    fi
}

echo -e "${CYAN}Checking image tags:${NC}"

# 1. Beads daemon image
update_tag "repository: ghcr.io/groblegark/beads" \
    "$BEADS_VERSION" \
    "bd-daemon"

# 2. Coop broker image (gastown-agent)
update_tag "repository: ghcr.io/groblegark/gastown/gastown-agent" \
    "$PLATFORM_VERSION" \
    "coop-broker (gastown-agent)"

# 3. Agent controller image
update_tag "repository: ghcr.io/groblegark/gastown/agent-controller" \
    "$PLATFORM_VERSION" \
    "agent-controller"

# 4. Agent image (agentImage under agentController)
# This one is tricky — it's the second gastown-agent reference, under agentController
# We handle it by looking for agentImage context
local_changes=0
current_agent_tag=$(awk '
    /agentImage:/ { found=1; next }
    found && /tag:/ { gsub(/.*tag: *"?/, ""); gsub(/".*/, ""); print; exit }
' "$VALUES_FILE")

if [[ -n "$current_agent_tag" && "$current_agent_tag" != "$PLATFORM_VERSION" ]]; then
    echo -e "  ${YELLOW}→ agentImage: $current_agent_tag → $PLATFORM_VERSION${NC}"
    CHANGES=$((CHANGES + 1))
    if [[ "$APPLY" == true ]]; then
        awk -v new_tag="$PLATFORM_VERSION" '
            /agentImage:/ { found=1; print; next }
            found && /tag:/ {
                sub(/tag: *"[^"]*"/, "tag: \"" new_tag "\"")
                found=0
            }
            { print }
        ' "$VALUES_FILE" > "${VALUES_FILE}.tmp"
        mv "${VALUES_FILE}.tmp" "$VALUES_FILE"
    fi
elif [[ -n "$current_agent_tag" ]]; then
    echo -e "  ${GREEN}✓ agentImage: $current_agent_tag (unchanged)${NC}"
else
    echo -e "  ${YELLOW}⚠ Not found: agentImage${NC}"
fi

echo ""

if [[ $CHANGES -eq 0 ]]; then
    echo -e "${GREEN}All tags already up to date.${NC}"
    exit 0
fi

if [[ "$APPLY" == false ]]; then
    echo -e "${YELLOW}$CHANGES tag(s) need updating. Run with --apply to make changes.${NC}"
    exit 0
fi

echo -e "${GREEN}✓ Updated $CHANGES tag(s) in $VALUES_FILE${NC}"

# Commit if requested
if [[ "$AUTO_COMMIT" == true ]]; then
    echo ""
    echo "Committing and pushing..."
    cd "$HELM_CHART_DIR"

    if ! git diff --quiet "$VALUES_FILE"; then
        git add "$VALUES_FILE"
        git commit -m "chore(${ENV_NAME}): bump images to platform ${PLATFORM_VERSION}

Updated from gastown platform-versions.env:
- Platform images: ${PLATFORM_VERSION}
- Beads daemon: ${BEADS_VERSION}

Generated by scripts/bump-helm-images.sh"

        git push
        echo -e "${GREEN}✓ Committed and pushed to $(git remote get-url origin)${NC}"
    else
        echo -e "${YELLOW}No changes to commit.${NC}"
    fi
fi
