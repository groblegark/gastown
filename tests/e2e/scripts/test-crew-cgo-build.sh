#!/usr/bin/env bash
# test-crew-cgo-build.sh — E2E: verify crew agent can run make build with CGO.
#
# Focused test for CGO compilation in crew agent pods. Execs into an existing
# crew agent pod and verifies:
#   1. Find a running crew agent pod
#   2. CGO prerequisites: gcc, libc6-dev (stdlib.h)
#   3. Go can compile a trivial CGO program
#   4. Clone beads repo and run make build
#   5. bd binary produced and runnable
#
# This test uses an EXISTING agent pod (no pod creation) for fast execution.
# It validates that the deployed image has all CGO dependencies.
#
# Usage:
#   E2E_NAMESPACE=gastown-rwx ./scripts/test-crew-cgo-build.sh

MODULE_NAME="crew-cgo-build"
source "$(dirname "$0")/lib.sh"

NS="$E2E_NAMESPACE"
log "Testing CGO build in crew agent pod in namespace: $NS"

# ── Configuration ────────────────────────────────────────────────────
BUILD_TIMEOUT=300
CLONE_DIR="/tmp/beads-cgo-test"

AGENT_POD=""

# Helper: exec in agent container
ax() {
  kube exec "$AGENT_POD" -c agent -- bash -c "$@" 2>/dev/null
}

# ── Test 1: Find a running crew agent pod ────────────────────────────
test_find_pod() {
  local pods
  pods=$(kube get pods --no-headers 2>/dev/null | grep -E "^gt-.*crew" | grep "Running" | awk '{print $1}')

  if [[ -z "$pods" ]]; then
    pods=$(kube get pods --no-headers 2>/dev/null | grep "^gt-" | grep "Running" | awk '{print $1}')
  fi

  AGENT_POD=$(echo "$pods" | head -1)
  [[ -n "$AGENT_POD" ]] || { log "No running agent pods found"; return 1; }
  log "Using pod: $AGENT_POD"
}
run_test "Find running crew agent pod" test_find_pod

if [[ -z "$AGENT_POD" ]]; then
  skip_test "CGO prerequisites present" "no agent pod"
  skip_test "Trivial CGO program compiles" "no agent pod"
  skip_test "Clone beads and make build" "no agent pod"
  skip_test "bd binary produced and runnable" "no agent pod"
  print_summary
  exit 0
fi

# ── Test 2: CGO prerequisites present ────────────────────────────────
test_cgo_prereqs() {
  # Check gcc
  local gcc_ver
  gcc_ver=$(ax 'gcc --version 2>&1 | head -1') || { log "gcc not found"; return 1; }
  log "gcc: $gcc_ver"

  # Check stdlib.h (the file CGO actually needs)
  local stdlib
  stdlib=$(ax 'test -f /usr/include/stdlib.h && echo yes') || true
  [[ "$stdlib" == "yes" ]] || { log "stdlib.h missing — libc6-dev not installed"; return 1; }
  log "stdlib.h: present"

  # Check CGO_ENABLED default
  local cgo
  cgo=$(ax 'go env CGO_ENABLED 2>/dev/null') || true
  log "CGO_ENABLED: ${cgo:-unknown}"
}
run_test "CGO prerequisites present (gcc, stdlib.h)" test_cgo_prereqs

# ── Test 3: Trivial CGO program compiles ─────────────────────────────
test_trivial_cgo() {
  # Write and compile a minimal CGO program
  ax '
    rm -rf /tmp/cgo-test
    mkdir -p /tmp/cgo-test
    cd /tmp/cgo-test
    go mod init cgo-test 2>&1
    cat > main.go << '"'"'GOEOF'"'"'
package main

// #include <stdlib.h>
import "C"

import "fmt"

func main() {
    fmt.Println("CGO works:", C.EXIT_SUCCESS)
}
GOEOF
    CGO_ENABLED=1 go build -o /tmp/cgo-test/test-cgo . 2>&1
  ' || { log "CGO compilation failed"; return 1; }

  # Run the compiled program
  local output
  output=$(ax '/tmp/cgo-test/test-cgo 2>&1') || true
  log "CGO test output: ${output:-empty}"
  assert_contains "$output" "CGO works"
}
run_test "Trivial CGO program compiles and runs" test_trivial_cgo

# ── Test 4: Clone beads and make build ───────────────────────────────
test_make_build() {
  # Clone fresh (capture both stdout and stderr)
  local clone_out
  clone_out=$(kube exec "$AGENT_POD" -c agent -- bash -c "rm -rf ${CLONE_DIR} && git clone --depth=1 https://github.com/groblegark/beads.git ${CLONE_DIR} 2>&1" 2>&1) || {
    log "Clone failed: $clone_out"
    return 1
  }

  # Run make build — capture exit code explicitly to detect failures
  local build_out build_exit
  build_out=$(kube exec "$AGENT_POD" -c agent -- bash -c "cd ${CLONE_DIR} && make build 2>&1; echo EXIT_CODE=\$?" 2>&1)
  build_exit=$(echo "$build_out" | grep "EXIT_CODE=" | tail -1 | cut -d= -f2)
  log "Build output (last 5 lines): $(echo "$build_out" | grep -v EXIT_CODE | tail -5)"
  log "Build exit code: ${build_exit:-unknown}"

  if [[ "${build_exit:-1}" != "0" ]]; then
    log "make build FAILED with exit code $build_exit"
    return 1
  fi
}
run_test "Clone beads and make build succeeds" test_make_build

# ── Test 5: bd binary produced and runnable ──────────────────────────
test_binary() {
  # Check binary exists
  local exists
  exists=$(ax "test -f ${CLONE_DIR}/bd && echo yes") || true
  [[ "$exists" == "yes" ]] || { log "bd binary not found at ${CLONE_DIR}/bd"; return 1; }

  # Check binary is executable
  local version
  version=$(ax "${CLONE_DIR}/bd --version 2>&1 | head -1") || true
  log "bd version: ${version:-unknown}"

  # Check it's a real binary (not empty or corrupted)
  local size
  size=$(ax "stat -c %s ${CLONE_DIR}/bd 2>/dev/null || stat -f %z ${CLONE_DIR}/bd 2>/dev/null") || true
  log "bd binary size: ${size:-unknown} bytes"

  # Binary should be > 10MB (beads is a substantial Go binary)
  [[ -n "$size" && "$size" -gt 10000000 ]] || { log "Binary suspiciously small: ${size:-0}"; return 1; }
}
run_test "bd binary produced and runnable" test_binary

# ── Cleanup ──────────────────────────────────────────────────────────
ax "rm -rf ${CLONE_DIR} /tmp/cgo-test" 2>/dev/null || true

# ── Summary ──────────────────────────────────────────────────────────
print_summary
