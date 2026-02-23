#!/usr/bin/env bash
# test-dispatch-rebuild.sh — E2E: verify dispatch-rebuild → push → helm-bump → deploy pipeline.
#
# Tests the full auto-rebuild pipeline triggered by a beads release dispatch:
#   1. Read current PLATFORM_VERSION from platform-versions.env
#   2. Trigger gastown-agent-rebuild dispatch via rwx CLI
#   3. Assert: PLATFORM_VERSION bumped on main (new commit in gastown)
#   4. Assert: New agent image pushed to GHCR with new CalVer tag
#   5. Assert: helm-bump committed updated tags to fics-helm-chart
#   6. Assert: deploy-rwx ran helm upgrade (check Helm revision incremented)
#   7. Assert: pods restarted and run new image version
#
# Scenario 2 is folded into scenario 1: dispatch always bumps PLATFORM_VERSION
# regardless of whether beads-version changed.
#
# This test uses the gastown-next (staging) namespace for rollout verification.
# Requires rwx CLI and RWX_ACCESS_TOKEN to be set.
#
# Usage:
#   E2E_NAMESPACE=gastown-next ./scripts/test-dispatch-rebuild.sh

set -euo pipefail
MODULE_NAME="dispatch-rebuild"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing dispatch-rebuild pipeline against namespace: $NS"

# ── Configuration ─────────────────────────────────────────────────────
GASTOWN_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
VERSIONS_FILE="$GASTOWN_ROOT/platform-versions.env"

# Timeouts (seconds)
DISPATCH_WAIT_TIMEOUT=1200   # 20 min: full build + push + deploy pipeline
DEPLOY_ROLLOUT_TIMEOUT=300   # 5 min: pod rollout after helm upgrade

# RWX workflow dispatch key
DISPATCH_KEY="gastown-agent-rebuild"

# State populated during tests
PRE_DISPATCH_VERSION=""
POST_DISPATCH_VERSION=""
PRE_HELM_REVISION=""
POST_HELM_REVISION=""
DISPATCH_RUN_ID=""

# ── Helpers ─────────────────────────────────────────────────────────

# Parse a key from platform-versions.env on main in the gastown repo.
get_live_platform_version() {
  # Fetch the file fresh from origin/main so we see post-dispatch commits.
  git -C "$GASTOWN_ROOT" fetch origin main --quiet 2>/dev/null || true
  git -C "$GASTOWN_ROOT" show origin/main:platform-versions.env 2>/dev/null \
    | grep '^PLATFORM_VERSION=' | cut -d= -f2
}

# Validate CalVer format: YYYY.MDD.N (3-part, no leading zero on month).
is_calver() {
  [[ "$1" =~ ^20[0-9]{2}\.[0-9]{1,4}\.[0-9]+$ ]]
}

# Compare CalVer versions: returns 0 if $1 > $2
calver_greater() {
  local a="$1" b="$2"
  # Split into parts: year, mdd, patch
  IFS='.' read -r ay am ap <<< "$a"
  IFS='.' read -r by bm bp <<< "$b"
  if [[ "$ay" -gt "$by" ]]; then return 0; fi
  if [[ "$ay" -lt "$by" ]]; then return 1; fi
  if [[ "$am" -gt "$bm" ]]; then return 0; fi
  if [[ "$am" -lt "$bm" ]]; then return 1; fi
  [[ "$ap" -gt "$bp" ]]
}

# Get helm release revision for the gastown release in $NS
get_helm_revision() {
  helm history "gastown-next" -n "$NS" --max 1 -o json 2>/dev/null \
    | python3 -c "import json,sys; data=json.load(sys.stdin); print(data[0]['revision'] if data else '')" 2>/dev/null \
    || echo ""
}

# Get image tag running on daemon pods in $NS
get_deployed_image_tag() {
  kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null \
    | sed 's/.*://'
}

# Check rwx CLI and token are available
check_rwx_available() {
  command -v rwx >/dev/null 2>&1 || { log "rwx CLI not found in PATH"; return 1; }
  [[ -n "${RWX_ACCESS_TOKEN:-}" ]] || { log "RWX_ACCESS_TOKEN not set"; return 1; }
}

