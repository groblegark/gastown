#!/usr/bin/env bash
# test-crew-image-prereqs.sh — Verify agent image has all crew prerequisites.
#
# Quick smoke test that execs into an existing crew agent pod and checks
# all tools and env vars required for the build-test-push cycle. No pods
# are created — this uses whatever is already running.
#
# Tests:
#   1. Find a running crew agent pod
#   2. go is installed (correct version)
#   3. gcc/g++ present (CGO compiler)
#   4. libc6-dev present (stdlib.h for CGO)
#   5. gh CLI present
#   6. rwx CLI present
#   7. git present
#   8. make present
#   9. GIT_TOKEN env var set
#  10. RWX_ACCESS_TOKEN env var set
#  11. GH_TOKEN env var set
#  12. Image tag matches expected platform version (if E2E_EXPECTED_IMAGE set)
#
# Usage:
#   E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-image-prereqs.sh
#   E2E_EXPECTED_IMAGE=2026.222.4 E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-image-prereqs.sh

MODULE_NAME="crew-image-prereqs"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing crew image prerequisites in namespace: $NS"

# ── Discover crew agent pod ──────────────────────────────────────────
AGENT_POD=""
AGENT_IMAGE=""

test_find_crew_pod() {
  # Look for crew pods (gt-*-crew-* pattern)
  local pods
  pods=$(kube get pods --no-headers 2>/dev/null | grep -E "^gt-.*crew" | grep "Running" | awk '{print $1}')

  if [[ -z "$pods" ]]; then
    # Fall back to any gt-* agent pod
    pods=$(kube get pods --no-headers 2>/dev/null | grep "^gt-" | grep "Running" | awk '{print $1}')
  fi

  AGENT_POD=$(echo "$pods" | head -1)
  [[ -n "$AGENT_POD" ]] || { log "No running agent pods found"; return 1; }

  AGENT_IMAGE=$(kube get pod "$AGENT_POD" -o jsonpath='{.spec.containers[?(@.name=="agent")].image}' 2>/dev/null)
  log "Using pod: $AGENT_POD (image: ${AGENT_IMAGE:-unknown})"
}
run_test "Find running crew agent pod" test_find_crew_pod

if [[ -z "$AGENT_POD" ]]; then
  skip_test "go installed" "no agent pod"
  skip_test "gcc/g++ present" "no agent pod"
  skip_test "libc6-dev present (stdlib.h)" "no agent pod"
  skip_test "gh CLI present" "no agent pod"
  skip_test "rwx CLI present" "no agent pod"
  skip_test "git present" "no agent pod"
  skip_test "make present" "no agent pod"
  skip_test "GIT_TOKEN set" "no agent pod"
  skip_test "RWX_ACCESS_TOKEN set" "no agent pod"
  skip_test "GH_TOKEN set" "no agent pod"
  skip_test "Image version matches expected" "no agent pod"
  print_summary
  exit 0
fi

# Helper: exec in agent container
ax() {
  kube exec "$AGENT_POD" -c agent -- bash -c "$@" 2>/dev/null
}

# ── Test 2: go installed ─────────────────────────────────────────────
test_go() {
  local ver
  ver=$(ax 'go version 2>&1') || { log "go not found"; return 1; }
  log "go: $ver"
  assert_contains "$ver" "go1."
}
run_test "go installed" test_go

# ── Test 3: gcc/g++ present ──────────────────────────────────────────
test_gcc() {
  local gcc_ver
  gcc_ver=$(ax 'gcc --version 2>&1 | head -1') || { log "gcc not found"; return 1; }
  log "gcc: $gcc_ver"
  [[ -n "$gcc_ver" ]]
}
run_test "gcc/g++ present (CGO compiler)" test_gcc

