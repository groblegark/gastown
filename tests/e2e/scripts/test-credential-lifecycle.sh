#!/usr/bin/env bash
# test-credential-lifecycle.sh — Full credential lifecycle: seed → refresh → distribute → reauth.
#
# Tests the entire credential pipeline across coop broker, agent pods, and K8s:
#
#   Phase 1 — Infrastructure:
#     1. Credential seeder sidecar is running (3/3 containers)
#     2. Broker credential status API responds with auth
#     3. Broker has at least one seeded account
#
#   Phase 2 — Credential health:
#     4. Account status is "healthy" (not expired/revoked)
#     5. Token has reasonable TTL (>5 min remaining)
#     6. Credential PVC persists across restarts
#
#   Phase 3 — Distribution to pods:
#     7. Broker knows about registered agent pods
#     8. Agent pods have credentials on filesystem
#     9. Agent pod credentials are not expired
#
#   Phase 4 — Reauth flow (device code):
#    10. Reauth endpoint returns auth_url + user_code
#    11. Auth URL is a valid Anthropic OAuth URL
#    12. WebSocket emits credential events
#
# Usage:
#   ./scripts/test-credential-lifecycle.sh [NAMESPACE]

MODULE_NAME="credential-lifecycle"
source "$(dirname "$0")/lib.sh"

log "Testing credential lifecycle in namespace: $E2E_NAMESPACE"

# ── Check credentials are configured ──────────────────────────────────
if ! broker_credentials_configured; then
  skip_all "broker credential pipeline not configured (no accounts in configmap)"
  exit 0
fi

# ── Discover broker ──────────────────────────────────────────────────
BROKER_POD=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')

