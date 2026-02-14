#!/usr/bin/env bash
# provision-namespace.sh — Create a fresh K8s namespace with the gastown helm chart.
#
# Usage:
#   ./scripts/provision-namespace.sh [--namespace NAME] [--values FILE] [--cleanup] [--set KEY=VAL ...]
#
# Defaults:
#   --namespace  gastown-e2e-$(date +%s)  (unique per run)
#   --values     values-e2e.yaml          (E2E overlay with all components enabled)
#   --cleanup    delete namespace on exit (trap)
#   --set        passthrough to helm --set (repeatable, for ExternalSecret remoteRefs)
#
# Examples:
#   # Full ephemeral E2E run (auto-cleanup):
#   ./scripts/provision-namespace.sh --cleanup \
#     --set bd-daemon.externalSecrets.doltRootPassword.remoteRef=shared-e2e-dolt-root-password \
#     --set bd-daemon.externalSecrets.daemonToken.remoteRef=shared-e2e-bd-daemon-token
#
#   # Use an existing namespace (skip install, just validate):
#   ./scripts/provision-namespace.sh --namespace gastown-next --skip-install
#
#   # Custom values file:
#   ./scripts/provision-namespace.sh --values values/gastown-uat.yaml

set -euo pipefail

# ── Defaults ─────────────────────────────────────────────────────────
NAMESPACE=""
VALUES_FILE=""
CHART_DIR=""
SKIP_INSTALL=false
AUTO_CLEANUP=false
TIMEOUT=900  # 15 minutes for all pods to be ready
POLL_INTERVAL=10
HELM_SET_ARGS=()  # --set passthrough flags

# ── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${BLUE}[provision]${NC} $1"; }
ok()   { echo -e "${GREEN}[provision]${NC} $1"; }
warn() { echo -e "${YELLOW}[provision]${NC} $1"; }
err()  { echo -e "${RED}[provision]${NC} $1" >&2; }
die()  { err "$1"; exit 1; }

# ── Parse args ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    --values)     VALUES_FILE="$2"; shift 2 ;;
    --chart-dir)  CHART_DIR="$2"; shift 2 ;;
    --skip-install) SKIP_INSTALL=true; shift ;;
    --cleanup)    AUTO_CLEANUP=true; shift ;;
    --timeout)    TIMEOUT="$2"; shift 2 ;;
    --set)        HELM_SET_ARGS+=("--set" "$2"); shift 2 ;;
    -h|--help)
      echo "Usage: $0 [--namespace NAME] [--values FILE] [--chart-dir DIR] [--skip-install] [--cleanup] [--timeout SECS] [--set KEY=VAL ...]"
      exit 0
      ;;
    *) die "Unknown arg: $1" ;;
  esac
done

# ── Resolve paths ────────────────────────────────────────────────────
# Auto-detect chart directory (prefer gastown repo chart over fics-helm-chart)
if [[ -z "$CHART_DIR" ]]; then
  if [[ -d "$(dirname "$0")/../../../helm/gastown" ]]; then
    CHART_DIR="$(cd "$(dirname "$0")/../../../helm/gastown" && pwd)"
  elif [[ -d "$HOME/gastown/helm/gastown" ]]; then
    CHART_DIR="$HOME/gastown/helm/gastown"
  elif [[ -d "$HOME/fics-helm-chart/charts/gastown" ]]; then
    CHART_DIR="$HOME/fics-helm-chart/charts/gastown"
  else
    die "Cannot find gastown helm chart. Use --chart-dir."
  fi
fi

if [[ -z "$VALUES_FILE" ]]; then
  # Default to E2E values overlay (enables all components)
  if [[ -f "$CHART_DIR/values-e2e.yaml" ]]; then
    VALUES_FILE="$CHART_DIR/values-e2e.yaml"
  elif [[ -f "$CHART_DIR/values/gastown-next.yaml" ]]; then
    VALUES_FILE="$CHART_DIR/values/gastown-next.yaml"
  else
    die "No values file found. Use --values."
  fi
fi

if [[ -z "$NAMESPACE" ]]; then
  NAMESPACE="gastown-e2e-$(date +%s)"
fi

log "Namespace:  $NAMESPACE"
log "Chart dir:  $CHART_DIR"
log "Values:     $VALUES_FILE"
log "Timeout:    ${TIMEOUT}s"

# ── Validate prerequisites ───────────────────────────────────────────
command -v kubectl >/dev/null || die "kubectl not found"
command -v helm >/dev/null    || die "helm not found"
[[ -f "$VALUES_FILE" ]]      || die "Values file not found: $VALUES_FILE"
[[ -f "$CHART_DIR/Chart.yaml" ]] || die "Chart.yaml not found in $CHART_DIR"

# Verify cluster access
kubectl cluster-info >/dev/null 2>&1 || die "Cannot connect to K8s cluster"

# ── Cleanup trap ─────────────────────────────────────────────────────
cleanup() {
  if [[ "$AUTO_CLEANUP" == "true" ]]; then
    warn "Cleaning up namespace $NAMESPACE..."
    helm uninstall "$NAMESPACE" -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete namespace "$NAMESPACE" --wait=false 2>/dev/null || true
    ok "Cleanup initiated for $NAMESPACE"
  fi
}

if [[ "$AUTO_CLEANUP" == "true" ]]; then
  trap cleanup EXIT
fi

