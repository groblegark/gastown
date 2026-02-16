#!/usr/bin/env bash
# soak-monitor-credentials.sh — Monitor credential lifecycle for soak testing.
#
# Polls the mux credential status API and logs events:
#   - Token TTL tracking
#   - Refresh events (detected by TTL jump)
#   - Distribution events (from mux logs)
#   - Session health changes
#
# Usage:
#   NAMESPACE=gastown-next ./scripts/soak-monitor-credentials.sh [interval_secs]
#
# Default polling interval: 60s

set -euo pipefail

NAMESPACE="${NAMESPACE:-${E2E_NAMESPACE:-gastown-next}}"
INTERVAL="${1:-60}"
LOG_FILE="/tmp/coop-soak-$(date +%Y%m%d-%H%M%S).log"

# Get auth token
AUTH_TOKEN=$(kubectl get secret -n "$NAMESPACE" coop-broker-auth-token \
  -o jsonpath='{.data.token}' | base64 -d)

# Find broker pod
BROKER_POD=$(kubectl get pods -n "$NAMESPACE" \
  -l app.kubernetes.io/component=coop-broker \
  -o jsonpath='{.items[0].metadata.name}')

echo "Soak monitor started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "  Namespace: $NAMESPACE"
echo "  Broker pod: $BROKER_POD"
echo "  Polling interval: ${INTERVAL}s"
echo "  Log file: $LOG_FILE"
echo ""

# Set up port-forward
kubectl port-forward -n "$NAMESPACE" "pod/$BROKER_POD" 29800:9800 >/dev/null 2>&1 &
PF_PID=$!
trap "kill $PF_PID 2>/dev/null" EXIT
sleep 3

LAST_TTL=0
LAST_SESSIONS=0
REFRESH_COUNT=0

log() {
  local msg="[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $1"
  echo "$msg" | tee -a "$LOG_FILE"
}

while true; do
  # Get credential status
  CRED_STATUS=$(curl -sf \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    "http://localhost:29800/api/v1/credentials/status" 2>/dev/null) || {
    log "ERROR: credential status API unreachable"
    sleep "$INTERVAL"
    continue
  }

  TTL=$(echo "$CRED_STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[0].get('expires_in_secs', 0))" 2>/dev/null || echo "0")
  STATUS=$(echo "$CRED_STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[0].get('status', 'unknown'))" 2>/dev/null || echo "unknown")
  HAS_REFRESH=$(echo "$CRED_STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[0].get('has_refresh_token', False))" 2>/dev/null || echo "False")

  # Get session count
  HEALTH=$(curl -sf "http://localhost:29800/api/v1/health" 2>/dev/null)
  SESSIONS=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_count', 0))" 2>/dev/null || echo "0")

  # Detect refresh event (TTL jumped up significantly)
  if [ "$LAST_TTL" -gt 0 ] && [ "$TTL" -gt "$LAST_TTL" ] && [ $((TTL - LAST_TTL)) -gt 300 ]; then
    REFRESH_COUNT=$((REFRESH_COUNT + 1))
    log "REFRESH DETECTED! TTL jumped from ${LAST_TTL}s to ${TTL}s (refresh #${REFRESH_COUNT})"

    # Check distribution logs
    DIST_LOGS=$(kubectl logs -n "$NAMESPACE" "$BROKER_POD" --tail=20 2>/dev/null | grep "distributor" || true)
    if [ -n "$DIST_LOGS" ]; then
      log "  Distribution activity: $(echo "$DIST_LOGS" | tail -1)"
    else
      log "  WARNING: No distribution activity in logs after refresh"
    fi
  fi

  # Detect session count change
  if [ "$LAST_SESSIONS" -gt 0 ] && [ "$SESSIONS" != "$LAST_SESSIONS" ]; then
    log "SESSION CHANGE: $LAST_SESSIONS -> $SESSIONS"
  fi

  # Log status (compact)
  TTL_MIN=$((TTL / 60))
  log "status=$STATUS ttl=${TTL_MIN}m sessions=$SESSIONS refresh=$HAS_REFRESH refreshes=$REFRESH_COUNT"

  # Warn on low TTL
  if [ "$TTL" -gt 0 ] && [ "$TTL" -lt 600 ]; then
    log "WARNING: Token TTL below 10 minutes!"
  fi

  # Warn on unhealthy status
  if [ "$STATUS" != "healthy" ]; then
    log "WARNING: Credential status is '$STATUS' (not healthy)"
  fi

  LAST_TTL=$TTL
  LAST_SESSIONS=$SESSIONS

  sleep "$INTERVAL"
done
