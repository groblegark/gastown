#!/usr/bin/env bash
# lib.sh — shared test helpers for E2E health test modules.
#
# Source this from each test module:
#   source "$(dirname "$0")/lib.sh"

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
DIM='\033[2m'
NC='\033[0m'

# ── Counters ─────────────────────────────────────────────────────────
_PASSED=0
_FAILED=0
_SKIPPED=0
_TOTAL=0
_MODULE_NAME="${MODULE_NAME:-unknown}"

# ── JUnit XML state ─────────────────────────────────────────────────
# When E2E_JUNIT_DIR is set, each module writes a JUnit XML file.
_JUNIT_CASES=""
_TEST_START_TIME=""

# ── Namespace ────────────────────────────────────────────────────────
# Namespace can come from:
#   1. E2E_NAMESPACE env var
#   2. First positional argument
#   3. Default: gastown-next
E2E_NAMESPACE="${E2E_NAMESPACE:-${1:-gastown-next}}"
export E2E_NAMESPACE

# ── Port forwarding state ────────────────────────────────────────────
_PF_PIDS=()

# ── Logging ──────────────────────────────────────────────────────────
log()  { echo -e "${BLUE}[$_MODULE_NAME]${NC} $*"; }
ok()   { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
skip() { echo -e "${YELLOW}[SKIP]${NC} $*"; }
dim()  { echo -e "${DIM}$*${NC}"; }

# ── Test runner ──────────────────────────────────────────────────────
# Usage: run_test "test name" command arg1 arg2 ...
run_test() {
  local name="$1"
  shift
  _TOTAL=$((_TOTAL + 1))

  local t_start t_end duration
  t_start=$(date +%s%N 2>/dev/null || date +%s)

  if "$@" 2>/dev/null; then
    ok "$name"
    _PASSED=$((_PASSED + 1))
    t_end=$(date +%s%N 2>/dev/null || date +%s)
    duration=$(_elapsed_secs "$t_start" "$t_end")
    _junit_add_pass "$name" "$duration"
  else
    fail "$name"
    _FAILED=$((_FAILED + 1))
    t_end=$(date +%s%N 2>/dev/null || date +%s)
    duration=$(_elapsed_secs "$t_start" "$t_end")
    _junit_add_fail "$name" "$duration"
  fi
  # Always return 0 so set -e doesn't abort on first failure
  return 0
}

# Usage: skip_test "test name" "reason"
skip_test() {
  local name="$1"
  local reason="${2:-}"
  _TOTAL=$((_TOTAL + 1))
  _SKIPPED=$((_SKIPPED + 1))
  skip "$name${reason:+ ($reason)}"
  _junit_add_skip "$name" "$reason"
}

# Usage: skip_all "reason" — mark entire module as skipped and print summary
skip_all() {
  local reason="${1:-}"
  skip "All tests in $_MODULE_NAME${reason:+: $reason}"
  print_summary
}

# ── Assertions ───────────────────────────────────────────────────────
assert_eq() {
  local actual="$1" expected="$2"
  [[ "$actual" == "$expected" ]]
}

assert_contains() {
  local haystack="$1" needle="$2"
  [[ "$haystack" == *"$needle"* ]]
}

assert_match() {
  local value="$1" pattern="$2"
  [[ "$value" =~ $pattern ]]
}

assert_gt() {
  local actual="$1" threshold="$2"
  [[ "$actual" -gt "$threshold" ]]
}

assert_ge() {
  local actual="$1" threshold="$2"
  [[ "$actual" -ge "$threshold" ]]
}

# ── kubectl helpers ──────────────────────────────────────────────────
kube() {
  kubectl -n "$E2E_NAMESPACE" "$@"
}

# Wait for a pod label selector to be ready. Returns 0 if ready, 1 if timeout.
wait_pod_ready() {
  local selector="$1"
  local timeout="${2:-60}"
  kubectl wait --for=condition=ready pod -l "$selector" \
    -n "$E2E_NAMESPACE" --timeout="${timeout}s" >/dev/null 2>&1
}

# Get pod name by label selector (first match)
get_pod() {
  local selector="$1"
  kube get pods -l "$selector" --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1
}

# Get pod readiness as "ready/total" (e.g. "2/2")
get_pod_ready_status() {
  local pod="$1"
  kube get pod "$pod" --no-headers -o custom-columns=":status.containerStatuses[*].ready" 2>/dev/null
}

# ── Port-forward helpers ─────────────────────────────────────────────
# Start port-forward in background. Returns local port.
# Usage: local_port=$(start_port_forward svc/my-svc 8080)
start_port_forward() {
  local target="$1"
  local remote_port="$2"
  local local_port="${3:-0}"  # 0 = auto-assign

  if [[ "$local_port" == "0" ]]; then
    # Find a free port
    local_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()')
  fi

  kubectl port-forward -n "$E2E_NAMESPACE" "$target" "${local_port}:${remote_port}" >/dev/null 2>&1 &
  local pf_pid=$!
  _PF_PIDS+=("$pf_pid")

  # Wait for port-forward to be ready (TCP connection test — works for any protocol)
  local deadline=$((SECONDS + 15))
  while [[ $SECONDS -lt $deadline ]]; do
    if (echo "" | nc -w 1 127.0.0.1 "$local_port" >/dev/null 2>&1) || \
       (python3 -c "import socket; s=socket.socket(); s.settimeout(1); s.connect(('127.0.0.1',$local_port)); s.close()" 2>/dev/null); then
      break
    fi
    # Also check that the process is still alive
    if ! kill -0 "$pf_pid" 2>/dev/null; then
      return 1
    fi
    sleep 0.5
  done

  echo "$local_port"
}

# Stop all port-forwards started by this script.
stop_port_forwards() {
  for pid in "${_PF_PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  _PF_PIDS=()
}

# ── JUnit XML helpers ────────────────────────────────────────────────
# Escape XML special characters
_xml_escape() {
  local s="$1"
  s="${s//&/&amp;}"
  s="${s//</&lt;}"
  s="${s//>/&gt;}"
  s="${s//\"/&quot;}"
  s="${s//\'/&apos;}"
  printf '%s' "$s"
}

# Compute elapsed seconds from nanosecond timestamps (or fallback to seconds)
_elapsed_secs() {
  local start="$1" end="$2"
  if [[ ${#start} -gt 10 ]]; then
    # nanoseconds
    printf '%.3f' "$(echo "($end - $start) / 1000000000" | bc -l 2>/dev/null || echo 0)"
  else
    echo "$(( end - start ))"
  fi
}

_junit_add_pass() {
  local name="$1" duration="${2:-0}"
  local ename
  ename=$(_xml_escape "$name")
  _JUNIT_CASES="${_JUNIT_CASES}    <testcase classname=\"$_MODULE_NAME\" name=\"${ename}\" time=\"${duration}\"/>\n"
}

_junit_add_fail() {
  local name="$1" duration="${2:-0}"
  local ename
  ename=$(_xml_escape "$name")
  _JUNIT_CASES="${_JUNIT_CASES}    <testcase classname=\"$_MODULE_NAME\" name=\"${ename}\" time=\"${duration}\">\n      <failure message=\"Test failed\"/>\n    </testcase>\n"
}

_junit_add_skip() {
  local name="$1" reason="${2:-}"
  local ename ereason
  ename=$(_xml_escape "$name")
  ereason=$(_xml_escape "$reason")
  _JUNIT_CASES="${_JUNIT_CASES}    <testcase classname=\"$_MODULE_NAME\" name=\"${ename}\" time=\"0\">\n      <skipped${ereason:+ message=\"${ereason}\"}/>\n    </testcase>\n"
}

# Write JUnit XML for this module (called from print_summary)
_write_junit_xml() {
  local dir="${E2E_JUNIT_DIR:-}"
  [[ -n "$dir" ]] || return 0
  mkdir -p "$dir"
  local file="${dir}/TEST-${_MODULE_NAME}.xml"
  {
    echo '<?xml version="1.0" encoding="UTF-8"?>'
    echo "<testsuites>"
    echo "  <testsuite name=\"$_MODULE_NAME\" tests=\"$_TOTAL\" failures=\"$_FAILED\" skipped=\"$_SKIPPED\" time=\"0\">"
    printf '%b' "$_JUNIT_CASES"
    echo '  </testsuite>'
    echo '</testsuites>'
  } > "$file"
}

# ── Summary ──────────────────────────────────────────────────────────
print_summary() {
  echo ""
  echo -e "${BLUE}━━━ $_MODULE_NAME summary ━━━${NC}"
  echo -e "  Total:   $_TOTAL"
  echo -e "  ${GREEN}Passed:  $_PASSED${NC}"
  if [[ $_FAILED -gt 0 ]]; then
    echo -e "  ${RED}Failed:  $_FAILED${NC}"
  fi
  if [[ $_SKIPPED -gt 0 ]]; then
    echo -e "  ${YELLOW}Skipped: $_SKIPPED${NC}"
  fi
  echo ""

  # Write JUnit XML if configured
  _write_junit_xml

  if [[ $_FAILED -gt 0 ]]; then
    return 1
  fi
  return 0
}

# ── Precondition helpers ──────────────────────────────────────────────
# Check if OAuth credentials are configured in this namespace.
# Returns 0 if the claude-credentials secret exists, 1 otherwise.
# Use this in credential test modules to skip gracefully in fresh namespaces.
credentials_configured() {
  kube get secret claude-credentials >/dev/null 2>&1
}

# Check if the broker credential pipeline is configured.
# Returns 0 if the coop-broker configmap has non-empty credential accounts.
# Use this in credential-lifecycle/refresh tests that depend on broker-managed OAuth.
broker_credentials_configured() {
  local cm
  cm=$(kube get configmap --no-headers 2>/dev/null | grep "coop-broker-config" | head -1 | awk '{print $1}')
  [[ -n "$cm" ]] || return 1
  local config
  config=$(kube get configmap "$cm" -o jsonpath='{.data.config\.json}' 2>/dev/null)
  [[ -n "$config" ]] || return 1
  # Check that credentials.accounts is non-empty
  local count
  count=$(echo "$config" | python3 -c "
import json, sys
d = json.load(sys.stdin)
accts = d.get('credentials', {}).get('accounts', [])
print(len(accts))
" 2>/dev/null)
  [[ "${count:-0}" -gt 0 ]]
}

# ── Cleanup trap ─────────────────────────────────────────────────────
_cleanup() {
  stop_port_forwards
}
trap _cleanup EXIT
