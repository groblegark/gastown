#!/bin/bash
set -euo pipefail

# =============================================================================
# PLATFORM DEPLOY — One command from version bump to verified deployment
# =============================================================================
#
# Automates the full CalVer deployment lifecycle:
#   1. Bump PLATFORM_VERSION in platform-versions.env
#   2. Optionally bump BEADS_VERSION and/or COOP_VERSION
#   3. Commit and push to gastown main (triggers docker.yml image build)
#   4. Wait for RWX docker.yml pipeline to succeed
#   5. Run bump-helm-images.sh to update fics-helm-chart values
#   6. Run helm upgrade to deploy to target namespace
#   7. Run check-image-drift.sh to verify zero drift
#
# Usage:
#   ./scripts/deploy-platform.sh                          # bump + build + deploy to gastown-next
#   ./scripts/deploy-platform.sh --namespace gastown-uat  # deploy to another env
#   ./scripts/deploy-platform.sh --beads v0.63.0          # also bump beads version
#   ./scripts/deploy-platform.sh --skip-build             # use existing images (skip RWX build)
#   ./scripts/deploy-platform.sh --dry-run                # show what would happen
#
# Prerequisites:
#   - rwx CLI installed and authenticated
#   - helm CLI installed with cluster access
#   - fics-helm-chart checked out at ~/book/fics-helm-chart
#   - kubectl configured for target cluster
#
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HELM_CHART_DIR="${HELM_CHART_DIR:-$HOME/book/fics-helm-chart}"
NAMESPACE="gastown-next"
BEADS_VERSION_OVERRIDE=""
COOP_VERSION_OVERRIDE=""
DRY_RUN=false
SKIP_BUILD=false
BUILD_TIMEOUT=600  # 10 minutes

usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

One command from version bump to verified deployment.

Options:
  --namespace NS     Target namespace (default: gastown-next)
  --beads VERSION    Bump BEADS_VERSION (e.g., v0.63.0)
  --coop VERSION     Bump COOP_VERSION (e.g., v0.13.0)
  --skip-build       Skip RWX build, use existing images
  --dry-run          Show what would happen without making changes
  --helm-dir DIR     Path to fics-helm-chart (default: ~/book/fics-helm-chart)
  --timeout SECS     Build timeout in seconds (default: 600)
  --help             Show this help

Examples:
  $(basename "$0")                              # Full deploy to gastown-next
  $(basename "$0") --beads v0.63.0              # Bump beads + deploy
  $(basename "$0") --skip-build                 # Deploy with existing images
  $(basename "$0") --dry-run                    # Preview changes only
  $(basename "$0") --namespace gastown-uat      # Deploy to UAT
EOF
    exit 0
}

# Parse args
while [[ $# -gt 0 ]]; do
    case "$1" in
        --namespace|-n) NAMESPACE="$2"; shift ;;
        --beads)        BEADS_VERSION_OVERRIDE="$2"; shift ;;
        --coop)         COOP_VERSION_OVERRIDE="$2"; shift ;;
        --skip-build)   SKIP_BUILD=true ;;
        --dry-run)      DRY_RUN=true ;;
        --helm-dir)     HELM_CHART_DIR="$2"; shift ;;
        --timeout)      BUILD_TIMEOUT="$2"; shift ;;
        --help|-h)      usage ;;
        *)              echo -e "${RED}Unknown option: $1${NC}"; usage ;;
    esac
    shift
done

# ---------------------------------------------------------------------------
# Validate prerequisites
# ---------------------------------------------------------------------------

if [[ ! -f "$REPO_ROOT/platform-versions.env" ]]; then
    echo -e "${RED}Error: platform-versions.env not found at $REPO_ROOT${NC}"
    exit 1
fi

VALUES_FILE="$HELM_CHART_DIR/charts/gastown/values/${NAMESPACE}.yaml"
if [[ ! -f "$VALUES_FILE" ]]; then
    echo -e "${RED}Error: Values file not found: $VALUES_FILE${NC}"
    echo "Available environments:"
    ls "$HELM_CHART_DIR/charts/gastown/values/" 2>/dev/null | sed 's/\.yaml$//' | sed 's/^/  /'
    exit 1
fi

if ! command -v rwx &>/dev/null && [[ "$SKIP_BUILD" == false ]]; then
    echo -e "${RED}Error: rwx CLI not found. Install from https://rwx.com or use --skip-build.${NC}"
    exit 1
fi

if ! command -v helm &>/dev/null; then
    echo -e "${RED}Error: helm CLI not found.${NC}"
    exit 1
fi

# ---------------------------------------------------------------------------
# Step 1: Compute next CalVer version
# ---------------------------------------------------------------------------

