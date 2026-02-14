#!/usr/bin/env bash
# test-credential-refresh.sh — Verify OAuth credential refresh actually works.
#
# Unlike credential-lifecycle (which checks status/TTL/PVC), this test
# verifies the refresh ACTUALLY SUCCEEDS by checking:
#   - ConfigMap has correct OAuth settings (client_id, token_url)
#   - Credential seeder correctly extracts tokens from K8s secret
#   - Broker logs confirm successful refresh (not stuck in error loop)
#
# These checks would have caught:
#   - Truncated client_id ("9d1c" vs full UUID)
#   - Wrong token_url (console.anthropic.com → platform.claude.com)
#   - JSON path mismatch in seeder (claudeAiOauth.x vs flat x)
#
# Usage:
#   ./scripts/test-credential-refresh.sh [NAMESPACE]

MODULE_NAME="credential-refresh"
source "$(dirname "$0")/lib.sh"

log "Testing credential refresh correctness in $E2E_NAMESPACE"

# ── Discover broker ──────────────────────────────────────────────
BROKER_POD=$(kube get pods --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')
BROKER_CONFIG=$(kube get configmap --no-headers 2>/dev/null | grep "coop-broker-config" | head -1 | awk '{print $1}')

if [[ -z "$BROKER_POD" ]]; then
  skip_all "no coop-broker pod found"
  exit 0
fi

log "Broker pod: $BROKER_POD"
log "Broker config: ${BROKER_CONFIG:-none}"

# Check for credential-seeder sidecar — without it, refresh tests don't apply
BROKER_CONTAINERS=$(kube get pod "$BROKER_POD" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
if [[ "$BROKER_CONTAINERS" != *"credential-seeder"* ]]; then
  log "Broker has no credential-seeder sidecar (containers: $BROKER_CONTAINERS)"
  skip_all "no credential-seeder sidecar on broker pod"
  exit 0
fi

# Skip if no broker ConfigMap with OAuth config
if [[ -z "$BROKER_CONFIG" ]]; then
  skip_all "no coop-broker-config ConfigMap found"
  exit 0
fi

# ── Port-forward to broker ───────────────────────────────────────
BROKER_PORT=""
BROKER_TOKEN="${BROKER_TOKEN:-V6T4jmuDY1GDgYDmSRaFa1wwd4RTkFKv}"

setup_broker() {
  if [[ -z "$BROKER_PORT" ]]; then
    if [[ -n "$BROKER_SVC" ]]; then
      BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 8080) || return 1
    else
      BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 8080) || return 1
    fi
  fi
}

broker_api() {
  local path="$1"
  curl -sf --connect-timeout 5 \
    -H "Authorization: Bearer $BROKER_TOKEN" \
    "http://127.0.0.1:${BROKER_PORT}${path}" 2>/dev/null
}

# ═══════════════════════════════════════════════════════════════════
# Phase 1: OAuth ConfigMap correctness
# ═══════════════════════════════════════════════════════════════════

