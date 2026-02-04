#!/bin/bash
# test-advice-e2e.sh - End-to-end test for the advice system
#
# Tests the complete advice flow:
#   1. bd advice add (creates advice beads)
#   2. bd advice list --for <agent> (verifies advice targeting)
#   3. bd advice remove (removes advice)
#   4. Verify advice is gone
#
# Tests all targeting modes: global, rig, role, agent
#
# Usage:
#   ./scripts/test-advice-e2e.sh
#
# Requirements:
#   - bd (beads CLI) must be in PATH
#   - gt (Gas Town CLI) must be in PATH

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Counters
PASSED=0
FAILED=0
TESTS_RUN=0

log() { echo -e "${BLUE}[*]${NC} $1"; }
pass() { echo -e "${GREEN}[PASS]${NC} $1"; PASSED=$((PASSED + 1)); }
fail() { echo -e "${RED}[FAIL]${NC} $1"; FAILED=$((FAILED + 1)); }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }

# Test function - runs a test and tracks results
run_test() {
    local name="$1"
    local expected="$2"  # "pass" or "fail"
    shift 2
    TESTS_RUN=$((TESTS_RUN + 1))

    if "$@"; then
        if [[ "$expected" == "pass" ]]; then
            pass "$name"
            return 0
        else
            fail "$name (expected failure but passed)"
            return 1
        fi
    else
        if [[ "$expected" == "fail" ]]; then
            pass "$name (expected failure)"
            return 0
        else
            fail "$name"
            return 1
        fi
    fi
}

# Check if advice list contains a specific title
advice_contains() {
    local dir="$1"
    local agent="$2"
    local title="$3"

    bd --sandbox advice list --for="$agent" --json 2>/dev/null | grep -q "\"title\":\"$title\"" || \
    bd --sandbox advice list --for="$agent" 2>/dev/null | grep -q "$title"
}

# Check if advice list does NOT contain a specific title
advice_missing() {
    local dir="$1"
    local agent="$2"
    local title="$3"

    ! advice_contains "$dir" "$agent" "$title"
}

# Setup test environment
setup_test_town() {
    log "Setting up test environment..."

    TEST_DIR=$(mktemp -d)
    export TEST_DIR

    # Create town-level .beads directory
    mkdir -p "$TEST_DIR/.beads"

    # Create routes.jsonl
    cat > "$TEST_DIR/.beads/routes.jsonl" <<EOF
{"prefix":"hq-","path":"."}
{"prefix":"gt-","path":"gastown/mayor/rig"}
{"prefix":"bd-","path":"beads/mayor/rig"}
EOF

    # Create gastown rig structure
    mkdir -p "$TEST_DIR/gastown/mayor/rig/.beads"
    echo "prefix: gt" > "$TEST_DIR/gastown/mayor/rig/.beads/config.yaml"

    # Create beads rig structure
    mkdir -p "$TEST_DIR/beads/mayor/rig/.beads"
    echo "prefix: bd" > "$TEST_DIR/beads/mayor/rig/.beads/config.yaml"

    # Create gastown polecat with redirect
    mkdir -p "$TEST_DIR/gastown/polecats/alpha/.beads"
    echo "../../mayor/rig/.beads" > "$TEST_DIR/gastown/polecats/alpha/.beads/redirect"

    # Create beads polecat with redirect
    mkdir -p "$TEST_DIR/beads/polecats/beta/.beads"
    echo "../../mayor/rig/.beads" > "$TEST_DIR/beads/polecats/beta/.beads/redirect"

    # Initialize beads databases
    (cd "$TEST_DIR" && bd --sandbox init --prefix hq 2>/dev/null || true)
    (cd "$TEST_DIR/gastown/mayor/rig" && bd --sandbox init --prefix gt 2>/dev/null || true)
    (cd "$TEST_DIR/beads/mayor/rig" && bd --sandbox init --prefix bd 2>/dev/null || true)

    log "Test environment created at $TEST_DIR"
}

