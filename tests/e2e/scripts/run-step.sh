#!/usr/bin/env bash
# run-step.sh — Execute a single E2E test step (shell module or Playwright spec).
#
# Wraps test execution with:
#   - Output capture to a log file
#   - Exit code tracking
#   - Auto-bug-filing on failure (via file-bug.sh)
#   - Port-forward setup for Playwright tests
#
# Usage:
#   ./scripts/run-step.sh --module <name> --namespace <ns> [--formula <name>] [--step <id>] [--epic <id>]
#   ./scripts/run-step.sh --playwright <spec> --namespace <ns> [--formula <name>] [--step <id>]
#
# Examples:
#   ./scripts/run-step.sh --module dolt-health --namespace gastown-next
#   ./scripts/run-step.sh --playwright mux.spec.js --namespace gastown-next --formula mol-e2e-validate --step verify-mux

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
E2E_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── Defaults ─────────────────────────────────────────────────────────
MODULE=""
PLAYWRIGHT_SPEC=""
NAMESPACE="${E2E_NAMESPACE:-gastown-next}"
FORMULA=""
STEP=""
EPIC_ID="${E2E_EPIC_ID:-}"
AUTO_BUG=true

# ── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

# ── Parse args ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --module)       MODULE="$2"; shift 2 ;;
    --playwright)   PLAYWRIGHT_SPEC="$2"; shift 2 ;;
    --namespace)    NAMESPACE="$2"; shift 2 ;;
    --formula)      FORMULA="$2"; shift 2 ;;
    --step)         STEP="$2"; shift 2 ;;
    --epic)         EPIC_ID="$2"; shift 2 ;;
    --no-bug)       AUTO_BUG=false; shift ;;
    *) shift ;;
  esac
done

export E2E_NAMESPACE="$NAMESPACE"

# ── Validate ─────────────────────────────────────────────────────────
if [[ -z "$MODULE" && -z "$PLAYWRIGHT_SPEC" ]]; then
  echo "Usage: run-step.sh --module <name> --namespace <ns>"
  echo "   or: run-step.sh --playwright <spec> --namespace <ns>"
  exit 1
fi

# Derive step ID from module name if not provided
if [[ -z "$STEP" ]]; then
  STEP="${MODULE:-${PLAYWRIGHT_SPEC%.spec.js}}"
fi

# ── Output log ───────────────────────────────────────────────────────
LOG_DIR="${E2E_DIR}/test-results"
mkdir -p "$LOG_DIR"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
LOG_FILE="${LOG_DIR}/${STEP}-${TIMESTAMP}.log"

# ── Execute: Shell module ─────────────────────────────────────────────
if [[ -n "$MODULE" ]]; then
  SCRIPT="$SCRIPT_DIR/test-${MODULE}.sh"
  if [[ ! -x "$SCRIPT" ]]; then
    echo -e "${RED}Script not found or not executable: $SCRIPT${NC}"
    exit 1
  fi

  echo -e "${BLUE}Running module: $MODULE against $NAMESPACE${NC}"
  EXIT_CODE=0
  "$SCRIPT" "$NAMESPACE" 2>&1 | tee "$LOG_FILE" || EXIT_CODE=$?
fi

# ── Execute: Playwright spec ─────────────────────────────────────────
if [[ -n "$PLAYWRIGHT_SPEC" ]]; then
  echo -e "${BLUE}Running Playwright: $PLAYWRIGHT_SPEC against $NAMESPACE${NC}"

  # Set up port-forward to coop-broker for Playwright
  MUX_SVC=$(kubectl get svc -n "$NAMESPACE" --no-headers 2>/dev/null | { grep "coop-broker" || true; } | head -1 | awk '{print $1}')
  PF_PID=""

  if [[ -n "$MUX_SVC" ]]; then
    kubectl port-forward -n "$NAMESPACE" "svc/$MUX_SVC" 18080:8080 >/dev/null 2>&1 &
    PF_PID=$!
    sleep 3
  else
    echo -e "${RED}No coop-broker service found in $NAMESPACE${NC}"
    exit 1
  fi

  EXIT_CODE=0
  (cd "$E2E_DIR" && npx playwright test "$PLAYWRIGHT_SPEC" 2>&1) | tee "$LOG_FILE" || EXIT_CODE=$?

  # Cleanup port-forward
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" 2>/dev/null || true
  fi
fi

# ── Result reporting ─────────────────────────────────────────────────
if [[ "$EXIT_CODE" -eq 0 ]]; then
  echo -e "${GREEN}PASS: $STEP${NC}"
else
  echo -e "${RED}FAIL: $STEP (exit code $EXIT_CODE)${NC}"

  # Auto-file bug on failure
  if [[ "$AUTO_BUG" == "true" && -n "$FORMULA" ]]; then
    TRACE_DIR=""
    if [[ -n "$PLAYWRIGHT_SPEC" ]]; then
      TRACE_DIR="${E2E_DIR}/test-results"
    fi

    "$SCRIPT_DIR/file-bug.sh" \
      --formula "$FORMULA" \
      --step "$STEP" \
      --namespace "$NAMESPACE" \
      --output "$LOG_FILE" \
      ${TRACE_DIR:+--trace "$TRACE_DIR"} \
      ${EPIC_ID:+--epic "$EPIC_ID"} || \
      echo "WARNING: Could not auto-file bug"
  fi
fi

echo "Log: $LOG_FILE"
exit "$EXIT_CODE"
