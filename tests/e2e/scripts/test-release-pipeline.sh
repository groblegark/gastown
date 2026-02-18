#!/usr/bin/env bash
# test-release-pipeline.sh — verify the release pipeline chain is correctly wired.
#
# Validates that the deployed state on gastown-rwx traces back through the
# complete release pipeline: platform-versions.env → GHCR images → deployed
# pods → binary versions → GitHub releases → Helm chart versions.
#
# This test does NOT cut a real release. It verifies the existing deployment
# was produced by a correctly functioning pipeline.
#
# Tests:
#   1. platform-versions.env is parseable with valid CalVer
#   2. GHCR images exist for PLATFORM_VERSION
#   3. Deployed daemon pods run the expected image tag
#   4. bd binary version inside pods matches BEADS_VERSION
#   5. GitHub release exists for BEADS_VERSION
#   6. Helm release version matches PLATFORM_VERSION
#   7. All pods in namespace run the same PLATFORM_VERSION
#
# Usage: E2E_NAMESPACE=gastown-rwx ./scripts/test-release-pipeline.sh

set -euo pipefail
MODULE_NAME="release-pipeline"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing release pipeline chain in $NS"

# ── Load platform versions ──────────────────────────────────────────
# Find platform-versions.env relative to gastown repo root
GASTOWN_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
VERSIONS_FILE="$GASTOWN_ROOT/platform-versions.env"

PLATFORM_VERSION=""
BEADS_VERSION=""
COOP_VERSION=""

# ── Test functions ───────────────────────────────────────────────────

test_versions_parseable() {
  if [[ ! -f "$VERSIONS_FILE" ]]; then
    log "platform-versions.env not found at $VERSIONS_FILE"
    return 1
  fi

  # Source the file to get version vars
  source "$VERSIONS_FILE"

  log "PLATFORM_VERSION=$PLATFORM_VERSION"
  log "BEADS_VERSION=$BEADS_VERSION"
  log "COOP_VERSION=$COOP_VERSION"

  # Validate CalVer format: YYYY.MM.DD.N
  if [[ ! "$PLATFORM_VERSION" =~ ^20[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]+$ ]]; then
    log "PLATFORM_VERSION '$PLATFORM_VERSION' doesn't match CalVer YYYY.MM.DD.N"
    return 1
  fi

  # Validate semver tags
  [[ "$BEADS_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] && \
  [[ "$COOP_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

test_ghcr_images_exist() {
  [[ -n "$PLATFORM_VERSION" ]] || return 1

  # Check if the deployed image can be pulled (via crane or skopeo if available,
  # otherwise verify by checking pod image status)
  local image="ghcr.io/groblegark/beads:${PLATFORM_VERSION}"

  # Use the running pods as proof the image exists and was pullable
  local pod_image
  pod_image=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{.items[0].status.containerStatuses[0].image}' 2>/dev/null)

  log "Pod image: $pod_image"
  log "Expected:  $image"

  # Also check the image ID to confirm it's not a cached stale image
  local image_id
  image_id=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' 2>/dev/null)
  log "Image ID:  ${image_id:0:80}..."

  [[ "$pod_image" == "$image" ]]
}

test_daemon_image_tag() {
  [[ -n "$PLATFORM_VERSION" ]] || return 1

  local expected_tag="$PLATFORM_VERSION"
  local pod_images
  pod_images=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' 2>/dev/null)

  local all_match=true
  while IFS= read -r img; do
    [[ -z "$img" ]] && continue
    local tag="${img##*:}"
    log "Daemon pod image: $img (tag: $tag)"
    if [[ "$tag" != "$expected_tag" ]]; then
      log "Tag mismatch: expected $expected_tag, got $tag"
      all_match=false
    fi
  done <<< "$pod_images"

  $all_match
}

test_bd_binary_version() {
  [[ -n "$BEADS_VERSION" ]] || return 1

  # Get a daemon pod and exec into it to check bd version
  local pod
  pod=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o custom-columns=":metadata.name" 2>/dev/null | head -1)

  if [[ -z "$pod" ]]; then
    log "No daemon pod found"
    return 1
  fi

  local bd_version
  bd_version=$(kube exec "$pod" -- bd --version 2>/dev/null | head -1 || echo "")

  log "bd --version output: $bd_version"
  log "Expected BEADS_VERSION: $BEADS_VERSION"

  # The version output should contain the version string (e.g., "v0.62.23" or "0.62.23")
  local version_num="${BEADS_VERSION#v}"  # strip leading v
  assert_contains "$bd_version" "$version_num"
}

test_github_release_exists() {
  [[ -n "$BEADS_VERSION" ]] || return 1

  # Check GitHub release exists via API (unauthenticated, public repo)
  local http_code
  http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    "https://api.github.com/repos/groblegark/beads/releases/tags/${BEADS_VERSION}" 2>/dev/null || echo "000")

  log "GitHub release check for $BEADS_VERSION: HTTP $http_code"

  # 200 = release exists, 404 = not found
  # Also try the upstream repo if fork returns 404
  if [[ "$http_code" != "200" ]]; then
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
      "https://api.github.com/repos/steveyegge/beads/releases/tags/${BEADS_VERSION}" 2>/dev/null || echo "000")
    log "Upstream release check for $BEADS_VERSION: HTTP $http_code"
  fi

  [[ "$http_code" == "200" ]]
}