# ── Install ──────────────────────────────────────────────────────────
if [[ "$SKIP_INSTALL" == "false" ]]; then
  log "Installing gastown helm chart into $NAMESPACE..."
  if [[ ${#HELM_SET_ARGS[@]} -gt 0 ]]; then
    log "  with ${#HELM_SET_ARGS[@]} --set args"
  fi

  # Background pod monitor — shows what's happening while helm --wait blocks
  _monitor_pods() {
    sleep 15  # let helm submit resources first
    while true; do
      if kubectl get ns "$NAMESPACE" >/dev/null 2>&1; then
        log "  [monitor] Pod status:"
        kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
          | while IFS= read -r line; do log "    $line"; done
        # Show events for any non-Running pods
        kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
          | grep -v "Running\|Completed" \
          | awk '{print $1}' \
          | while IFS= read -r pod; do
              log "  [monitor] Events for $pod:"
              kubectl get events -n "$NAMESPACE" --field-selector "involvedObject.name=$pod" --no-headers 2>/dev/null \
                | tail -5 \
                | while IFS= read -r ev; do log "    $ev"; done
            done
      fi
      sleep 30
    done
  }
  _monitor_pods &
  MONITOR_PID=$!

  set +e  # Capture helm exit code for diagnostics
  helm upgrade --install "$NAMESPACE" "$CHART_DIR/" \
    -n "$NAMESPACE" --create-namespace \
    --values "$CHART_DIR/values.yaml" \
    --values "$VALUES_FILE" \
    "${HELM_SET_ARGS[@]}" \
    --timeout "${TIMEOUT}s" \
    --wait 2>&1 | while IFS= read -r line; do log "  helm: $line"; done
  HELM_EXIT=${PIPESTATUS[0]}
  set -e

  # Stop monitor
  kill "$MONITOR_PID" 2>/dev/null || true
  wait "$MONITOR_PID" 2>/dev/null || true

  if [[ "$HELM_EXIT" -ne 0 ]]; then
    # Capture final pod state on failure
    err "Helm install failed (exit $HELM_EXIT). Final pod state:"
    kubectl get pods -n "$NAMESPACE" -o wide 2>&1 | while IFS= read -r line; do err "  $line"; done
    err "Recent events:"
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>/dev/null | tail -30 \
      | while IFS= read -r line; do err "  $line"; done
    err "Describing non-ready pods:"
    kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
      | grep -v "Running\|Completed" \
      | awk '{print $1}' \
      | while IFS= read -r pod; do
          kubectl describe pod "$pod" -n "$NAMESPACE" 2>&1 | tail -30 \
            | while IFS= read -r line; do err "  [$pod] $line"; done
        done
    die "Helm install failed (exit $HELM_EXIT)"
  fi

  ok "Helm install completed"
else
  log "Skipping install (--skip-install)"
  # Verify namespace exists
  kubectl get ns "$NAMESPACE" >/dev/null 2>&1 || die "Namespace $NAMESPACE does not exist"
fi

# ── Wait for pods ────────────────────────────────────────────────────
log "Waiting for all pods to be ready (timeout: ${TIMEOUT}s)..."

deadline=$((SECONDS + TIMEOUT))
while [[ $SECONDS -lt $deadline ]]; do
  # Count pods that are not Running/Completed (exclude CronJob completed pods)
  # Note: grep -v returns exit 1 when no lines match; use || true to avoid set -e
  not_ready=$(kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
    | { grep -v Completed || true; } \
    | { grep -v "1/1\|2/2\|3/3" || true; } \
    | { grep -v "^$" || true; } \
    | wc -l | tr -d ' ')

  total=$(kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
    | { grep -v Completed || true; } \
    | { grep -v "^$" || true; } \
    | wc -l | tr -d ' ')

  ready=$((total - not_ready))

  if [[ "$not_ready" -eq 0 && "$total" -gt 0 ]]; then
    ok "All $total pods ready in $NAMESPACE"
    break
  fi

  log "  $ready/$total pods ready, waiting..."
  sleep "$POLL_INTERVAL"
done

if [[ $SECONDS -ge $deadline ]]; then
  err "Timeout waiting for pods. Current state:"
  kubectl get pods -n "$NAMESPACE" --no-headers 2>&1 | while IFS= read -r line; do err "  $line"; done
  die "Pod readiness timeout after ${TIMEOUT}s"
fi

# ── Post-provision: configure custom types on daemon ─────────────────
# Beads core only allows built-in types (bug, task, feature, etc.).
# Gas Town agent workflows require "agent" and other custom types.
# Configure them on the daemon so E2E tests can create agent beads.
log "Configuring custom types on daemon..."
DAEMON_POD=$(kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
  | grep "daemon" | grep -v "dolt\|nats" | grep "Running" | head -1 | awk '{print $1}')
if [[ -n "$DAEMON_POD" ]]; then
  kubectl exec -n "$NAMESPACE" "$DAEMON_POD" -c bd-daemon -- \
    bd config set types.custom "agent,role,rig,convoy,slot,gate" >/dev/null 2>&1 \
    && ok "Custom types configured (agent, role, rig, convoy, slot, gate)" \
    || warn "Could not configure custom types on daemon"
else
  warn "No daemon pod found — custom types not configured"
fi

# ── Summary ──────────────────────────────────────────────────────────
echo ""
ok "Namespace $NAMESPACE is ready"
echo ""
kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null \
  | grep -v Completed \
  | while IFS= read -r line; do echo "  $line"; done
echo ""

# Export namespace for downstream scripts
echo "$NAMESPACE"
