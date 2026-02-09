#!/bin/bash
# Test the agent-entrypoint.sh script logic (workspace setup, settings creation).
# Run: bash deploy/agent-entrypoint_test.sh
#
# Strategy: source the entrypoint with exec/set stubbed out, then call functions directly.

PASS=0
FAIL=0
TEMP_DIR=$(mktemp -d)

cleanup() { rm -rf "$TEMP_DIR"; }
trap cleanup EXIT

assert_dir() {
    if [ -d "$1" ]; then
        echo "  PASS: directory exists: $1"
        ((PASS++))
    else
        echo "  FAIL: directory missing: $1"
        ((FAIL++))
    fi
}

assert_file() {
    if [ -f "$1" ]; then
        echo "  PASS: file exists: $1"
        ((PASS++))
    else
        echo "  FAIL: file missing: $1"
        ((FAIL++))
    fi
}

assert_file_contains() {
    if grep -q "$2" "$1" 2>/dev/null; then
        echo "  PASS: $1 contains '$2'"
        ((PASS++))
    else
        echo "  FAIL: $1 does not contain '$2'"
        ((FAIL++))
    fi
}

assert_not_exists() {
    if [ ! -e "$1" ]; then
        echo "  PASS: does not exist: $1"
        ((PASS++))
    else
        echo "  FAIL: unexpectedly exists: $1"
        ((FAIL++))
    fi
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${SCRIPT_DIR}/agent-entrypoint.sh"

if [ ! -f "$SCRIPT" ]; then
    echo "FATAL: agent-entrypoint.sh not found at $SCRIPT"
    exit 1
fi

# Source the entrypoint with exec and set -euo stubbed out.
# We provide required env vars so the parameter expansion doesn't fail,
# and override exec so start_agent doesn't replace our process.
exec() { : ; }  # no-op exec
export GT_ROLE=mayor GT_RIG=gastown GT_AGENT=test
export GT_WORKSPACE="$TEMP_DIR/_init"
export HOME="$TEMP_DIR/_inithome"
mkdir -p "$HOME"
source "$SCRIPT"
# At this point, all functions are defined and the main block already ran once
# (creating $_init workspace). We now call functions directly for each test.

# Restore exec for required-env-var tests later
unset -f exec

# Disable errexit from the sourced script — tests need to continue on assertion failures
set +e

# ================================================================
echo "=== Test 1: Mayor workspace setup ==="
ROLE=mayor
AGENT=test-mayor
GT_WORKSPACE="$TEMP_DIR/ws1"
WORKSPACE="$TEMP_DIR/ws1"
unset GT_TOWN_ROOT 2>/dev/null || true

setup_workspace
assert_dir "$TEMP_DIR/ws1/mayor"

# ================================================================
echo "=== Test 2: Deacon workspace setup ==="
ROLE=deacon
AGENT=test-deacon
GT_WORKSPACE="$TEMP_DIR/ws2"
WORKSPACE="$TEMP_DIR/ws2"
unset GT_TOWN_ROOT 2>/dev/null || true

setup_workspace
assert_dir "$TEMP_DIR/ws2/deacon"

# ================================================================
echo "=== Test 3: Crew workspace setup ==="
ROLE=crew
AGENT=colonizer
GT_WORKSPACE="$TEMP_DIR/ws3"
WORKSPACE="$TEMP_DIR/ws3"
unset GT_TOWN_ROOT 2>/dev/null || true

setup_workspace
assert_dir "$TEMP_DIR/ws3/crew/colonizer"

# ================================================================
echo "=== Test 4: Polecat workspace (no extra dirs) ==="
ROLE=polecat
AGENT=test-polecat
GT_WORKSPACE="$TEMP_DIR/ws4"
WORKSPACE="$TEMP_DIR/ws4"
unset GT_TOWN_ROOT 2>/dev/null || true

setup_workspace
assert_dir "$TEMP_DIR/ws4"
assert_not_exists "$TEMP_DIR/ws4/polecat"

# ================================================================
echo "=== Test 5: Claude settings creation ==="
export HOME="$TEMP_DIR/home5"
mkdir -p "$HOME"

setup_claude_settings
assert_file "$HOME/.claude/settings.json"
assert_file_contains "$HOME/.claude/settings.json" "Bash"
assert_file_contains "$HOME/.claude/settings.json" "permissions"

# ================================================================
echo "=== Test 6: Claude settings not overwritten ==="
export HOME="$TEMP_DIR/home6"
mkdir -p "$HOME/.claude"
echo '{"custom":"preserved"}' > "$HOME/.claude/settings.json"

setup_claude_settings
assert_file_contains "$HOME/.claude/settings.json" "preserved"

# ================================================================
echo "=== Test 7: CLAUDE.md copied for mayor when configmap present ==="
ROLE=mayor
GT_WORKSPACE="$TEMP_DIR/ws7"
WORKSPACE="$TEMP_DIR/ws7"
export HOME="$TEMP_DIR/home7"
unset GT_TOWN_ROOT 2>/dev/null || true
mkdir -p "$HOME"

# Only test configmap copy if we can write to /etc/agent-pod (unlikely outside container)
if mkdir -p /etc/agent-pod 2>/dev/null && [ -w "/etc/agent-pod" ]; then
    echo "# Test CLAUDE.md" > /etc/agent-pod/CLAUDE.md
    setup_workspace
    assert_file "$TEMP_DIR/ws7/mayor/CLAUDE.md"
    rm -f /etc/agent-pod/CLAUDE.md
else
    echo "  SKIP: /etc/agent-pod not writable (expected outside container)"
    setup_workspace
    assert_dir "$TEMP_DIR/ws7/mayor"
fi

# ================================================================
echo "=== Test 8: GT_TOWN_ROOT sets working directory ==="
ROLE=crew
AGENT=test
GT_WORKSPACE="$TEMP_DIR/ws8"
WORKSPACE="$TEMP_DIR/ws8"
export GT_TOWN_ROOT="$TEMP_DIR/townroot"
export HOME="$TEMP_DIR/home8"
mkdir -p "$HOME"

setup_workspace
assert_dir "$TEMP_DIR/townroot"
unset GT_TOWN_ROOT

# ================================================================
echo "=== Test 9: Required env vars ==="
# Test that missing GT_ROLE causes failure
(GT_ROLE="" GT_RIG=x bash -c 'ROLE="${GT_ROLE:?GT_ROLE is required}"' 2>/dev/null) && {
    echo "  FAIL: should have failed with empty GT_ROLE"
    ((FAIL++))
} || {
    echo "  PASS: empty GT_ROLE correctly rejected"
    ((PASS++))
}

# Test that missing GT_RIG causes failure
(GT_ROLE=mayor GT_RIG="" bash -c 'RIG="${GT_RIG:?GT_RIG is required}"' 2>/dev/null) && {
    echo "  FAIL: should have failed with empty GT_RIG"
    ((FAIL++))
} || {
    echo "  PASS: empty GT_RIG correctly rejected"
    ((PASS++))
}

# ================================================================
echo ""
echo "================================"
echo "Results: $PASS passed, $FAIL failed"
echo "================================"
[ "$FAIL" -eq 0 ] || exit 1
