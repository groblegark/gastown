#!/usr/bin/env bash
# test-image-version-drift.sh — verify all images run expected CalVer versions
#
# Checks that running container images match the expected platform version
# from the helm values. Detects drift caused by missed upgrades or stale tags.
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-image-version-drift.sh

set -euo pipefail
MODULE_NAME="image-version-drift"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

# ── Helpers ────────────────────────────────────────────────────────────

# Collect all gastown image references from running pods
get_gastown_images() {
  kube get pods -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{range .spec.initContainers[*]}{.image}{"\n"}{end}{end}' \
    | grep -E 'groblegark/gastown/' \
    | sort -u
}

# Extract CalVer tags from image list
extract_calver_tags() {
  grep -oE '20[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]+' | sort -u
}

# ── Test functions ─────────────────────────────────────────────────────

test_single_calver() {
  local images tags tag_count
  images=$(get_gastown_images)
  [[ -n "$images" ]] || return 1

  tags=$(echo "$images" | extract_calver_tags)
  tag_count=$(echo "$tags" | wc -l | tr -d ' ')

  if [[ "$tag_count" -eq 1 ]]; then
    log "All images at CalVer $(echo "$tags")"
    return 0
  else
    log "Version drift: $tag_count different tags: $(echo "$tags" | tr '\n' ' ')"
    echo "$images" | while read -r img; do log "  $img"; done
    return 1
  fi
}

test_no_latest_tags() {
  local images latest
  images=$(get_gastown_images)
  latest=$(echo "$images" | grep ':latest$' || true)

  if [[ -z "$latest" ]]; then
    return 0
  else
    log "Found :latest tags:"
    echo "$latest" | while read -r img; do log "  $img"; done
    return 1
  fi
}

test_broker_agent_match() {
  local broker_img agent_img broker_tag agent_tag

  broker_img=$(kube get deployment -l app.kubernetes.io/component=coop-broker \
    -o jsonpath='{.items[0].spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
  [[ -n "$broker_img" ]] || { log "No coop broker found"; return 0; }

  broker_tag=$(echo "$broker_img" | extract_calver_tags | head -1)

  # Check running agent pods
  agent_img=$(kube get pods -l gastown.io/managed-by=agent-controller \
    -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null || echo "")

  if [[ -z "$agent_img" ]]; then
    log "No agent pods to compare (broker tag: $broker_tag)"
    return 0
  fi

  agent_tag=$(echo "$agent_img" | extract_calver_tags | head -1)

  if [[ "$broker_tag" == "$agent_tag" ]]; then
    log "Broker ($broker_tag) == Agent ($agent_tag)"
    return 0
  else
    log "Broker ($broker_tag) != Agent ($agent_tag)"
    return 1
  fi
}

test_platform_version_file() {
  local pod runtime_version image_tag

  pod=$(get_pod "app.kubernetes.io/component=coop-broker")
  [[ -n "$pod" ]] || { log "No broker pod found"; return 0; }

  runtime_version=$(kube exec "$pod" -- cat /etc/platform-version 2>/dev/null || echo "")
  [[ -n "$runtime_version" ]] || { log "/etc/platform-version not found"; return 0; }

  image_tag=$(kube get pod "$pod" -o jsonpath='{.spec.containers[0].image}' 2>/dev/null \
    | extract_calver_tags | head -1)

  if [[ "$runtime_version" == "$image_tag" ]]; then
    log "Runtime version ($runtime_version) matches image tag"
    return 0
  else
    log "Runtime version ($runtime_version) != image tag ($image_tag)"
    return 1
  fi
}

# ── Run tests ──────────────────────────────────────────────────────────

log "Checking image version consistency in $NS"

run_test "All gastown images share single CalVer version" test_single_calver
run_test "No floating :latest tags on gastown images" test_no_latest_tags
run_test "Coop broker and agent images use same tag" test_broker_agent_match
run_test "Platform version file matches running image tag" test_platform_version_file

print_summary
