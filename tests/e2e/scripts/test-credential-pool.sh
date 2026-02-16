#!/usr/bin/env bash
# test-credential-pool.sh — verify credential pool API and load balancing.
#
# Tests the credential pool endpoints added in coop v0.12.5:
#
#   1. Pool API responds with account list
#   2. At least one healthy account in pool
#   3. Pool session counts are non-negative
#   4. Total sessions matches sum of per-account counts
#   5. Rebalance endpoint responds (POST)
#   6. Pool state is consistent after rebalance
#
# Usage: E2E_NAMESPACE=gastown-next ./scripts/test-credential-pool.sh

set -euo pipefail
MODULE_NAME="credential-pool"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing credential pool API in $NS"

# ── Check credentials are configured ──────────────────────────────────
if ! broker_credentials_configured; then
  skip_all "broker credential pipeline not configured"
  exit 0
fi

# ── Discover broker ──────────────────────────────────────────────────
BROKER_POD=$(get_pod "app.kubernetes.io/component=coop-broker")
BROKER_SVC=$(kube get svc --no-headers 2>/dev/null | grep "coop-broker" | head -1 | awk '{print $1}')

# Get broker auth token
BROKER_TOKEN=""
AUTH_SECRET=$(kube get secret --no-headers 2>/dev/null | grep -E "coop-broker-auth|daemon-token" | head -1 | awk '{print $1}')
if [[ -n "$AUTH_SECRET" ]]; then
  BROKER_TOKEN=$(kube get secret "$AUTH_SECRET" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
fi

if [[ -z "$BROKER_POD" ]]; then
  skip_all "no coop-broker pod found"
  exit 0
fi

log "Broker pod: $BROKER_POD"

# ── Port-forward to broker ───────────────────────────────────────────
BROKER_PORT=""

setup_broker() {
  if [[ -z "$BROKER_PORT" ]]; then
    if [[ -n "$BROKER_SVC" ]]; then
      BROKER_PORT=$(start_port_forward "svc/$BROKER_SVC" 9800) || return 1
    else
      BROKER_PORT=$(start_port_forward "pod/$BROKER_POD" 9800) || return 1
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

# ── Test functions ─────────────────────────────────────────────────────

POOL_RESP=""

test_pool_api_responds() {
  setup_broker || return 1
  POOL_RESP=$(broker_api GET "/api/v1/credentials/pool")
  [[ -n "$POOL_RESP" ]] || return 1

  # Must have accounts array and summary fields
  local has_accounts
  has_accounts=$(json_field "$POOL_RESP" "
accts = d.get('accounts', [])
print('yes' if isinstance(accts, list) else 'no')
")
  log "Pool response: ${POOL_RESP:0:300}"
  [[ "$has_accounts" == "yes" ]]
}

test_healthy_account_exists() {
  [[ -n "$POOL_RESP" ]] || return 1
  local healthy_count
  healthy_count=$(json_field "$POOL_RESP" "print(d.get('healthy_accounts', 0))")
  log "Healthy accounts: $healthy_count"
  [[ "${healthy_count:-0}" -gt 0 ]]
}

test_session_counts_valid() {
  [[ -n "$POOL_RESP" ]] || return 1
  local result
  result=$(json_field "$POOL_RESP" "
accts = d.get('accounts', [])
for a in accts:
    sc = a.get('session_count', -1)
    if sc < 0:
        print('negative')
        raise SystemExit
print('ok')
")
  assert_eq "$result" "ok"
}

test_total_sessions_consistent() {
  [[ -n "$POOL_RESP" ]] || return 1
  local result
  result=$(json_field "$POOL_RESP" "
accts = d.get('accounts', [])
computed = sum(a.get('session_count', 0) for a in accts)
reported = d.get('total_sessions', -1)
print(f'{computed}|{reported}')
")
  local computed reported
  computed=$(echo "$result" | cut -d'|' -f1)
  reported=$(echo "$result" | cut -d'|' -f2)
  log "Total sessions: computed=$computed, reported=$reported"
  [[ "$computed" == "$reported" ]]
}

REBALANCE_RESP=""

test_rebalance_endpoint() {
  [[ -n "$BROKER_PORT" ]] || return 1
  REBALANCE_RESP=$(broker_api POST "/api/v1/credentials/pool/rebalance" -d '{}')
  [[ -n "$REBALANCE_RESP" ]] || return 1
  log "Rebalance response: $REBALANCE_RESP"
  # Must contain rebalanced count
  local has_field
  has_field=$(json_field "$REBALANCE_RESP" "
print('yes' if 'rebalanced' in d else 'no')
")
  [[ "$has_field" == "yes" ]]
}

test_pool_consistent_after_rebalance() {
  [[ -n "$BROKER_PORT" ]] || return 1
  # Re-fetch pool state
  local pool_after
  pool_after=$(broker_api GET "/api/v1/credentials/pool")
  [[ -n "$pool_after" ]] || return 1

  local result
  result=$(json_field "$pool_after" "
accts = d.get('accounts', [])
computed = sum(a.get('session_count', 0) for a in accts)
reported = d.get('total_sessions', -1)
healthy = d.get('healthy_accounts', 0)
total = d.get('total_accounts', 0)
print(f'{computed}|{reported}|{healthy}|{total}')
")
  local computed reported healthy total
  computed=$(echo "$result" | cut -d'|' -f1)
  reported=$(echo "$result" | cut -d'|' -f2)
  healthy=$(echo "$result" | cut -d'|' -f3)
  total=$(echo "$result" | cut -d'|' -f4)
  log "Post-rebalance: sessions=$reported (computed=$computed), healthy=$healthy/$total"
  [[ "$computed" == "$reported" ]]
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Pool API responds with account list" test_pool_api_responds
run_test "At least one healthy account in pool" test_healthy_account_exists
run_test "Per-account session counts are non-negative" test_session_counts_valid
run_test "Total sessions equals sum of per-account counts" test_total_sessions_consistent
run_test "Rebalance endpoint responds (POST)" test_rebalance_endpoint
run_test "Pool state consistent after rebalance" test_pool_consistent_after_rebalance

print_summary
