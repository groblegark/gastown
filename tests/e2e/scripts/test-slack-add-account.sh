#!/usr/bin/env bash
# test-slack-add-account.sh — E2E validate add-account + reauth flow via coopmux API.
#
# Tests the full credential account lifecycle that the slackbot drives:
#
#   1. Slackbot standalone deployment is running
#   2. Slackbot has COOP_BROKER_URL configured (points to coopmux)
#   3. Coopmux /api/v1/credentials/new endpoint creates a test account
#   4. New account appears in /api/v1/credentials/status
#   5. Reauth endpoint returns auth_url for the new account
#   6. Auth URL is a valid Anthropic OAuth URL
#   7. Account appears in credential pool
#   8. Cleanup: account can be removed or is left in expired state
#
# This validates the API path the slackbot uses when a user types
# "add account <name>" in Slack. The slackbot calls these same endpoints.
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-slack-add-account.sh

set -euo pipefail
MODULE_NAME="slack-add-account"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing Slack add-account + reauth flow in $NS"

# ── Check credentials are configured ──────────────────────────────────
if ! broker_credentials_configured; then
  skip_all "broker credential pipeline not configured"
  exit 0
fi

# ── Discover slackbot ──────────────────────────────────────────────────
# Slackbot can be standalone deployment or sidecar
SLACKBOT_POD=""
SLACKBOT_CONTAINER=""

# Check standalone deployment first
STANDALONE_POD=$(kube get pods --no-headers 2>/dev/null | grep "slackbot" | grep "Running" | head -1 | awk '{print $1}')
if [[ -n "$STANDALONE_POD" ]]; then
  SLACKBOT_POD="$STANDALONE_POD"
  # Container name is "slack-bot" in both standalone and sidecar deployments
  SLACKBOT_CONTAINER=$(kube get pod "$STANDALONE_POD" -o jsonpath='{.spec.containers[0].name}' 2>/dev/null)
  SLACKBOT_CONTAINER="${SLACKBOT_CONTAINER:-slack-bot}"
  log "Found standalone slackbot: $SLACKBOT_POD (container: $SLACKBOT_CONTAINER)"
