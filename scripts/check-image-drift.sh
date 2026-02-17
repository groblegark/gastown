#!/bin/bash
set -euo pipefail

# =============================================================================
# IMAGE VERSION DRIFT CHECKER
# =============================================================================
#
# Compares running container images in a Kubernetes namespace against the
# expected tags from platform-versions.env. Reports drift.
#
# Usage:
#   ./scripts/check-image-drift.sh                          # check gastown-next
#   ./scripts/check-image-drift.sh --namespace gastown-uat  # check another env
#
# Exit codes:
#   0 = all images match expected versions
#   1 = drift detected (wrong tags or digests)
#   2 = error (missing prereqs, namespace not found)
#
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
NAMESPACE="gastown-next"
VERBOSE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --namespace|-n) NAMESPACE="$2"; shift ;;
        --verbose|-v)   VERBOSE=true ;;
        --help|-h)
            echo "Usage: $(basename "$0") [--namespace NS] [--verbose]"
            exit 0
            ;;
        *) echo -e "${RED}Unknown option: $1${NC}"; exit 2 ;;
    esac
    shift
done

# Load expected versions
if [[ ! -f "$REPO_ROOT/platform-versions.env" ]]; then
    echo -e "${RED}Error: platform-versions.env not found${NC}"
    exit 2
fi
source "$REPO_ROOT/platform-versions.env"

echo -e "${CYAN}Image Drift Check — $NAMESPACE${NC}"
echo "  Expected platform version: $PLATFORM_VERSION"
echo "  Expected beads version:    $BEADS_VERSION"
echo ""

DRIFT=0

# Check a specific deployment/pod image
check_image() {
    local component="$1"
    local label_selector="$2"
    local expected_tag="$3"

    local pod_image
    pod_image=$(kubectl get pods -n "$NAMESPACE" -l "$label_selector" \
        -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null) || true

    if [[ -z "$pod_image" ]]; then
        echo -e "  ${YELLOW}⚠ $component: no pod found (selector: $label_selector)${NC}"
        return
    fi

    local actual_tag="${pod_image##*:}"

    if [[ "$actual_tag" == "$expected_tag" ]]; then
        echo -e "  ${GREEN}✓ $component: $actual_tag${NC}"
    else
        echo -e "  ${RED}✗ $component: $actual_tag (expected $expected_tag)${NC}"
        DRIFT=$((DRIFT + 1))
    fi

    if [[ "$VERBOSE" == true ]]; then
        local image_id
        image_id=$(kubectl get pods -n "$NAMESPACE" -l "$label_selector" \
            -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' 2>/dev/null) || true
        echo -e "    image: $pod_image"
        echo -e "    digest: ${image_id##*@}"
    fi
}

# Check agent pod images (gt- prefix pods)
check_agent_pod() {
    local pod_name="$1"
    local expected_tag="$2"

    local pod_image
    pod_image=$(kubectl get pod "$pod_name" -n "$NAMESPACE" \
        -o jsonpath='{.spec.containers[0].image}' 2>/dev/null) || true

    if [[ -z "$pod_image" ]]; then
        echo -e "  ${YELLOW}⚠ $pod_name: not found${NC}"
        return
    fi

    local actual_tag="${pod_image##*:}"

    if [[ "$actual_tag" == "$expected_tag" ]]; then
        echo -e "  ${GREEN}✓ $pod_name: $actual_tag${NC}"
    else
        echo -e "  ${RED}✗ $pod_name: $actual_tag (expected $expected_tag)${NC}"
        DRIFT=$((DRIFT + 1))
    fi
}

echo -e "${CYAN}Infrastructure:${NC}"

# Daemon (CalVer — all images use PLATFORM_VERSION)
check_image "bd-daemon" "app.kubernetes.io/component=daemon" "$PLATFORM_VERSION"

# Controller
check_image "agent-controller" "app.kubernetes.io/component=agent-controller" "$PLATFORM_VERSION"

# Coop broker
check_image "coop-broker" "app.kubernetes.io/component=coop-broker" "$PLATFORM_VERSION"

# Slackbot (uses daemon image = PLATFORM_VERSION)
check_image "slackbot" "app.kubernetes.io/component=slackbot" "$PLATFORM_VERSION"

echo ""
echo -e "${CYAN}Agent Pods:${NC}"

# Find all gt- pods and check their images
agent_pods=$(kubectl get pods -n "$NAMESPACE" -o name 2>/dev/null | grep "pod/gt-" | sed 's|pod/||') || true

if [[ -z "$agent_pods" ]]; then
    echo -e "  ${YELLOW}No agent pods found${NC}"
else
    while IFS= read -r pod; do
        check_agent_pod "$pod" "$PLATFORM_VERSION"
    done <<< "$agent_pods"
fi

echo ""

if [[ $DRIFT -eq 0 ]]; then
    echo -e "${GREEN}No drift detected. All images match expected versions.${NC}"
    exit 0
else
    echo -e "${RED}Drift detected: $DRIFT image(s) out of date.${NC}"
    echo ""
    echo "To fix, run:"
    echo "  ./scripts/bump-helm-images.sh --apply --commit"
    echo "  helm upgrade $NAMESPACE charts/gastown/ -n $NAMESPACE --values values.yaml --values values/${NAMESPACE}.yaml"
    exit 1
fi