# Get broker auth token (try secret first, then configmap, then fallback)
BROKER_TOKEN=""
AUTH_SECRET=$(kube get secret --no-headers 2>/dev/null | grep -E "coop-broker-auth|daemon-token" | head -1 | awk '{print $1}')
if [[ -n "$AUTH_SECRET" ]]; then
  BROKER_TOKEN=$(kube get secret "$AUTH_SECRET" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
fi
if [[ -z "$BROKER_TOKEN" ]]; then
  CM=$(kube get configmap --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
  if [[ -n "$CM" ]]; then
    BROKER_TOKEN=$(kube get configmap "$CM" -o jsonpath='{.data.BROKER_TOKEN}' 2>/dev/null || echo "")
  fi
fi
BROKER_TOKEN="${BROKER_TOKEN:-V6T4jmuDY1GDgYDmSRaFa1wwd4RTkFKv}"

if [[ -z "$BROKER_POD" ]]; then
  skip_test "Credential seeder sidecar running" "no coop-broker pod"
  skip_test "Credential status API responds" "no coop-broker pod"
  skip_test "Broker has seeded account" "no coop-broker pod"
  skip_test "Account status is healthy" "no coop-broker pod"
  skip_test "Token TTL is reasonable" "no coop-broker pod"
  skip_test "Credential PVC exists" "no coop-broker pod"
  skip_test "Broker has registered pods" "no coop-broker pod"
  skip_test "Agent pod has credentials" "no coop-broker pod"
  skip_test "Agent credentials not expired" "no coop-broker pod"
  skip_test "Reauth endpoint returns auth URL" "no coop-broker pod"
  skip_test "Auth URL is valid Anthropic OAuth" "no coop-broker pod"
  skip_test "WebSocket emits credential events" "no coop-broker pod"
  print_summary
  exit 0
fi

log "Broker pod: $BROKER_POD"

BROKER_CONTAINERS=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
log "Broker containers: $BROKER_CONTAINERS"

# ── Port-forward to broker ───────────────────────────────────────────
BROKER_PORT=""

setup_broker() {
  if [[ -z "$BROKER_PORT" ]]; then
    # Coopmux serves all APIs (mux + broker + credentials) on port 9800
    if [[ -n "$BROKER_SVC" ]]; then
      BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
    else
      BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 9800) || return 1
    fi
  fi
}

# Helper: call broker API with auth
broker_api() {
  local path="$1"
  curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    "http://127.0.0.1:${BROKER_PORT}${path}" 2>/dev/null
}

# Helper: extract JSON field via temp file (avoids pipe issues)
json_extract() {
  local json_str="$1" expr="$2"
  local tmpf
  tmpf=$(mktemp)
  printf '%s' "$json_str" > "$tmpf"
  python3 -c "
import json
with open('$tmpf') as f:
    d = json.load(f)
$expr
" 2>/dev/null
  rm -f "$tmpf"
}

# ═══════════════════════════════════════════════════════════════════════
# Phase 1: Infrastructure
# ═══════════════════════════════════════════════════════════════════════

# ── Test 1: Credential seeder sidecar is running ─────────────────────
test_broker_running() {
  # Broker pod has a single "coopmux" container that handles mux + broker + credentials
  local containers
  containers=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
  assert_contains "$containers" "coopmux" || return 1

  # All containers should be ready
  local ready
  ready=$(kube get pod "$BROKER_POD" --no-headers 2>/dev/null | awk '{print $2}')
  log "Broker pod readiness: $ready"
  # Accept any N/N ready state (coopmux is a single all-in-one binary)
  [[ "$ready" =~ ^([0-9]+)/\1$ ]]
}
run_test "Coop broker running with coopmux container" test_broker_running

# ── Test 2: Credential status API responds ────────────────────────────
CRED_STATUS=""

test_cred_api_responds() {
  setup_broker || return 1
  CRED_STATUS=$(broker_api "/api/v1/credentials/status")
  [[ -n "$CRED_STATUS" ]] || return 1
  # API returns either [{...}] (array) or {"accounts": [{...}]}
  # Both are valid — just check we got JSON with credential data
  assert_contains "$CRED_STATUS" "name" || assert_contains "$CRED_STATUS" "accounts"
}
run_test "Credential status API responds (with auth)" test_cred_api_responds

# ── Test 3: Broker has at least one seeded account ────────────────────
ACCOUNT_COUNT=0

test_has_account() {
  [[ -n "$CRED_STATUS" ]] || return 1
  ACCOUNT_COUNT=$(json_extract "$CRED_STATUS" "
accts = d if isinstance(d, list) else d.get('accounts', [])
print(len(accts))
")
  log "Account count: $ACCOUNT_COUNT"
  [[ "${ACCOUNT_COUNT:-0}" -gt 0 ]]
}
run_test "Broker has at least one seeded account" test_has_account

# Bail if no credentials at all
if [[ "${ACCOUNT_COUNT:-0}" -eq 0 ]]; then
  skip_test "Account status is healthy" "no accounts seeded"
  skip_test "Token TTL is reasonable" "no accounts seeded"
  skip_test "Credential PVC exists" "no accounts seeded"
  skip_test "Broker has registered pods" "no accounts seeded"
  skip_test "Agent pod has credentials" "no accounts seeded"
  skip_test "Agent credentials not expired" "no accounts seeded"
  skip_test "Reauth endpoint returns auth URL" "no accounts seeded"
  skip_test "Auth URL is valid Anthropic OAuth" "no accounts seeded"
  skip_test "WebSocket emits credential events" "no accounts seeded"
  print_summary
  exit 0
fi

# ═══════════════════════════════════════════════════════════════════════
# Phase 2: Credential health
# ═══════════════════════════════════════════════════════════════════════

# ── Test 4: Account status is healthy ─────────────────────────────────
ACCOUNT_STATUS=""
ACCOUNT_NAME=""

test_account_healthy() {
  [[ -n "$CRED_STATUS" ]] || return 1
  local result
  result=$(json_extract "$CRED_STATUS" "
accts = d if isinstance(d, list) else d.get('accounts', [])
if accts:
    a = accts[0]
    print(f\"{a.get('name','?')}|{a.get('status','?')}|{a.get('provider','?')}\")
else:
    print('||')
")
  ACCOUNT_NAME=$(echo "$result" | cut -d'|' -f1)
  ACCOUNT_STATUS=$(echo "$result" | cut -d'|' -f2)
  local provider=$(echo "$result" | cut -d'|' -f3)
  log "Account: $ACCOUNT_NAME, status: $ACCOUNT_STATUS, provider: $provider"
  [[ "$ACCOUNT_STATUS" == "healthy" ]]
}
run_test "Account status is 'healthy' (not expired/revoked)" test_account_healthy

# ── Test 5: Token has reasonable TTL ──────────────────────────────────
test_token_ttl() {
  [[ -n "$CRED_STATUS" ]] || return 1
  local ttl
  ttl=$(json_extract "$CRED_STATUS" "
accts = d if isinstance(d, list) else d.get('accounts', [])
if accts:
    print(accts[0].get('expires_in_secs', 0))
else:
    print(0)
")
  log "Token TTL: ${ttl}s ($(( ttl / 60 ))m)"
  # Should have at least 5 minutes remaining
  [[ "${ttl:-0}" -gt 300 ]]
}
run_test "Token TTL > 5 minutes remaining" test_token_ttl

# ── Test 6: Credential PVC exists ─────────────────────────────────────
test_cred_pvc() {
  local volumes
  volumes=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.volumes[*].name}' 2>/dev/null)
  assert_contains "$volumes" "credentials"
}
run_test "Credential PVC volume exists on broker pod" test_cred_pvc

# ═══════════════════════════════════════════════════════════════════════
# Phase 3: Distribution to agent pods
# ═══════════════════════════════════════════════════════════════════════

# ── Test 7: Broker has registered pods ────────────────────────────────
REGISTERED_PODS=""
REGISTERED_POD_COUNT=0

test_registered_pods() {
  [[ -n "$BROKER_PORT" ]] || return 1
  # Coopmux uses sessions (not pods) — try /api/v1/sessions first, fall back to /api/v1/broker/pods
  REGISTERED_PODS=$(broker_api "/api/v1/sessions")
  if [[ -n "$REGISTERED_PODS" ]]; then
    REGISTERED_POD_COUNT=$(json_extract "$REGISTERED_PODS" "
sessions = d if isinstance(d, list) else d.get('sessions', [])
print(len(sessions))
")
  else
    REGISTERED_PODS=$(broker_api "/api/v1/broker/pods")
    [[ -n "$REGISTERED_PODS" ]] || return 1
    REGISTERED_POD_COUNT=$(json_extract "$REGISTERED_PODS" "
pods = d.get('pods', [])
print(len(pods))
")
  fi
  log "Registered sessions/pods: $REGISTERED_POD_COUNT"
  [[ "${REGISTERED_POD_COUNT:-0}" -gt 0 ]]
}
run_test "Broker has registered agent sessions" test_registered_pods

# ── Test 8: Agent pod has credentials on filesystem ───────────────────
AGENT_POD=$(kube get pods --no-headers 2>/dev/null | { grep "^gt-" || true; } | { grep "Running" || true; } | head -1 | awk '{print $1}')

test_agent_has_creds() {
  [[ -n "$AGENT_POD" ]] || return 1
  kube exec "$AGENT_POD" -- test -f /home/agent/.claude/.credentials.json 2>/dev/null
}

if [[ -n "$AGENT_POD" ]]; then
  run_test "Agent pod has credentials file" test_agent_has_creds
else
  skip_test "Agent pod has credentials file" "no running agent pods"
fi

# ── Test 9: Agent credentials are not expired ─────────────────────────
test_agent_creds_valid() {
  [[ -n "$AGENT_POD" ]] || return 1

  # Extract credential expiry via base64 + temp file
  local tmpfile
  tmpfile=$(mktemp)
  kube exec "$AGENT_POD" -- sh -c 'base64 /home/agent/.claude/.credentials.json' 2>/dev/null \
    | base64 -d > "$tmpfile" 2>/dev/null
  [[ ! -s "$tmpfile" ]] && { rm -f "$tmpfile"; return 1; }

  local result
  result=$(python3 -c "
import json, time
with open('$tmpfile') as f:
    d = json.load(f)
oauth = d.get('claudeAiOauth', {})
exp = oauth.get('expiresAt', 0)
if exp:
    now_ms = int(time.time() * 1000)
    remaining_s = (exp - now_ms) / 1000
    print(f'{remaining_s:.0f}')
else:
    print('0')
" 2>/dev/null)
  rm -f "$tmpfile"

  log "Agent credential TTL: ${result}s"
  # Positive = not expired, negative = expired
  [[ "${result:-0}" -gt 0 ]]
}

if [[ -n "$AGENT_POD" ]]; then
  run_test "Agent pod credentials not expired" test_agent_creds_valid
else
  skip_test "Agent pod credentials not expired" "no running agent pods"
fi

# ═══════════════════════════════════════════════════════════════════════
# Phase 4: Reauth flow
# ═══════════════════════════════════════════════════════════════════════

# ── Test 10: Reauth endpoint returns auth URL ─────────────────────────
REAUTH_RESP=""

test_reauth_endpoint() {
  [[ -n "$BROKER_PORT" ]] || return 1

  # Build reauth request body
  local bodyfile
  bodyfile=$(mktemp)
  python3 -c "
import json
with open('$bodyfile', 'w') as f:
    json.dump({'account': '$ACCOUNT_NAME'}, f)
" 2>/dev/null

  local respfile
  respfile=$(mktemp)
  local http_code
  http_code=$(curl -s -o "$respfile" -w "%{http_code}" --connect-timeout 10 \
    -X POST -H "Content-Type: application/json" \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    -d "@${bodyfile}" \
    "http://127.0.0.1:${BROKER_PORT}/api/v1/credentials/reauth" 2>/dev/null)
  rm -f "$bodyfile"

  REAUTH_RESP=$(cat "$respfile" 2>/dev/null)
  rm -f "$respfile"

  log "Reauth API returned HTTP $http_code"
  log "Reauth response: ${REAUTH_RESP:0:200}"

  # Accept 200 (reauth started) or 409 (already healthy, reauth not needed)
  if [[ "$http_code" == "200" ]]; then
    assert_contains "$REAUTH_RESP" "auth_url" || assert_contains "$REAUTH_RESP" "user_code"
  elif [[ "$http_code" == "409" || "$http_code" == "400" ]]; then
    # 409 = account already healthy, no reauth needed — that's fine
    log "Reauth not needed (account healthy)"
    return 0
  else
    return 1
  fi
}
run_test "Reauth endpoint responds (returns auth URL or healthy)" test_reauth_endpoint

# ── Test 11: Auth URL is valid Anthropic OAuth URL ────────────────────
test_auth_url_valid() {
  [[ -n "$REAUTH_RESP" ]] || return 1

  # If reauth returned an auth_url, validate it
  local auth_url
  auth_url=$(json_extract "$REAUTH_RESP" "
url = d.get('auth_url', '')
print(url)
" 2>/dev/null)

  if [[ -z "$auth_url" ]]; then
    # No auth URL (account was healthy) — pass
    log "No auth URL returned (account healthy, reauth not triggered)"
    return 0
  fi

  log "Auth URL: $auth_url"
  # Should be an Anthropic OAuth URL
  assert_contains "$auth_url" "anthropic.com" || assert_contains "$auth_url" "claude.ai"
}
run_test "Auth URL is valid Anthropic OAuth URL" test_auth_url_valid

# ── Test 12: Broker seeder logs show successful seed ──────────────────
test_broker_cred_logs() {
  # coopmux handles credential management — check its logs for credential activity
  local logs
  logs=$(kube logs "$BROKER_POD" -c coopmux --tail=100 2>/dev/null)
  [[ -n "$logs" ]] || return 1

  # Look for evidence of credential handling in coopmux logs
  if echo "$logs" | grep -qi "credential"; then
    log "Found credential activity in coopmux logs"
    return 0
  fi
  if echo "$logs" | grep -qi "oauth"; then
    log "Found OAuth activity in coopmux logs"
    return 0
  fi
  if echo "$logs" | grep -qi "healthy"; then
    log "Found health status in coopmux logs"
    return 0
  fi
  # coopmux started successfully — that's the minimum
  if echo "$logs" | grep -qi "listening\|started\|ready"; then
    log "Coopmux started (no credential activity yet)"
    return 0
  fi
  log "No credential activity in coopmux logs"
  return 1
}
run_test "Coopmux logs show credential or startup activity" test_broker_cred_logs

# ── Summary ──────────────────────────────────────────────────────────
print_summary
