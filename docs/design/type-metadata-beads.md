# Decision Types Design

> Script-based validation for decision context
> Part of epic: bd-epc-decision_type_templates_subtype

## Summary

Decision types are **just scripts**. If a script exists for a type, it runs. That's it.

```
gt decision request --type tradeoff --context '{...}'
        │
        ▼
    Find script: decision-type-tradeoff.{sh,js,py,...}
        │
        ▼
    Run script (stdin = context JSON)
        │
        ▼
    Exit 0 → create decision
    Exit 1 → show stderr, fail
```

No hardcoded types. No registry. No metadata. Users define types by creating scripts.

## Implementation

### 1. Script Discovery (~30 lines)

New file: `internal/decision/validate.go`

```go
package decision

import (
    "bytes"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

// ValidateType finds and runs a validator script for the given type.
// Returns nil if no script exists (type is unvalidated but allowed).
func ValidateType(typeName, contextJSON string) error {
    scriptPath := findTypeScript(typeName)
    if scriptPath == "" {
        return nil // No script = no validation
    }

    cmd := exec.Command(scriptPath)
    cmd.Stdin = strings.NewReader(contextJSON)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        msg := strings.TrimSpace(stderr.String())
        if msg == "" {
            msg = err.Error()
        }
        return fmt.Errorf("%s", msg)
    }

    return nil
}

func findTypeScript(typeName string) string {
    // Script naming: decision-type-{name}.{ext}
    baseName := "decision-type-" + typeName

    // Search locations in priority order
    locations := []string{
        filepath.Join(os.Getenv("HOME"), ".config", "gt", "validators"),
        ".gt/validators",
    }

    extensions := []string{".sh", ".bash", ".js", ".py", ".rb"}

    for _, dir := range locations {
        for _, ext := range extensions {
            path := filepath.Join(dir, baseName+ext)
            if info, err := os.Stat(path); err == nil && !info.IsDir() {
                return path
            }
        }
    }

    return ""
}
```

### 2. CLI Changes (~10 lines)

In `internal/cmd/decision_impl.go`:

```go
var decisionType string

func init() {
    decisionRequestCmd.Flags().StringVar(&decisionType, "type", "",
        "Decision type (validated by decision-type-{name} script if present)")
}

func runDecisionRequest(cmd *cobra.Command, args []string) error {
    // ... existing code ...

    if decisionType != "" {
        if err := decision.ValidateType(decisionType, decisionContext); err != nil {
            return fmt.Errorf("type '%s' validation: %w", decisionType, err)
        }
    }

    // ... rest of function ...
}
```

### 3. Store Type in Decision (~5 lines)

Add `Type` field to `DecisionFields` struct, persist it.

## Example Scripts

Scripts live in `~/.config/gt/validators/` or `.gt/validators/`.

### `decision-type-tradeoff.sh`

```bash
#!/bin/bash
ctx=$(cat)

# Must have options array with 2+ items
count=$(echo "$ctx" | jq '.options | length // 0')
if [ "$count" -lt 2 ]; then
    echo "tradeoff needs at least 2 options" >&2
    exit 1
fi

# Must have recommendation
rec=$(echo "$ctx" | jq -r '.recommendation // empty')
if [ -z "$rec" ]; then
    echo "tradeoff needs a recommendation" >&2
    exit 1
fi
```

### `decision-type-stuck.js`

```javascript
#!/usr/bin/env node
const ctx = JSON.parse(require('fs').readFileSync(0, 'utf8'));

if (!ctx.blocker) {
    console.error('stuck needs a blocker');
    process.exit(1);
}
if (!Array.isArray(ctx.tried) || ctx.tried.length === 0) {
    console.error('stuck needs at least one thing you tried');
    process.exit(1);
}
```

### `decision-type-checkpoint.py`

```python
#!/usr/bin/env python3
import json, sys

ctx = json.load(sys.stdin)

if 'progress' not in ctx:
    print('checkpoint needs progress', file=sys.stderr)
    sys.exit(1)
if 'next_steps' not in ctx:
    print('checkpoint needs next_steps', file=sys.stderr)
    sys.exit(1)
```

## Usage

```bash
# With validation (script exists)
gt decision request \
  --type tradeoff \
  --prompt "Which cache?" \
  --context '{"options": ["Redis", "SQLite"], "recommendation": "Redis"}' \
  --option "Redis" --option "SQLite"

# Without validation (no script for this type)
gt decision request \
  --type custom-thing \
  --prompt "Something custom" \
  --context '{"whatever": "you want"}' \
  --option "A" --option "B"

# No type at all (backwards compatible)
gt decision request \
  --prompt "Simple question" \
  --option "Yes" --option "No"
```

## Implementation Estimate

| Component | Lines |
|-----------|-------|
| `validate.go` | ~50 |
| `decision_impl.go` changes | ~10 |
| `DecisionFields.Type` field | ~5 |
| **Total** | **~65** |

Plus whatever validator scripts you want to ship as defaults.

## What This Doesn't Do

- No emoji/label metadata (scripts could output it, but we don't parse it)
- No `gt decision types` command (just `ls ~/.config/gt/validators/decision-type-*`)
- No Slack rendering changes (type is stored but not displayed specially)

These can be added later if needed. Start minimal.