# ── Test 4: libc6-dev present ────────────────────────────────────────
test_libc6_dev() {
  # Check for stdlib.h — the actual file CGO needs
  local has_stdlib
  has_stdlib=$(ax 'test -f /usr/include/stdlib.h && echo yes') || true

  if [[ "$has_stdlib" == "yes" ]]; then
    log "stdlib.h found at /usr/include/stdlib.h"
    return 0
  fi

  # Also check via dpkg
  local dpkg_status
  dpkg_status=$(ax 'dpkg -l libc6-dev 2>&1 | grep "^ii"') || true

  if [[ -n "$dpkg_status" ]]; then
    log "libc6-dev: $dpkg_status"
    return 0
  fi

  log "libc6-dev NOT installed — stdlib.h missing (CGO builds will fail)"
  return 1
}
run_test "libc6-dev present (stdlib.h for CGO)" test_libc6_dev

# ── Test 5: gh CLI present ───────────────────────────────────────────
test_gh() {
  local ver
  ver=$(ax 'gh --version 2>&1 | head -1') || { log "gh not found"; return 1; }
  log "gh: $ver"
  assert_contains "$ver" "gh version"
}
run_test "gh CLI present" test_gh

# ── Test 6: rwx CLI present ──────────────────────────────────────────
test_rwx() {
  local ver
  ver=$(ax 'rwx --version 2>&1') || { log "rwx not found"; return 1; }
  log "rwx: $ver"
  [[ -n "$ver" ]]
}
run_test "rwx CLI present" test_rwx

# ── Test 7: git present ──────────────────────────────────────────────
test_git() {
  local ver
  ver=$(ax 'git --version 2>&1') || { log "git not found"; return 1; }
  log "git: $ver"
  assert_contains "$ver" "git version"
}
run_test "git present" test_git

# ── Test 8: make present ─────────────────────────────────────────────
test_make() {
  local ver
  ver=$(ax 'make --version 2>&1 | head -1') || { log "make not found"; return 1; }
  log "make: $ver"
  [[ -n "$ver" ]]
}
run_test "make present" test_make

# ── Test 9: GIT_TOKEN env var set ────────────────────────────────────
test_git_token() {
  local val
  val=$(ax 'test -n "$GIT_TOKEN" && echo "set"') || true
  [[ "$val" == "set" ]] || { log "GIT_TOKEN not set"; return 1; }
}
run_test "GIT_TOKEN env var set" test_git_token

# ── Test 10: RWX_ACCESS_TOKEN env var set ────────────────────────────
test_rwx_token() {
  local val
  val=$(ax 'test -n "$RWX_ACCESS_TOKEN" && echo "set"') || true
  [[ "$val" == "set" ]] || { log "RWX_ACCESS_TOKEN not set"; return 1; }
}
run_test "RWX_ACCESS_TOKEN env var set" test_rwx_token

# ── Test 11: GH_TOKEN env var set ────────────────────────────────────
test_gh_token() {
  local val
  val=$(ax 'test -n "$GH_TOKEN" && echo "set"') || true
  [[ "$val" == "set" ]] || { log "GH_TOKEN not set"; return 1; }
}
run_test "GH_TOKEN env var set" test_gh_token

# ── Test 12: Image version matches expected ──────────────────────────
EXPECTED_IMAGE="${E2E_EXPECTED_IMAGE:-}"

if [[ -z "$EXPECTED_IMAGE" ]]; then
  skip_test "Image version matches expected" "E2E_EXPECTED_IMAGE not set"
else
  test_image_version() {
    [[ -n "$AGENT_IMAGE" ]] || { log "Could not determine image"; return 1; }
    if [[ "$AGENT_IMAGE" == *"$EXPECTED_IMAGE"* ]]; then
      log "Image $AGENT_IMAGE matches expected $EXPECTED_IMAGE"
      return 0
    fi
    log "Image mismatch: running $AGENT_IMAGE, expected tag $EXPECTED_IMAGE"
    return 1
  }
  run_test "Image version matches expected ($EXPECTED_IMAGE)" test_image_version
fi

# ── Summary ──────────────────────────────────────────────────────────
print_summary