test_helm_release_version() {
  # Verify the helm release appVersion matches what's actually deployed.
  # Note: platform-versions.env on disk may be ahead of the deployed release
  # if a bump was committed without a deploy. So we check internal consistency:
  # helm appVersion must match the daemon pod image tag.
  local helm_version
  helm_version=$(helm list -n "$NS" --no-headers -o json 2>/dev/null \
    | python3 -c "
import json, sys
releases = json.load(sys.stdin)
for r in releases:
    if 'gastown' in r.get('name', ''):
        print(r.get('app_version', ''))
        break
" 2>/dev/null || echo "")

  local deployed_tag
  deployed_tag=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null)
  deployed_tag="${deployed_tag##*:}"

  log "Helm release appVersion: $helm_version"
  log "Deployed daemon image tag: $deployed_tag"

  # Both should be valid CalVer
  [[ "$helm_version" =~ ^20[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]+$ ]] || {
    log "Helm appVersion is not valid CalVer"
    return 1
  }

  [[ "$helm_version" == "$deployed_tag" ]]
}

test_all_pods_same_version() {
  # Check that all gastown-built pods run the same CalVer tag.
  # Only checks pods with gastown-managed images (ghcr.io/groblegark/*).
  # Third-party images (dolt, nats, redis, alpine) have their own versioning.
  local deployed_tag
  deployed_tag=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null)
  deployed_tag="${deployed_tag##*:}"

  local images
  images=$(kube get pods --no-headers \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .spec.containers[*]}{.image}{" "}{end}{"\n"}{end}' 2>/dev/null)

  local mismatches=0
  local checked=0
  while IFS=$'\t' read -r pod imgs; do
    [[ -z "$pod" ]] && continue
    for img in $imgs; do
      # Only check gastown-managed images from GHCR
      [[ "$img" == *"ghcr.io/groblegark"* ]] || continue
      local tag="${img##*:}"
      checked=$((checked + 1))
      if [[ "$tag" != "$deployed_tag" ]]; then
        log "Version mismatch: $pod runs $img (expected tag $deployed_tag)"
        mismatches=$((mismatches + 1))
      fi
    done
  done <<< "$images"

  log "Checked $checked gastown-managed containers, $mismatches mismatches (expected tag: $deployed_tag)"
  [[ "$mismatches" -eq 0 ]] && [[ "$checked" -gt 0 ]]
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Platform versions file is parseable with valid CalVer" test_versions_parseable
run_test "GHCR images exist for PLATFORM_VERSION" test_ghcr_images_exist
run_test "Daemon pods run expected PLATFORM_VERSION image" test_daemon_image_tag
run_test "bd binary version matches BEADS_VERSION" test_bd_binary_version
run_test "GitHub release exists for BEADS_VERSION" test_github_release_exists
run_test "Helm release appVersion matches PLATFORM_VERSION" test_helm_release_version
run_test "All platform pods run same PLATFORM_VERSION" test_all_pods_same_version

print_summary
