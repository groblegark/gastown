#!/usr/bin/env bash
# test-credential-hot-reload.sh — Verify credential distribution to agent sessions.
#
# Tests that the mux can push fresh credentials to running coop sessions
# WITHOUT restarting agent pods. This is the "hot reload" path:
#   broker refreshes → CredentialEvent::Refreshed → distributor → session profiles
#
# Pre-conditions:
#   - Mux has registered sessions (agent pods)
#   - Broker credentials are healthy
#
# Tests:
#   Phase 1: Infrastructure (mux sessions, broker health, agent reachability)
#   Phase 2: Credential distribute via mux API (requires merged broker+mux)
#   Phase 3: Direct session profile push (mux → agent coop /api/v1/session/profiles)
#   Phase 4: Agent health after credential operations
#
# Usage:
#   ./scripts/test-credential-hot-reload.sh [NAMESPACE]

MODULE_NAME="credential-hot-reload"
source "$(dirname "$0")/lib.sh"

log "Testing credential hot-reload in $E2E_NAMESPACE"

# ── Check credentials are configured ──────────────────────────────────
if ! broker_credentials_configured; then
  skip_all "broker credential pipeline not configured (no accounts in configmap)"
  exit 0
fi

# ── Discover components ──────────────────────────────────────────
BROKER_POD=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
AGENT_POD=$(kube get pods --no-headers 2>/dev/null | grep "^gt-" | head -1 | awk '{print $1}')

if [[ -z "$BROKER_POD" ]]; then
  skip_all "no coop-broker pod found"
  exit 0
fi

if [[ -z "$AGENT_POD" ]]; then
  skip_all "no agent pod found (gt-* prefix)"
  exit 0
fi

log "Broker pod: $BROKER_POD"
log "Agent pod: $AGENT_POD"

# ── Port-forward setup ──────────────────────────────────────────
BROKER_PORT=""
MUX_PORT=""
AGENT_PORT=""
BROKER_TOKEN="${BROKER_TOKEN:-}"

# Try to read broker token from K8s secret
if [[ -z "$BROKER_TOKEN" ]]; then
  BROKER_TOKEN=$(kube get secret coop-broker-auth-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || true)
fi

setup_broker() {
  if [[ -z "$BROKER_PORT" ]]; then
    if [[ -n "$BROKER_SVC" ]]; then
      BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 8080) || return 1
    else
      BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 8080) || return 1
    fi
  fi
}

setup_mux() {
  if [[ -z "$MUX_PORT" ]]; then
    if [[ -n "$BROKER_SVC" ]]; then
      MUX_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
    else
      MUX_PORT=$(start_port_forward "pod/$BROKER_POD" 9800) || return 1
    fi
  fi
}

setup_agent() {
  if [[ -z "$AGENT_PORT" ]]; then
    AGENT_PORT=$(start_port_forward "pod/$AGENT_POD" 8080) || return 1
  fi
}

