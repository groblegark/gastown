#!/usr/bin/env bash
# test-agent-image.sh — Verify agent image has all expected tools (no sidecar).
#
# Tests that the rich agent image contains all dev tools that were previously
# provided by the toolchain sidecar. This ensures agents can git clone,
# build Go/Python code, use LSP servers, and interact with K8s/AWS/Docker
# without any sidecar injection.
#
# Tests:
#   1.  git clone via HTTPS (libcurl-gnutls present)
#   2.  go build compiles a simple Go program
#   3.  python3 runs a script
#   4.  gopls responds to version check
#   5.  rust-analyzer responds to version check
#   6.  kubectl version works (client)
#   7.  aws --version works
#   8.  docker --version works (client only)
#   9.  make --version works
#  10.  node/npm available
#
# Usage:
#   ./scripts/test-agent-image.sh [NAMESPACE]

MODULE_NAME="agent-image"
source "$(dirname "$0")/lib.sh"

log "Testing agent image tools in namespace: $E2E_NAMESPACE"

# ── Discover agent pod ─────────────────────────────────────────────────
AGENT_POD=$(kube get pods --no-headers 2>/dev/null \
  | { grep -E "^gt-" || true; } \
  | { grep -v "Completed\|Error\|Init" || true; } \
  | head -1 | awk '{print $1}')

if [[ -z "$AGENT_POD" ]]; then
  skip_all "No agent pod found"
  exit 0
fi

log "Agent pod: $AGENT_POD"

# Discover the container name (usually "agent")
AGENT_CONTAINER=$(kube get pod "$AGENT_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)
AGENT_CONTAINER="${AGENT_CONTAINER:-agent}"
log "Container: $AGENT_CONTAINER"

# Verify the pod has NO toolchain sidecar — that's the whole point
CONTAINERS=$(kube get pod "$AGENT_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
if [[ "$CONTAINERS" == *"toolchain"* ]]; then
  log "WARNING: Pod has toolchain sidecar — this test validates sidecar-free operation"
fi

# Helper: run command inside the agent container
agent_exec() {
  kube exec "$AGENT_POD" -c "$AGENT_CONTAINER" -- "$@" 2>/dev/null
}

# ── Test 1: git clone via HTTPS ──────────────────────────────────────────
test_git_clone_https() {
  agent_exec sh -c '
    cd /tmp
    rm -rf e2e-git-clone-test
    git clone --depth 1 https://github.com/groblegark/beads.git e2e-git-clone-test
    test -f e2e-git-clone-test/go.mod
    rm -rf e2e-git-clone-test
  '
}
run_test "git clone via HTTPS works" test_git_clone_https

# ── Test 2: go build compiles a simple program ──────────────────────────
test_go_build() {
  agent_exec sh -c '
    cd /tmp
    mkdir -p e2e-go-test
    cat > e2e-go-test/main.go <<GOEOF
package main
import "fmt"
func main() { fmt.Println("hello from agent") }
GOEOF
    cd e2e-go-test
    go build -o /tmp/e2e-go-test/hello main.go
    test -x /tmp/e2e-go-test/hello
    OUTPUT=$(/tmp/e2e-go-test/hello)
    [ "$OUTPUT" = "hello from agent" ]
    rm -rf /tmp/e2e-go-test
  '
}
run_test "go build compiles a simple Go program" test_go_build

# ── Test 3: python3 runs a script ────────────────────────────────────────
test_python3() {
  local output
  output=$(agent_exec python3 -c 'print(2+2)')
  assert_eq "$output" "4"
}
run_test "python3 runs a script" test_python3

# ── Test 4: gopls version check ──────────────────────────────────────────
test_gopls() {
  # gopls may be in /usr/local/go/bin which isn't always in PATH
  agent_exec sh -c 'gopls version 2>/dev/null || /usr/local/go/bin/gopls version'
}
run_test "gopls responds to version check" test_gopls

# ── Test 5: rust-analyzer version check ──────────────────────────────────
test_rust_analyzer() {
  agent_exec rust-analyzer --version
}
run_test "rust-analyzer responds to version check" test_rust_analyzer

# ── Test 6: kubectl version (client) ─────────────────────────────────────
test_kubectl() {
  local output
  output=$(agent_exec kubectl version --client --output=json)
  assert_contains "$output" "clientVersion"
}
run_test "kubectl version works (client)" test_kubectl

# ── Test 7: aws --version ────────────────────────────────────────────────
test_aws() {
  local output
  output=$(agent_exec aws --version)
  assert_contains "$output" "aws-cli"
}
run_test "aws --version works" test_aws

# ── Test 8: docker --version (client only) ────────────────────────────────
test_docker() {
  local output
  output=$(agent_exec docker --version)
  assert_contains "$output" "Docker"
}
run_test "docker --version works (client only)" test_docker

# ── Test 9: make --version ────────────────────────────────────────────────
test_make() {
  agent_exec make --version
}
run_test "make --version works" test_make

# ── Test 10: node/npm available ───────────────────────────────────────────
test_node_npm() {
  agent_exec node --version && agent_exec npm --version
}
run_test "node and npm available" test_node_npm

# ── Summary ───────────────────────────────────────────────────────────
print_summary