source "$REPO_ROOT/platform-versions.env"
CURRENT_VERSION="$PLATFORM_VERSION"

# Parse current CalVer: YYYY.MM.DD.N
TODAY=$(date +%Y.%m.%d)
CURRENT_DATE="${CURRENT_VERSION%.*}"
CURRENT_BUILD="${CURRENT_VERSION##*.}"

if [[ "$CURRENT_DATE" == "$TODAY" ]]; then
    NEXT_BUILD=$((CURRENT_BUILD + 1))
else
    NEXT_BUILD=1
fi
NEXT_VERSION="${TODAY}.${NEXT_BUILD}"

# When skipping build, reuse the current version (images already exist)
if [[ "$SKIP_BUILD" == true ]]; then
    NEXT_VERSION="$CURRENT_VERSION"
fi

echo -e "${BOLD}${CYAN}Platform Deploy${NC}"
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo -e "  Namespace:          ${BOLD}$NAMESPACE${NC}"
echo -e "  Current version:    $CURRENT_VERSION"
echo -e "  Next version:       ${BOLD}$NEXT_VERSION${NC}"
echo -e "  Beads version:      ${BEADS_VERSION_OVERRIDE:-$BEADS_VERSION (unchanged)}"
echo -e "  Coop version:       ${COOP_VERSION_OVERRIDE:-$COOP_VERSION (unchanged)}"
echo -e "  Skip build:         $SKIP_BUILD"
echo ""

if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}DRY RUN — no changes will be made.${NC}"
    echo ""
    echo "Would do:"
    if [[ "$SKIP_BUILD" == true ]]; then
        echo "  1. Use existing PLATFORM_VERSION=$NEXT_VERSION (no bump)"
        echo "  2. (skipped) No commit needed"
        echo "  3. (skipped) Use existing images"
    else
        echo "  1. Update platform-versions.env: PLATFORM_VERSION=$NEXT_VERSION"
        [[ -n "$BEADS_VERSION_OVERRIDE" ]] && echo "     Also: BEADS_VERSION=$BEADS_VERSION_OVERRIDE"
        [[ -n "$COOP_VERSION_OVERRIDE" ]] && echo "     Also: COOP_VERSION=$COOP_VERSION_OVERRIDE"
        echo "  2. Commit and push to gastown main"
        echo "  3. Wait for RWX docker.yml to build images"
    fi
    echo "  4. Run bump-helm-images.sh --apply --commit"
    echo "  5. helm upgrade $NAMESPACE"
    echo "  6. check-image-drift.sh --namespace $NAMESPACE"
    exit 0
fi

# ---------------------------------------------------------------------------
# Step 2: Bump platform-versions.env (skip if --skip-build)
# ---------------------------------------------------------------------------

if [[ "$SKIP_BUILD" == true ]]; then
    # Use current version — images already exist
    NEXT_VERSION="$CURRENT_VERSION"
    echo -e "${CYAN}[1/6] Using existing platform version${NC}"
    echo -e "  PLATFORM_VERSION = ${GREEN}$NEXT_VERSION${NC} (no bump)"
    echo -e "${CYAN}[2/6] Skipping commit (--skip-build)${NC}"
else
    echo -e "${CYAN}[1/6] Bumping platform-versions.env${NC}"

    sed -i '' "s/^PLATFORM_VERSION=.*/PLATFORM_VERSION=$NEXT_VERSION/" "$REPO_ROOT/platform-versions.env"

    if [[ -n "$BEADS_VERSION_OVERRIDE" ]]; then
        sed -i '' "s/^BEADS_VERSION=.*/BEADS_VERSION=$BEADS_VERSION_OVERRIDE/" "$REPO_ROOT/platform-versions.env"
        echo "  BEADS_VERSION → $BEADS_VERSION_OVERRIDE"
    fi

    if [[ -n "$COOP_VERSION_OVERRIDE" ]]; then
        sed -i '' "s/^COOP_VERSION=.*/COOP_VERSION=$COOP_VERSION_OVERRIDE/" "$REPO_ROOT/platform-versions.env"
        echo "  COOP_VERSION → $COOP_VERSION_OVERRIDE"
    fi

    echo -e "  PLATFORM_VERSION → ${GREEN}$NEXT_VERSION${NC}"

    # ---------------------------------------------------------------------------
    # Step 3: Commit and push
    # ---------------------------------------------------------------------------

    echo -e "${CYAN}[2/6] Committing and pushing to gastown main${NC}"

    cd "$REPO_ROOT"
    git add platform-versions.env

    COMMIT_MSG="chore: bump platform to $NEXT_VERSION"
    [[ -n "$BEADS_VERSION_OVERRIDE" ]] && COMMIT_MSG="$COMMIT_MSG (beads $BEADS_VERSION_OVERRIDE)"
    [[ -n "$COOP_VERSION_OVERRIDE" ]] && COMMIT_MSG="$COMMIT_MSG (coop $COOP_VERSION_OVERRIDE)"

    git commit -m "$COMMIT_MSG"
    git push

    echo -e "  ${GREEN}✓ Pushed${NC}"