broker_api() {
  local path="$1"
  local method="${2:-GET}"
  local data="${3:-}"
  local args=(-sf --connect-timeout 5 -X "$method")
  if [[ -n "$BROKER_TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $BROKER_TOKEN")
  fi
  args+=(-H "Content-Type: application/json")
  if [[ -n "$data" ]]; then
    args+=(-d "$data")
  fi
  curl "${args[@]}" "http://127.0.0.1:${BROKER_PORT}${path}" 2>/dev/null
}

mux_api() {
  local path="$1"
  local method="${2:-GET}"
  local data="${3:-}"
  local args=(-sf --connect-timeout 5 -X "$method")
  if [[ -n "$BROKER_TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $BROKER_TOKEN")
  fi
  args+=(-H "Content-Type: application/json")
  if [[ -n "$data" ]]; then
    args+=(-d "$data")
  fi
  curl "${args[@]}" "http://127.0.0.1:${MUX_PORT}${path}" 2>/dev/null
}

agent_api() {
  local path="$1"
  local method="${2:-GET}"
  local data="${3:-}"
  local args=(-sf --connect-timeout 5 -X "$method")
  args+=(-H "Content-Type: application/json")
  if [[ -n "$data" ]]; then
    args+=(-d "$data")
  fi
  curl "${args[@]}" "http://127.0.0.1:${AGENT_PORT}${path}" 2>/dev/null
}

# ═══════════════════════════════════════════════════════════════════
# Phase 1: Pre-conditions — mux and broker are healthy
# ═══════════════════════════════════════════════════════════════════

# ── Test 1: Mux has registered sessions ──────────────────────────
MUX_SESSIONS_JSON=""

test_mux_sessions() {
  setup_mux || return 1

  MUX_SESSIONS_JSON=$(mux_api "/api/v1/sessions")
  [[ -n "$MUX_SESSIONS_JSON" ]] || return 1

  local count
  count=$(echo "$MUX_SESSIONS_JSON" | python3 -c "
import json, sys
d = json.load(sys.stdin)
sessions = d if isinstance(d, list) else d.get('sessions', [])
print(len(sessions))
" 2>/dev/null)

  log "Mux registered sessions: $count"
  [[ "${count:-0}" -gt 0 ]]
}
run_test "Mux has at least one registered session" test_mux_sessions

# ── Test 2: Broker credentials are healthy ───────────────────────
ACCOUNT_NAME=""

test_broker_healthy() {
  # In collapsed deployment, credentials are on the mux (port 9800), not separate broker
  setup_mux || return 1

  local status
  status=$(mux_api "/api/v1/credentials/status") || true

  if [[ -z "$status" ]] || echo "$status" | grep -q '"error"'; then
    log "Could not get credential status"
    return 1
  fi

  # API returns either [{name, status, ...}] (array) or {accounts: [{...}]}
  ACCOUNT_NAME=$(echo "$status" | python3 -c "
import json, sys
d = json.load(sys.stdin)
accounts = d if isinstance(d, list) else d.get('accounts', [])
for a in accounts:
    print(a.get('name', ''))
    break
" 2>/dev/null)

  local acct_status
  acct_status=$(echo "$status" | python3 -c "
import json, sys
d = json.load(sys.stdin)
accounts = d if isinstance(d, list) else d.get('accounts', [])
for a in accounts:
    print(a.get('status', 'unknown'))
    break
" 2>/dev/null)

  log "Account: $ACCOUNT_NAME, status: $acct_status"
  [[ "$acct_status" == "healthy" ]]
  return $?
}
run_test "Broker credentials are healthy" test_broker_healthy

# ── Test 3: Agent pod coop API is reachable ──────────────────────
test_agent_coop_reachable() {
  setup_agent || return 1

  local health
  health=$(agent_api "/api/v1/health")
  [[ -n "$health" ]] || return 1

  local status
  status=$(echo "$health" | python3 -c "import json,sys; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)
  log "Agent coop status: $status"
  [[ "$status" == "running" ]]
}
run_test "Agent pod coop API is reachable" test_agent_coop_reachable

# ═══════════════════════════════════════════════════════════════════
# Phase 2: Mux credential distribute (requires merged broker+mux)
# ═══════════════════════════════════════════════════════════════════

# ── Test 4: Mux has credential broker configured ────────────────
MUX_HAS_BROKER=false

test_mux_credential_broker() {
  setup_mux || return 1

  local status
  status=$(mux_api "/api/v1/credentials/status" 2>/dev/null) || true

  if [[ -z "$status" ]] || echo "$status" | grep -q "credential broker not configured"; then
    log "Mux does not have credential broker embedded (separate containers)"
    return 1
  fi

  MUX_HAS_BROKER=true
  log "Mux has embedded credential broker"
  return 0
}
run_test "Mux has credential broker embedded (bd-ib85z)" test_mux_credential_broker

# ── Test 5: Distribute credentials via mux API ──────────────────
test_distribute() {
  if [[ "$MUX_HAS_BROKER" != "true" ]]; then
    log "Skipping: mux doesn't have embedded broker (bd-ib85z required)"
    return 1
  fi

  setup_mux || return 1
  ACCOUNT_NAME="${ACCOUNT_NAME:-claude-max}"

  # Use -s (not -sf) so we get error response bodies
  local args=(-s --connect-timeout 5 -X POST)
  if [[ -n "$BROKER_TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $BROKER_TOKEN")
  fi
  args+=(-H "Content-Type: application/json")
  args+=(-d "{\"account\":\"$ACCOUNT_NAME\",\"switch\":true}")

  local result
  result=$(curl "${args[@]}" "http://127.0.0.1:${MUX_PORT}/api/v1/credentials/distribute" 2>/dev/null)

  if [[ -z "$result" ]]; then
    log "Distribute call returned empty"
    return 1
  fi

  # Check for error response (e.g. "no credentials available")
  if echo "$result" | grep -q '"error"'; then
    local err_msg
    err_msg=$(echo "$result" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(d.get('error',{}).get('message', str(d)))
" 2>/dev/null)
    log "Distribute error: $err_msg"
    return 1
  fi

  log "Distribute result: $result"

  local distributed
  distributed=$(echo "$result" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(d.get('distributed', False))
" 2>/dev/null)

  [[ "$distributed" == "True" ]]
}
run_test "Distribute credentials to sessions via mux API" test_distribute

# ═══════════════════════════════════════════════════════════════════
# Phase 3: Direct session profile push (always testable)
# ═══════════════════════════════════════════════════════════════════

# ── Test 6: Push a test profile directly to agent coop ───────────
test_direct_profile_push() {
  setup_agent || return 1

  # Push a dummy profile to the agent's coop instance.
  # This tests the POST /api/v1/session/profiles endpoint that the
  # mux distributor would call. We use a test profile name to avoid
  # interfering with active credentials.
  local result
  result=$(agent_api "/api/v1/session/profiles" "POST" \
    '{"profiles":[{"name":"_e2e_test_","credentials":{"ANTHROPIC_API_KEY":"sk-test-dummy"}}]}')

  if [[ -z "$result" ]]; then
    # Older coop versions may not support the profiles endpoint
    log "Profiles endpoint not available (older coop version)"
    dim "  This endpoint is required for hot-reload credential distribution"
    return 1
  fi

  log "Profile push result: $result"
  return 0
}
run_test "Push test profile directly to agent coop session" test_direct_profile_push

# ── Test 7: Agent coop lists profiles ────────────────────────────
test_list_profiles() {
  setup_agent || return 1

  local profiles
  profiles=$(agent_api "/api/v1/session/profiles")
  if [[ -z "$profiles" ]]; then
    log "Profiles list endpoint not available"
    return 1
  fi

  log "Agent profiles: $profiles"
  # Check that our test profile was accepted
  if echo "$profiles" | grep -q "_e2e_test_"; then
    log "Test profile found in agent profiles list"
    return 0
  fi

  log "Test profile not found (may have been rejected)"
  return 1
}
run_test "Agent coop lists registered profiles" test_list_profiles

# ═══════════════════════════════════════════════════════════════════
# Phase 4: Post-condition checks
# ═══════════════════════════════════════════════════════════════════

# ── Test 8: Agent pod coop still healthy ─────────────────────────
test_agent_still_healthy() {
  setup_agent || return 1

  local health
  health=$(agent_api "/api/v1/health")
  [[ -n "$health" ]] || return 1

  local status
  status=$(echo "$health" | python3 -c "import json,sys; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)
  log "Agent coop status after profile ops: $status"
  [[ "$status" == "running" ]]
}
run_test "Agent pod coop API still healthy after profile operations" test_agent_still_healthy

# ── Test 9: Mux sessions intact ─────────────────────────────────
test_mux_sessions_intact() {
  setup_mux || return 1

  local sessions
  sessions=$(mux_api "/api/v1/sessions")
  [[ -n "$sessions" ]] || return 1

  local count
  count=$(echo "$sessions" | python3 -c "
import json, sys
d = json.load(sys.stdin)
sessions = d if isinstance(d, list) else d.get('sessions', [])
print(len(sessions))
" 2>/dev/null)

  log "Mux sessions after operations: $count"
  [[ "${count:-0}" -gt 0 ]]
}
run_test "Mux sessions remain registered after operations" test_mux_sessions_intact

# ── Summary ──────────────────────────────────────────────────────
print_summary