# ── Test 1: client_id is a full UUID (not truncated) ─────────────
test_client_id_full() {
  [[ -n "$BROKER_CONFIG" ]] || return 1
  local config
  config=$(kube get configmap "$BROKER_CONFIG" -o jsonpath='{.data.config\.json}' 2>/dev/null)
  [[ -n "$config" ]] || return 1

  local client_id
  client_id=$(echo "$config" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for acct in d.get('credentials',{}).get('accounts',[]):
    cid = acct.get('client_id','')
    print(cid)
    break
" 2>/dev/null)

  log "client_id: $client_id"

  # Must be at least 30 chars (full UUID is 36). Catches "9d1c" truncation.
  local len=${#client_id}
  log "client_id length: $len (must be >= 30)"
  [[ "$len" -ge 30 ]]
}
run_test "OAuth client_id is a full UUID (not truncated)" test_client_id_full

# ── Test 2: token_url points to platform.claude.com ──────────────
test_token_url() {
  [[ -n "$BROKER_CONFIG" ]] || return 1
  local config
  config=$(kube get configmap "$BROKER_CONFIG" -o jsonpath='{.data.config\.json}' 2>/dev/null)
  [[ -n "$config" ]] || return 1

  local token_url
  token_url=$(echo "$config" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for acct in d.get('credentials',{}).get('accounts',[]):
    print(acct.get('token_url',''))
    break
" 2>/dev/null)

  log "token_url: $token_url"

  # Must not be the deprecated console.anthropic.com
  if [[ "$token_url" == *"console.anthropic.com"* ]]; then
    log "FAIL: token_url uses deprecated console.anthropic.com"
    return 1
  fi

  # Should contain a valid OAuth token path
  assert_contains "$token_url" "/v1/oauth/token"
}
run_test "OAuth token_url is not deprecated console.anthropic.com" test_token_url

# ═══════════════════════════════════════════════════════════════════
# Phase 2: Credential seeder extracts tokens correctly
# ═══════════════════════════════════════════════════════════════════

# ── Test 3: K8s credential secret exists with expected keys ──────
CRED_SECRET=""

test_cred_secret_keys() {
  # Find the credential secret
  CRED_SECRET=$(kube get secret --no-headers 2>/dev/null \
    | grep -E "claude-cred|claude-oauth" | head -1 | awk '{print $1}')
  [[ -n "$CRED_SECRET" ]] || { log "No claude credential secret found"; return 1; }

  log "Credential secret: $CRED_SECRET"

  # Check it has a credentials.json key
  local keys
  keys=$(kube get secret "$CRED_SECRET" -o jsonpath='{.data}' 2>/dev/null \
    | python3 -c "import json,sys; print(' '.join(json.load(sys.stdin).keys()))" 2>/dev/null)
  log "Secret keys: $keys"
  assert_contains "$keys" "credentials.json"
}
run_test "Claude credential K8s secret exists with credentials.json" test_cred_secret_keys

# ── Test 4: Seeder can extract refresh token from secret format ──
test_seeder_extraction() {
  [[ -n "$CRED_SECRET" ]] || return 1

  # Decode the secret and check that a refresh token is extractable
  # (supports both flat and nested claudeAiOauth formats)
  local result
  result=$(kube get secret "$CRED_SECRET" -o jsonpath='{.data.credentials\.json}' 2>/dev/null \
    | base64 -d \
    | python3 -c "
import json, sys
c = json.load(sys.stdin)
# Try nested first, then flat
o = c.get('claudeAiOauth', {})
rt = o.get('refreshToken', '') or c.get('refreshToken', '') or c.get('refresh_token', '')
if rt:
    print(f'OK:{rt[:15]}...')
else:
    print('MISSING')
" 2>/dev/null)

  log "Refresh token extraction: $result"
  assert_contains "$result" "OK:"
}
run_test "Seeder can extract refresh token from secret format" test_seeder_extraction

# ═══════════════════════════════════════════════════════════════════
# Phase 3: Refresh actually works
# ═══════════════════════════════════════════════════════════════════

# ── Test 5: Broker logs show successful refresh (no error loop) ──
test_refresh_no_errors() {
  local logs
  logs=$(kube logs "$BROKER_POD" -c coop-broker --tail=200 2>/dev/null)
  [[ -n "$logs" ]] || return 1

  # Count refresh errors in the last 200 lines
  local error_count
  error_count=$(echo "$logs" | grep -c "refresh failed after.*attempts" || true)
  log "Refresh failure cycles in last 200 lines: $error_count"

  # Allow up to 2 failure cycles (transient startup), but not a sustained loop
  [[ "${error_count:-0}" -le 2 ]]
}
run_test "Broker has <= 2 refresh failure cycles (not stuck)" test_refresh_no_errors

# ── Test 6: Broker has had at least one successful refresh ───────
test_refresh_success() {
  local logs
  logs=$(kube logs "$BROKER_POD" -c coop-broker 2>/dev/null)
  [[ -n "$logs" ]] || return 1

  # Look for evidence of successful refresh OR healthy seeded credentials
  if echo "$logs" | grep -q "credentials refreshed successfully"; then
    log "Found successful refresh in logs"
    return 0
  fi
  if echo "$logs" | grep -q "credentials seeded"; then
    log "Found successful seed in logs (may not need refresh yet)"
    return 0
  fi
  log "No refresh success or seed confirmation found"
  return 1
}
run_test "Broker has refreshed or seeded credentials successfully" test_refresh_success

# ── Test 7: Current credential status is healthy (not refreshing) ─
test_current_status() {
  setup_broker || return 1

  local status
  status=$(broker_api "/api/v1/credentials/status")
  [[ -n "$status" ]] || return 1

  local acct_status
  acct_status=$(echo "$status" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('accounts', []):
    print(a.get('status', 'unknown'))
    break
" 2>/dev/null)

  log "Current account status: $acct_status"

  # Should be "healthy", not "refreshing" (stuck) or "expired"
  [[ "$acct_status" == "healthy" ]]
}
run_test "Current credential status is 'healthy' (not stuck refreshing)" test_current_status

# ── Summary ──────────────────────────────────────────────────────
print_summary