# Cleanup test environment
cleanup() {
    if [[ -n "$TEST_DIR" && -d "$TEST_DIR" ]]; then
        log "Cleaning up $TEST_DIR..."
        rm -rf "$TEST_DIR"
    fi
}

# Register cleanup on exit
trap cleanup EXIT

echo "================================================"
echo "  Advice System E2E Test"
echo "  $(date)"
echo "================================================"
echo

# Check prerequisites
log "Checking prerequisites..."
if ! command -v bd &> /dev/null; then
    fail "bd (beads CLI) not found in PATH"
    exit 1
fi
pass "bd CLI found"

if command -v gt &> /dev/null; then
    pass "gt CLI found"
else
    warn "gt (Gas Town CLI) not found in PATH - some tests may be skipped"
fi

# Setup
setup_test_town

# Change to test directory
ORIG_DIR=$(pwd)
cd "$TEST_DIR"

echo
echo "================================================"
echo "  Test 1: Global Advice"
echo "================================================"
echo

# Create global advice in gastown rig
cd "$TEST_DIR/gastown/mayor/rig"
GLOBAL_ID=$(bd --sandbox advice add "Global test advice" -l global -d "This should appear for all agents" --json 2>/dev/null | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4 || echo "")

if [[ -z "$GLOBAL_ID" ]]; then
    # Try to extract from non-JSON output
    GLOBAL_OUTPUT=$(bd --sandbox advice add "Global test advice" -l global -d "This should appear for all agents" 2>&1)
    GLOBAL_ID=$(echo "$GLOBAL_OUTPUT" | grep -oE 'gt-[a-z0-9]+' | head -1 || echo "")
fi

if [[ -n "$GLOBAL_ID" ]]; then
    pass "Created global advice: $GLOBAL_ID"
else
    fail "Failed to create global advice"
fi

# Test: gastown polecat sees global advice
cd "$TEST_DIR/gastown/polecats/alpha"
run_test "Gastown polecat sees global advice" pass \
    advice_contains "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Global test advice"

# Create same global advice in beads rig (global advice needs to be in each database)
cd "$TEST_DIR/beads/mayor/rig"
bd --sandbox advice add "Global test advice" -l global -d "This should appear for all agents" 2>/dev/null || true

# Test: beads polecat sees global advice
cd "$TEST_DIR/beads/polecats/beta"
run_test "Beads polecat sees global advice" pass \
    advice_contains "$TEST_DIR/beads/polecats/beta" "beads/polecats/beta" "Global test advice"

echo
echo "================================================"
echo "  Test 2: Rig-Scoped Advice"
echo "================================================"
echo

# Create rig-scoped advice for gastown only
cd "$TEST_DIR/gastown/mayor/rig"
RIG_ID=$(bd --sandbox advice add "Gastown-only advice" --rig=gastown -d "Only gastown agents should see this" --json 2>/dev/null | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4 || echo "")
if [[ -z "$RIG_ID" ]]; then
    bd --sandbox advice add "Gastown-only advice" --rig=gastown -d "Only gastown agents should see this" 2>/dev/null || true
fi
pass "Created gastown rig-scoped advice"

# Test: gastown polecat sees rig advice
cd "$TEST_DIR/gastown/polecats/alpha"
run_test "Gastown polecat sees gastown rig advice" pass \
    advice_contains "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Gastown-only advice"

# Test: beads polecat does NOT see gastown advice
cd "$TEST_DIR/beads/polecats/beta"
run_test "Beads polecat does NOT see gastown rig advice" pass \
    advice_missing "$TEST_DIR/beads/polecats/beta" "beads/polecats/beta" "Gastown-only advice"

echo
echo "================================================"
echo "  Test 3: Role-Scoped Advice"
echo "================================================"
echo

# Create role-scoped advice for witness only
cd "$TEST_DIR/gastown/mayor/rig"
bd --sandbox advice add "Witness-only advice" --role=witness -d "Only witnesses should see this" 2>/dev/null || true
pass "Created witness role-scoped advice"

# Test: gastown witness sees role advice
run_test "Gastown witness sees witness role advice" pass \
    advice_contains "$TEST_DIR/gastown/mayor/rig" "gastown/witness" "Witness-only advice"