# ── Test 1: Pre-conditions ────────────────────────────────────────────

test_preconditions() {
  # Verify platform-versions.env is readable and has valid CalVer
  if [[ ! -f "$VERSIONS_FILE" ]]; then
    log "platform-versions.env not found at $VERSIONS_FILE"
    return 1
  fi

  source "$VERSIONS_FILE"
  PRE_DISPATCH_VERSION="$PLATFORM_VERSION"

  log "Current PLATFORM_VERSION: $PRE_DISPATCH_VERSION"
  log "Current BEADS_VERSION: $BEADS_VERSION"

  is_calver "$PRE_DISPATCH_VERSION" || {
    log "PLATFORM_VERSION '$PRE_DISPATCH_VERSION' is not valid CalVer (YYYY.MDD.N)"
    return 1
  }

  # Verify rwx is available (required to trigger the dispatch)
  check_rwx_available || return 1

  # Verify cluster connectivity and namespace
  kubectl cluster-info >/dev/null 2>&1 || { log "Cannot reach cluster"; return 1; }
  kubectl get ns "$NS" >/dev/null 2>&1 || { log "Namespace $NS not found"; return 1; }

  # Record pre-dispatch helm revision
  PRE_HELM_REVISION=$(get_helm_revision)
  log "Pre-dispatch helm revision: ${PRE_HELM_REVISION:-unknown}"

  # Record pre-dispatch deployed tag
  local pre_tag
  pre_tag=$(get_deployed_image_tag)
  log "Pre-dispatch deployed image tag: ${pre_tag:-unknown}"
}

run_test "Pre-conditions: versions parseable, rwx available, cluster reachable" test_preconditions

if [[ -z "$PRE_DISPATCH_VERSION" ]]; then
  skip_all "Cannot read pre-dispatch PLATFORM_VERSION"
  exit 0
fi

if ! check_rwx_available 2>/dev/null; then
  skip_test "Dispatch gastown-agent-rebuild via rwx" "rwx CLI or RWX_ACCESS_TOKEN unavailable"
  skip_test "PLATFORM_VERSION bumped on gastown main after dispatch" "dispatch not triggered"
  skip_test "New agent image pushed to GHCR with new CalVer tag" "dispatch not triggered"
  skip_test "Helm chart updated to new PLATFORM_VERSION" "dispatch not triggered"
  skip_test "Helm release revision incremented (deploy ran)" "dispatch not triggered"
  skip_test "Daemon pods running new PLATFORM_VERSION" "dispatch not triggered"
  print_summary
  exit 0
fi

# ── Test 2: Trigger dispatch ──────────────────────────────────────────

