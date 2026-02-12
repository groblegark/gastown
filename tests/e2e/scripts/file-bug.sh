#!/usr/bin/env bash
# file-bug.sh — Auto-file a beads bug for E2E test failures.
#
# Called by formula steps or run-step.sh when a test module fails.
# Creates a bug issue linked to the E2E epic with failure details.
#
# Usage:
#   ./scripts/file-bug.sh --formula <name> --step <id> --namespace <ns> [--output <file>] [--trace <dir>]
#
# Examples:
#   ./scripts/file-bug.sh --formula mol-gastown-e2e-bootstrap --step verify-dolt --namespace gastown-next
#   ./scripts/file-bug.sh --formula mol-gastown-e2e-validate --step verify-io --namespace gastown-next --output /tmp/test.log

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ── Defaults ─────────────────────────────────────────────────────────
FORMULA=""
STEP=""
NAMESPACE=""
OUTPUT_FILE=""
TRACE_DIR=""
EPIC_ID="${E2E_EPIC_ID:-}"

# ── Parse args ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --formula)    FORMULA="$2"; shift 2 ;;
    --step)       STEP="$2"; shift 2 ;;
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    --output)     OUTPUT_FILE="$2"; shift 2 ;;
    --trace)      TRACE_DIR="$2"; shift 2 ;;
    --epic)       EPIC_ID="$2"; shift 2 ;;
    *) shift ;;
  esac
done

if [[ -z "$FORMULA" || -z "$STEP" || -z "$NAMESPACE" ]]; then
  echo "Usage: file-bug.sh --formula <name> --step <id> --namespace <ns>"
  exit 1
fi

# ── Build bug title and description ──────────────────────────────────
TITLE="E2E FAIL: ${FORMULA}:${STEP} in ${NAMESPACE}"

DESCRIPTION="## E2E Test Failure

**Formula:** ${FORMULA}
**Step:** ${STEP}
**Namespace:** ${NAMESPACE}
**Timestamp:** $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Append test output if available
if [[ -n "$OUTPUT_FILE" && -f "$OUTPUT_FILE" ]]; then
  # Truncate to last 100 lines to avoid enormous descriptions
  TAIL=$(tail -100 "$OUTPUT_FILE" 2>/dev/null || echo "(could not read output)")
  DESCRIPTION="${DESCRIPTION}

## Test Output (last 100 lines)
\`\`\`
${TAIL}
\`\`\`"
fi

# Note trace directory if available
if [[ -n "$TRACE_DIR" && -d "$TRACE_DIR" ]]; then
  TRACE_FILES=$(find "$TRACE_DIR" -type f -name "*.zip" -o -name "*.png" 2>/dev/null | head -5)
  if [[ -n "$TRACE_FILES" ]]; then
    DESCRIPTION="${DESCRIPTION}

## Playwright Traces
\`\`\`
${TRACE_FILES}
\`\`\`"
  fi
fi

# ── File the bug ─────────────────────────────────────────────────────
BUG_ID=$(bd create --type=bug --title="$TITLE" --priority=1 --description="$DESCRIPTION" --json 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")

if [[ -n "$BUG_ID" ]]; then
  echo "Filed bug: $BUG_ID — $TITLE"

  # Link to epic if provided
  if [[ -n "$EPIC_ID" ]]; then
    bd dep add "$BUG_ID" "$EPIC_ID" 2>/dev/null || true
  fi
else
  # Fallback: try without --json
  bd create --type=bug --title="$TITLE" --priority=1 --description="$DESCRIPTION" 2>/dev/null || \
    echo "WARNING: Could not file bug for $FORMULA:$STEP (bd create failed)"
fi