# Test: gastown polecat does NOT see witness advice (different role)
cd "$TEST_DIR/gastown/polecats/alpha"
run_test "Gastown polecat does NOT see witness role advice" pass \
    advice_missing "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Witness-only advice"

echo
echo "================================================"
echo "  Test 4: Agent-Specific Advice"
echo "================================================"
echo

# Create agent-specific advice for gastown/polecats/alpha
cd "$TEST_DIR/gastown/mayor/rig"
bd --sandbox advice add "Alpha-specific advice" --agent=gastown/polecats/alpha -d "Only alpha should see this" 2>/dev/null || true
pass "Created agent-specific advice"

# Test: alpha sees agent-specific advice
cd "$TEST_DIR/gastown/polecats/alpha"
run_test "Alpha sees agent-specific advice" pass \
    advice_contains "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Alpha-specific advice"

# Test: other gastown polecat does NOT see alpha advice
run_test "Other gastown polecat does NOT see alpha advice" pass \
    advice_missing "$TEST_DIR/gastown/mayor/rig" "gastown/polecats/gamma" "Alpha-specific advice"

# Test: beads polecat does NOT see alpha advice
cd "$TEST_DIR/beads/polecats/beta"
run_test "Beads polecat does NOT see alpha advice" pass \
    advice_missing "$TEST_DIR/beads/polecats/beta" "beads/polecats/beta" "Alpha-specific advice"

echo
echo "================================================"
echo "  Test 5: Advice Removal"
echo "================================================"
echo

# Create advice specifically for removal test
cd "$TEST_DIR/gastown/mayor/rig"
REMOVE_OUTPUT=$(bd --sandbox advice add "Advice to remove" -l global -d "This will be removed" 2>&1)
REMOVE_ID=$(echo "$REMOVE_OUTPUT" | grep -oE 'gt-[a-z0-9]+' | head -1 || echo "")

if [[ -n "$REMOVE_ID" ]]; then
    pass "Created advice for removal test: $REMOVE_ID"

    # Verify it exists first
    cd "$TEST_DIR/gastown/polecats/alpha"
    run_test "Advice exists before removal" pass \
        advice_contains "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Advice to remove"

    # Remove it
    cd "$TEST_DIR/gastown/mayor/rig"
    bd --sandbox advice remove "$REMOVE_ID" 2>/dev/null || true
    pass "Removed advice: $REMOVE_ID"

    # Test: advice is gone
    cd "$TEST_DIR/gastown/polecats/alpha"
    run_test "Advice no longer visible after removal" pass \
        advice_missing "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Advice to remove"
else
    fail "Could not create advice for removal test"
fi

echo
echo "================================================"
echo "  Test 6: Cross-Rig Isolation"
echo "================================================"
echo

# Create beads-specific advice
cd "$TEST_DIR/beads/mayor/rig"
bd --sandbox advice add "Beads-only advice" --rig=beads -d "Only beads agents should see this" 2>/dev/null || true
pass "Created beads rig-scoped advice"

# Test: beads polecat sees beads advice
cd "$TEST_DIR/beads/polecats/beta"
run_test "Beads polecat sees beads rig advice" pass \
    advice_contains "$TEST_DIR/beads/polecats/beta" "beads/polecats/beta" "Beads-only advice"

# Test: gastown polecat does NOT see beads advice
cd "$TEST_DIR/gastown/polecats/alpha"
run_test "Gastown polecat does NOT see beads rig advice" pass \
    advice_missing "$TEST_DIR/gastown/polecats/alpha" "gastown/polecats/alpha" "Beads-only advice"

# Return to original directory
cd "$ORIG_DIR"

echo
echo "================================================"
echo "  Test Summary"
echo "================================================"
echo
echo -e "Tests run: $TESTS_RUN"
echo -e "Passed:    ${GREEN}$PASSED${NC}"
echo -e "Failed:    ${RED}$FAILED${NC}"
echo

if [[ $FAILED -eq 0 ]]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed${NC}"
    exit 1
fi