test_trigger_dispatch() {
  source "$VERSIONS_FILE"

  log "Dispatching $DISPATCH_KEY with beads-version=$BEADS_VERSION ..."

  # Dispatch with --wait so we block until the pipeline completes.
  # Use --output json to parse the result status.
  local output
  local tmpfile
  tmpfile=$(mktemp)
  if rwx run ".rwx/docker.yml" \
      --dispatch "$DISPATCH_KEY" \
      --init "beads-version=${BEADS_VERSION}" \
      --init "trigger-source=e2e-test" \
      --wait \
      --output json \
      > "$tmpfile" 2>&1; then
    output=$(cat "$tmpfile")
    rm -f "$tmpfile"
  else
    output=$(cat "$tmpfile")
    rm -f "$tmpfile"
    log "rwx dispatch command failed"
    log "Output: $(echo "$output" | tail -5)"
    # Extract run ID even on failure
    DISPATCH_RUN_ID=$(echo "$output" | grep -oE '"run_id":\s*"[^"]+"' | head -1 | grep -oE '"[^"]+"$' | tr -d '"') || true
    return 1
  fi

  log "Dispatch output (last 3 lines): $(echo "$output" | tail -3)"

  # Extract run ID for diagnostics
  DISPATCH_RUN_ID=$(echo "$output" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    print(data.get('run_id', ''))
except:
    pass
" 2>/dev/null) || true

  # Check result status
  local status
  status=$(echo "$output" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    print(data.get('result_status', data.get('status', '')))
except:
    pass
" 2>/dev/null) || true

  log "Dispatch result status: ${status:-unknown}"
  log "Dispatch run ID: ${DISPATCH_RUN_ID:-unknown}"

  [[ "$status" == "succeeded" ]]
}

run_test "Dispatch gastown-agent-rebuild via rwx" test_trigger_dispatch

# ── Test 3: PLATFORM_VERSION bumped on main ──────────────────────────

test_platform_version_bumped() {
  # Fetch fresh from origin/main to get the post-dispatch commit
  POST_DISPATCH_VERSION=$(get_live_platform_version)

  log "Pre-dispatch:  $PRE_DISPATCH_VERSION"
  log "Post-dispatch: ${POST_DISPATCH_VERSION:-<not found>}"

  is_calver "${POST_DISPATCH_VERSION:-}" || {
    log "Post-dispatch PLATFORM_VERSION '${POST_DISPATCH_VERSION:-}' is not valid CalVer"
    return 1
  }

  calver_greater "$POST_DISPATCH_VERSION" "$PRE_DISPATCH_VERSION" || {
    log "PLATFORM_VERSION not incremented: $POST_DISPATCH_VERSION <= $PRE_DISPATCH_VERSION"
    return 1
  }

  log "PLATFORM_VERSION bumped: $PRE_DISPATCH_VERSION → $POST_DISPATCH_VERSION"
}

run_test "PLATFORM_VERSION bumped on gastown main after dispatch" test_platform_version_bumped

# ── Test 4: New image pushed to GHCR ─────────────────────────────────

test_image_pushed() {
  [[ -n "${POST_DISPATCH_VERSION:-}" ]] || {
    log "No post-dispatch version available"
    return 1
  }

  local image="ghcr.io/groblegark/gastown/gastown-agent:${POST_DISPATCH_VERSION}"
  log "Checking GHCR image: $image"

  # Pull just the manifest (not the full image) via crane or skopeo.
  # Fall back to checking if the deployed tag matches (the pod pulled it successfully).
  if command -v crane >/dev/null 2>&1; then
    crane manifest "$image" >/dev/null 2>&1 && {
      log "Image manifest found: $image"
      return 0
    }
    log "crane manifest check failed for $image (may need auth)"
  fi

  # Verify by checking the running pod image — if the pod is running it,
  # the image was successfully pulled from GHCR.
  local pod_tag
  pod_tag=$(get_deployed_image_tag)
  log "Currently deployed tag: ${pod_tag:-unknown}"

  if [[ "$pod_tag" == "$POST_DISPATCH_VERSION" ]]; then
    log "Pods running new image tag $POST_DISPATCH_VERSION — push confirmed"
    return 0
  fi

  # The pipeline ran and PLATFORM_VERSION was bumped; check GHCR via API.
  # ghcr.io supports unauthenticated manifest HEAD for public repos.
  local http_code
  http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 \
    -H "Accept: application/vnd.oci.image.manifest.v1+json" \
    "https://ghcr.io/v2/groblegark/gastown/gastown-agent/manifests/${POST_DISPATCH_VERSION}" \
    2>/dev/null || echo "000")
  log "GHCR manifest HTTP status for ${POST_DISPATCH_VERSION}: $http_code"

  # 200 or 401 (auth required but tag exists) both mean the image is there.
  # 404 means the tag doesn't exist.
  [[ "$http_code" == "200" || "$http_code" == "401" ]]
}

run_test "New agent image pushed to GHCR with new CalVer tag" test_image_pushed

# ── Test 5: Helm chart updated ────────────────────────────────────────

test_helm_chart_updated() {
  [[ -n "${POST_DISPATCH_VERSION:-}" ]] || {
    log "No post-dispatch version available"
    return 1
  }

  # Check helm release appVersion reflects the new PLATFORM_VERSION.
  # This confirms helm-bump committed and deploy-rwx applied the chart update.
  local helm_app_version
  helm_app_version=$(helm list -n "$NS" --no-headers -o json 2>/dev/null \
    | python3 -c "
import json, sys
try:
    releases = json.load(sys.stdin)
    for r in releases:
        if 'gastown' in r.get('name', ''):
            print(r.get('app_version', ''))
            break
except:
    pass
" 2>/dev/null || echo "")

  log "Helm release appVersion: ${helm_app_version:-unknown}"
  log "Expected: $POST_DISPATCH_VERSION"

  [[ "$helm_app_version" == "$POST_DISPATCH_VERSION" ]] || {
    log "Helm appVersion mismatch: got '${helm_app_version}' want '$POST_DISPATCH_VERSION'"
    return 1
  }
}

run_test "Helm chart updated to new PLATFORM_VERSION" test_helm_chart_updated

# ── Test 6: Helm revision incremented ────────────────────────────────

test_helm_revision_incremented() {
  POST_HELM_REVISION=$(get_helm_revision)
  log "Pre-dispatch helm revision:  ${PRE_HELM_REVISION:-unknown}"
  log "Post-dispatch helm revision: ${POST_HELM_REVISION:-unknown}"

  [[ -n "$POST_HELM_REVISION" ]] || {
    log "Could not determine post-dispatch helm revision"
    return 1
  }

  # Revision must have incremented (deploy ran at least once)
  if [[ -n "$PRE_HELM_REVISION" ]]; then
    [[ "$POST_HELM_REVISION" -gt "$PRE_HELM_REVISION" ]] || {
      log "Helm revision did not increment: before=$PRE_HELM_REVISION after=$POST_HELM_REVISION"
      return 1
    }
    log "Helm revision incremented: $PRE_HELM_REVISION → $POST_HELM_REVISION"
  else
    log "No pre-dispatch revision recorded; revision is now $POST_HELM_REVISION"
  fi
}

run_test "Helm release revision incremented (deploy-rwx ran)" test_helm_revision_incremented

# ── Test 7: Pods running new version ─────────────────────────────────

test_pods_running_new_version() {
  [[ -n "${POST_DISPATCH_VERSION:-}" ]] || {
    log "No post-dispatch version available"
    return 1
  }

  log "Waiting up to ${DEPLOY_ROLLOUT_TIMEOUT}s for pods to run $POST_DISPATCH_VERSION ..."

  local deadline=$((SECONDS + DEPLOY_ROLLOUT_TIMEOUT))
  while [[ $SECONDS -lt $deadline ]]; do
    local current_tag
    current_tag=$(get_deployed_image_tag)
    if [[ "$current_tag" == "$POST_DISPATCH_VERSION" ]]; then
      log "Daemon pods now running $POST_DISPATCH_VERSION"
      break
    fi
    log "Waiting: current tag is ${current_tag:-unknown} (want $POST_DISPATCH_VERSION)"
    sleep 15
  done

  # Final check — all daemon pods must run the new tag
  local pod_images
  pod_images=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' 2>/dev/null)

  if [[ -z "$pod_images" ]]; then
    log "No daemon pods found in $NS"
    return 1
  fi

  local mismatches=0
  while IFS= read -r img; do
    [[ -z "$img" ]] && continue
    local tag="${img##*:}"
    log "Daemon pod image: $img"
    if [[ "$tag" != "$POST_DISPATCH_VERSION" ]]; then
      log "Version mismatch: got $tag want $POST_DISPATCH_VERSION"
      mismatches=$((mismatches + 1))
    fi
  done <<< "$pod_images"

  # Also check that pods are Ready
  kube wait --for=condition=Ready pods -l "app.kubernetes.io/component=daemon" \
    --timeout=60s 2>/dev/null || {
    log "Daemon pods not Ready after version check"
    return 1
  }

  [[ "$mismatches" -eq 0 ]]
}

run_test "Daemon pods running new PLATFORM_VERSION" test_pods_running_new_version

# ── Summary ──────────────────────────────────────────────────────────
if [[ -n "$DISPATCH_RUN_ID" ]]; then
  log "Dispatch run ID: $DISPATCH_RUN_ID"
fi
print_summary