fi

# ---------------------------------------------------------------------------
# Step 4: Wait for RWX build (or skip)
# ---------------------------------------------------------------------------

if [[ "$SKIP_BUILD" == true ]]; then
    echo -e "${CYAN}[3/6] Skipping RWX build (--skip-build)${NC}"
else
    echo -e "${CYAN}[3/6] Waiting for RWX docker.yml pipeline...${NC}"

    BUILD_OUTPUT=$(rwx run .rwx/docker.yml \
        --init commit-sha="$(git rev-parse HEAD)" \
        --wait --output json 2>&1) || true

    RUN_ID=$(echo "$BUILD_OUTPUT" | grep -o '"RunID":"[^"]*"' | cut -d'"' -f4)
    STATUS=$(echo "$BUILD_OUTPUT" | grep -o '"ResultStatus":"[^"]*"' | cut -d'"' -f4)

    if [[ "$STATUS" == "succeeded" ]]; then
        echo -e "  ${GREEN}✓ Build succeeded${NC} (RunID: $RUN_ID)"
    else
        echo -e "  ${YELLOW}⚠ Pipeline finished with status: $STATUS${NC} (RunID: $RUN_ID)"
        echo "  Checking if images were pushed despite pipeline status..."

        # Verify the images actually exist on GHCR (push tasks may have
        # succeeded even if deploy-rwx or other non-push tasks failed).
        IMAGES_OK=true
        for img in "ghcr.io/groblegark/beads:$NEXT_VERSION" \
                   "ghcr.io/groblegark/gastown/agent-controller:$NEXT_VERSION" \
                   "ghcr.io/groblegark/gastown/gastown-agent:$NEXT_VERSION"; do
            if crane digest "$img" &>/dev/null; then
                echo -e "    ${GREEN}✓${NC} $img"
            else
                echo -e "    ${RED}✗${NC} $img (not found)"
                IMAGES_OK=false
            fi
        done

        if [[ "$IMAGES_OK" == true ]]; then
            echo -e "  ${GREEN}✓ All images available — continuing deploy${NC}"
        else
            echo -e "  ${RED}✗ Missing images — cannot deploy${NC}"
            echo "  Check logs: rwx results $RUN_ID"
            exit 1
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Step 5: Bump helm chart image tags
# ---------------------------------------------------------------------------

echo -e "${CYAN}[4/6] Bumping helm chart image tags${NC}"

# Re-source to pick up any changes
source "$REPO_ROOT/platform-versions.env"

"$SCRIPT_DIR/bump-helm-images.sh" --apply --commit --env "$NAMESPACE"

echo -e "  ${GREEN}✓ Helm values updated${NC}"

# ---------------------------------------------------------------------------
# Step 6: Helm deploy
# ---------------------------------------------------------------------------

echo -e "${CYAN}[5/6] Deploying to $NAMESPACE${NC}"

cd "$HELM_CHART_DIR/charts/gastown"

# Update dependencies first
helm dependency update ./ 2>&1 | tail -3

helm upgrade --install "$NAMESPACE" ./ \
    -n "$NAMESPACE" \
    --values values.yaml \
    --values "values/${NAMESPACE}.yaml"

echo -e "  ${GREEN}✓ Deployed${NC}"

# ---------------------------------------------------------------------------
# Step 7: Verify (drift check)
# ---------------------------------------------------------------------------

echo -e "${CYAN}[6/6] Verifying deployment (drift check)${NC}"

cd "$REPO_ROOT"

# Wait for pods to roll out
echo "  Waiting 45s for rollout..."
sleep 45

if "$SCRIPT_DIR/check-image-drift.sh" --namespace "$NAMESPACE"; then
    echo ""
    echo -e "${GREEN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}${BOLD}✓ Platform $NEXT_VERSION deployed to $NAMESPACE${NC}"
    echo -e "${GREEN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
else
    echo ""
    echo -e "${YELLOW}⚠ Drift detected — pods may still be rolling out.${NC}"
    echo "  Re-check in 60s: $SCRIPT_DIR/check-image-drift.sh --namespace $NAMESPACE"
fi