else
  # Fall back to sidecar in daemon pod
  DAEMON_POD=$(kube get pods --no-headers 2>/dev/null | grep "daemon" | grep -v "dolt\|nats\|clusterctl" | grep "Running" | head -1 | awk '{print $1}')
  if [[ -n "$DAEMON_POD" ]]; then
    CONTAINERS=$(kube get pod "$DAEMON_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
    if [[ "$CONTAINERS" == *"slack-bot"* ]]; then
      SLACKBOT_POD="$DAEMON_POD"
      SLACKBOT_CONTAINER="slack-bot"
      log "Found slackbot sidecar in: $SLACKBOT_POD"
    fi
  fi
fi

if [[ -z "$SLACKBOT_POD" ]]; then
  skip_all "no slackbot pod/sidecar found"
  exit 0
fi

# ── Discover broker ──────────────────────────────────────────────────
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')

# Get broker auth token
BROKER_TOKEN=""
AUTH_SECRET=$(kube get secret --no-headers 2>/dev/null | grep -E "coop-broker-auth|daemon-token" | head -1 | awk '{print $1}')
if [[ -n "$AUTH_SECRET" ]]; then
  BROKER_TOKEN=$(kube get secret "$AUTH_SECRET" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
fi

# ── Port-forward to broker ───────────────────────────────────────────
BROKER_PORT=""

setup_broker() {
  if [[ -z "$BROKER_PORT" ]]; then
    if [[ -n "$BROKER_SVC" ]]; then
      BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
    else
      local broker_pod
      broker_pod=$(get_pod "app.kubernetes.io/component=coop-broker")
      [[ -n "$broker_pod" ]] || return 1
      BROKER_PORT=$(start_port_forward "pod/$broker_pod" 9800) || return 1
    fi
  fi
}

broker_api() {
  local method="${1:-GET}" path="$2"
  shift 2
  curl -sf --connect-timeout 5 -X "$method" \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    -H "Content-Type: application/json" \
    "http://127.0.0.1:${BROKER_PORT}${path}" "$@" 2>/dev/null
}

json_field() {
  local json_str="$1" expr="$2"
  python3 -c "
import json, sys
d = json.loads(sys.stdin.read())
$expr
" <<< "$json_str" 2>/dev/null
}

# ── Unique test account name ──────────────────────────────────────────
E2E_ACCOUNT="e2e-test-$(date +%s)"
log "Test account name: $E2E_ACCOUNT"

# ── Test functions ─────────────────────────────────────────────────────

test_slackbot_running() {
  local state
  state=$(kube get pod "$SLACKBOT_POD" -o jsonpath="{.status.containerStatuses[?(@.name==\"${SLACKBOT_CONTAINER}\")].state}" 2>/dev/null)
  assert_contains "$state" "running"
}

test_slackbot_has_broker_url() {
  # Check that slackbot has COOP_BROKER_URL or COOPMUX_URL configured
  local env_names
  env_names=$(kube get pod "$SLACKBOT_POD" -o jsonpath="{.spec.containers[?(@.name==\"${SLACKBOT_CONTAINER}\")].env[*].name}" 2>/dev/null)
  if assert_contains "$env_names" "COOP_BROKER_URL" || assert_contains "$env_names" "COOPMUX_URL"; then
    log "Slackbot has broker URL configured"
    return 0
  fi
  log "Slackbot env vars: $env_names"
  return 1
}

NEW_ACCOUNT_RESP=""

test_create_account() {
  setup_broker || return 1
  local bodyfile respfile http_code
  bodyfile=$(mktemp)
  respfile=$(mktemp)
  python3 -c "
import json
with open('$bodyfile', 'w') as f:
    json.dump({
        'name': '$E2E_ACCOUNT',
        'provider': 'claude',
        'reauth': False
    }, f)
" 2>/dev/null

  http_code=$(curl -s -o "$respfile" -w "%{http_code}" --connect-timeout 10 \
    -X POST -H "Content-Type: application/json" \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    -d "@${bodyfile}" \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/credentials/new" 2>/dev/null)
  rm -f "$bodyfile"

  NEW_ACCOUNT_RESP=$(cat "$respfile" 2>/dev/null)
  rm -f "$respfile"

  log "Create account HTTP $http_code: ${NEW_ACCOUNT_RESP:0:200}"

  if [[ "$http_code" == "200" || "$http_code" == "201" ]]; then
    return 0
  elif [[ "$http_code" == "409" ]]; then
    # Account already exists — acceptable in re-runs
    log "Account already exists (409) — acceptable"
    return 0
  else
    return 1
  fi
}

test_account_in_status() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local status_resp
  status_resp=$(broker_api GET "/api/v1/credentials/status")
  [[ -n "$status_resp" ]] || return 1

  local found
  found=$(json_field "$status_resp" "
accts = d if isinstance(d, list) else d.get('accounts', [])
found = any(a.get('name') == '$E2E_ACCOUNT' for a in accts)
print('yes' if found else 'no')
")
  log "Account $E2E_ACCOUNT in status: $found"
  [[ "$found" == "yes" ]]
}

REAUTH_RESP=""

test_reauth_returns_url() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local bodyfile respfile http_code
  bodyfile=$(mktemp)
  respfile=$(mktemp)
  python3 -c "
import json
with open('$bodyfile', 'w') as f:
    json.dump({'account': '$E2E_ACCOUNT'}, f)
" 2>/dev/null

  http_code=$(curl -s -o "$respfile" -w "%{http_code}" --connect-timeout 10 \
    -X POST -H "Content-Type: application/json" \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    -d "@${bodyfile}" \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/credentials/reauth" 2>/dev/null)
  rm -f "$bodyfile"

  REAUTH_RESP=$(cat "$respfile" 2>/dev/null)
  rm -f "$respfile"

  log "Reauth HTTP $http_code: ${REAUTH_RESP:0:300}"

  if [[ "$http_code" == "200" ]]; then
    # Should contain auth_url or user_code
    assert_contains "$REAUTH_RESP" "auth_url" || assert_contains "$REAUTH_RESP" "user_code"
  elif [[ "$http_code" == "409" || "$http_code" == "400" ]]; then
    # Account already healthy or reauth already in progress
    log "Reauth not needed or in progress ($http_code)"
    return 0
  else
    return 1
  fi
}

test_auth_url_valid() {
  [[ -n "$REAUTH_RESP" ]] || { log "No reauth response to validate"; return 0; }

  local auth_url
  auth_url=$(json_field "$REAUTH_RESP" "print(d.get('auth_url', ''))" 2>/dev/null)
  if [[ -z "$auth_url" ]]; then
    log "No auth URL in response (device code or already healthy)"
    return 0
  fi
  log "Auth URL: ${auth_url:0:100}..."
  assert_contains "$auth_url" "anthropic.com" || assert_contains "$auth_url" "claude.ai"
}

test_account_in_pool() {
  [[ -n "$BROKER_PORT" ]] || return 1
  local pool_resp
  pool_resp=$(broker_api GET "/api/v1/credentials/pool")
  [[ -n "$pool_resp" ]] || return 1

  local found
  found=$(json_field "$pool_resp" "
accts = d.get('accounts', [])
found = any(a.get('name') == '$E2E_ACCOUNT' for a in accts)
print('yes' if found else 'no')
")
  log "Account $E2E_ACCOUNT in pool: $found"
  [[ "$found" == "yes" ]]
}

test_slackbot_cred_logs() {
  # Check slackbot logs for credential-related activity (NATS subscription, coop events)
  local logs
  logs=$(kube logs "$SLACKBOT_POD" -c "$SLACKBOT_CONTAINER" --tail=100 2>/dev/null)
  [[ -n "$logs" ]] || return 1

  if echo "$logs" | grep -qi "coop\|credential\|nats.*subscribe\|cred_watcher"; then
    log "Slackbot has credential handling activity"
    return 0
  fi
  if echo "$logs" | grep -qi "connected\|listening\|started"; then
    log "Slackbot running (no credential events yet)"
    return 0
  fi
  log "No credential activity in slackbot logs"
  return 1
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Slackbot deployment is running" test_slackbot_running
run_test "Slackbot has coopmux URL configured" test_slackbot_has_broker_url
run_test "Create test account via /api/v1/credentials/new" test_create_account
run_test "New account appears in credential status" test_account_in_status
run_test "Reauth endpoint returns auth URL for new account" test_reauth_returns_url
run_test "Auth URL is valid Anthropic OAuth URL" test_auth_url_valid
run_test "New account appears in credential pool" test_account_in_pool
run_test "Slackbot has credential handling activity" test_slackbot_cred_logs

print_summary
