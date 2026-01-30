# Decision Types Design

> Lightweight type system for structured decision context
> Part of epic: bd-epc-decision_type_templates_subtype

## Summary

Decision types answer: **"Why is the agent asking the human?"**

| Type | Agent Says... | Emoji |
|------|--------------|-------|
| `confirmation` | "I'm about to do X, is that right?" | ⚠️ |
| `ambiguity` | "Requirements could mean A or B" | ❓ |
| `tradeoff` | "Option A vs B, each has pros/cons" | ⚖️ |
| `stuck` | "I can't proceed without X" | 🚧 |
| `checkpoint` | "Here's where I am, any corrections?" | 📍 |
| `quality` | "Is this good enough?" | ✨ |
| `exception` | "Something unexpected happened" | ⚡ |
| `prioritization` | "Multiple things need doing, what first?" | 🎯 |

Derived from analysis of 20+ Gas Town formulas.

## Architecture

**Simple script-based validation.** No bead types, no JSON schemas, just scripts.

```
gt decision request --type tradeoff --context '{...}'
        │
        ▼
┌─────────────────────────────────────┐
│  1. Look up type in DecisionTypes   │
│     (hardcoded Go map)              │
└─────────────────┬───────────────────┘
                  │
                  ▼
┌─────────────────────────────────────┐
│  2. Run validator script            │
│     ~/.config/gt/validators/        │
│       decision-type-tradeoff.sh     │
│     (stdin = context JSON)          │
└─────────────────┬───────────────────┘
                  │
                  ▼
┌─────────────────────────────────────┐
│  3. Script exits 0? Create decision │
│     Script exits 1? Show stderr     │
└─────────────────────────────────────┘
```

**Key insight:** Reuse the existing validator execution pattern from `internal/validator/executor.go`. Validation is just "call a script, check exit code."

## Implementation

### 1. Type Registry (~100 lines)

New file: `internal/decision/types.go`

```go
package decision

type TypeDef struct {
    Name        string
    Emoji       string
    Label       string
    Description string
    Required    []string  // Context fields to check (basic validation)
}

var Types = map[string]TypeDef{
    "confirmation": {
        Name:        "confirmation",
        Emoji:       "⚠️",
        Label:       "Confirmation",
        Description: "High-stakes action needs sign-off",
        Required:    []string{"action", "impact"},
    },
    "ambiguity": {
        Name:        "ambiguity",
        Emoji:       "❓",
        Label:       "Ambiguity",
        Description: "Multiple valid interpretations",
        Required:    []string{"interpretations"},
    },
    "tradeoff": {
        Name:        "tradeoff",
        Emoji:       "⚖️",
        Label:       "Tradeoff",
        Description: "Options with pros/cons, human sets priority",
        Required:    []string{"options", "recommendation"},
    },
    "stuck": {
        Name:        "stuck",
        Emoji:       "🚧",
        Label:       "Stuck",
        Description: "Agent is blocked, needs something",
        Required:    []string{"blocker", "tried"},
    },
    "checkpoint": {
        Name:        "checkpoint",
        Emoji:       "📍",
        Label:       "Checkpoint",
        Description: "Mid-work check-in",
        Required:    []string{"progress", "next_steps"},
    },
    "quality": {
        Name:        "quality",
        Emoji:       "✨",
        Label:       "Quality",
        Description: "Subjective judgment call",
        Required:    []string{"artifact", "assessment"},
    },
    "exception": {
        Name:        "exception",
        Emoji:       "⚡",
        Label:       "Exception",
        Description: "Unexpected situation",
        Required:    []string{"situation", "recommendation"},
    },
    "prioritization": {
        Name:        "prioritization",
        Emoji:       "🎯",
        Label:       "Prioritization",
        Description: "Competing tasks, need ordering",
        Required:    []string{"candidates", "constraints"},
    },
}

func GetType(name string) (TypeDef, bool) {
    t, ok := Types[name]
    return t, ok
}

func ListTypes() []TypeDef {
    // Return sorted list
}
```

### 2. Script-Based Validation (~50 lines)

Extend `internal/validator/executor.go` or add to `internal/decision/validate.go`:

```go
// ValidateType runs the type-specific validator script.
// Script receives context JSON on stdin, exits 0 for valid, 1 for invalid.
func ValidateType(typeName, contextJSON string) error {
    typedef, ok := Types[typeName]
    if !ok {
        return fmt.Errorf("unknown decision type: %s", typeName)
    }

    // Basic required field check (fast path)
    if err := checkRequiredFields(typedef.Required, contextJSON); err != nil {
        return err
    }

    // Look for custom validator script
    scriptPath := findTypeValidator(typeName)
    if scriptPath == "" {
        return nil  // No custom script, basic validation passed
    }

    // Run script with context on stdin
    cmd := exec.Command(scriptPath)
    cmd.Stdin = strings.NewReader(contextJSON)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return fmt.Errorf("validation failed: %s", stderr.String())
    }

    return nil
}

func findTypeValidator(typeName string) string {
    // Check standard locations:
    // 1. ~/.config/gt/validators/decision-type-{name}.sh
    // 2. .gt/validators/decision-type-{name}.sh
    // 3. Built-in (embedded)
}

func checkRequiredFields(required []string, contextJSON string) error {
    var ctx map[string]interface{}
    if err := json.Unmarshal([]byte(contextJSON), &ctx); err != nil {
        return fmt.Errorf("context must be valid JSON object: %w", err)
    }

    var missing []string
    for _, field := range required {
        if _, ok := ctx[field]; !ok {
            missing = append(missing, field)
        }
    }

    if len(missing) > 0 {
        return fmt.Errorf("missing required context fields: %v", missing)
    }
    return nil
}
```

### 3. CLI Changes (~30 lines)

In `internal/cmd/decision_impl.go`:

```go
// Add flag
var decisionType string

func init() {
    decisionRequestCmd.Flags().StringVar(&decisionType, "type", "",
        "Decision type (confirmation, ambiguity, tradeoff, stuck, checkpoint, quality, exception, prioritization)")
}

func runDecisionRequest(cmd *cobra.Command, args []string) error {
    // ... existing validation ...

    // Type validation (if specified)
    if decisionType != "" {
        if err := decision.ValidateType(decisionType, decisionContext); err != nil {
            return fmt.Errorf("--%s type validation failed: %w", decisionType, err)
        }
    }

    // ... rest of function ...
}
```

### 4. Slack Rendering (~20 lines)

In `internal/slackbot/bot.go`, update `formatDecisionMessage`:

```go
func formatDecisionMessage(fields *beads.DecisionFields) string {
    var sb strings.Builder

    // Add type prefix if present
    if fields.Type != "" {
        if typedef, ok := decision.GetType(fields.Type); ok {
            sb.WriteString(typedef.Emoji)
            sb.WriteString(" ")
            sb.WriteString(typedef.Label)
            sb.WriteString(": ")
        }
    }

    sb.WriteString(fields.Question)
    // ... rest of formatting ...
}
```

## Example Validator Scripts

Scripts are simple. They read context JSON from stdin, validate, exit 0 or 1.

### `decision-type-tradeoff.sh`

```bash
#!/bin/bash
# Validate tradeoff decision context

ctx=$(cat)

# Check options is an array with 2+ items
options_count=$(echo "$ctx" | jq '.options | length')
if [ "$options_count" -lt 2 ]; then
    echo "tradeoff requires at least 2 options, got $options_count" >&2
    exit 1
fi

# Check recommendation exists and isn't empty
rec=$(echo "$ctx" | jq -r '.recommendation // empty')
if [ -z "$rec" ]; then
    echo "tradeoff requires a recommendation" >&2
    exit 1
fi

exit 0
```

### `decision-type-stuck.js` (Node.js example)

```javascript
#!/usr/bin/env node
const ctx = JSON.parse(require('fs').readFileSync(0, 'utf8'));

if (!ctx.blocker || ctx.blocker.trim() === '') {
    console.error('stuck requires a blocker description');
    process.exit(1);
}

if (!Array.isArray(ctx.tried) || ctx.tried.length === 0) {
    console.error('stuck requires at least one thing you tried');
    process.exit(1);
}

process.exit(0);
```

## CLI Usage

```bash
# Create typed decision
gt decision request \
  --type tradeoff \
  --prompt "Which caching strategy?" \
  --context '{"options": ["Redis", "SQLite"], "recommendation": "Redis"}' \
  --option "Redis: Distributed" \
  --option "SQLite: Simple"

# List available types
gt decision types

# Show type details
gt decision types show tradeoff
```

## Decision Types Detail

### 1. `confirmation` ⚠️

**"I'm about to do X, is that right?"**

High-stakes action needs human sign-off before proceeding.

| Field | Required | Description |
|-------|----------|-------------|
| `action` | Yes | What the agent is about to do |
| `impact` | Yes | What happens if this proceeds |
| `reversible` | No | Boolean: can this be undone? |
| `why` | No | Why the agent wants to do this |

---

### 2. `ambiguity` ❓

**"The requirements could mean A or B"**

Multiple valid interpretations, need human to clarify intent.

| Field | Required | Description |
|-------|----------|-------------|
| `interpretations` | Yes | Array of possible interpretations |
| `leaning` | No | Which interpretation agent prefers |
| `why_unclear` | No | What makes this ambiguous |

---

### 3. `tradeoff` ⚖️

**"Option A vs B, each has pros/cons"**

No clear winner - depends on human priorities.

| Field | Required | Description |
|-------|----------|-------------|
| `options` | Yes | Array of options being considered |
| `recommendation` | Yes | Agent's suggestion if forced to choose |
| `analysis` | No | Pros/cons breakdown per option |
| `deciding_factor` | No | What would tip the balance |

---

### 4. `stuck` 🚧

**"I can't proceed without X"**

Agent is blocked and needs something from the human.

| Field | Required | Description |
|-------|----------|-------------|
| `blocker` | Yes | What's blocking progress |
| `tried` | Yes | Array of things already attempted |
| `need` | No | Specific thing that would unblock |

---

### 5. `checkpoint` 📍

**"Here's where I am, any course corrections?"**

Periodic check-in during long work.

| Field | Required | Description |
|-------|----------|-------------|
| `progress` | Yes | What's been accomplished |
| `next_steps` | Yes | What's planned next |
| `concerns` | No | Any worries or risks |

---

### 6. `quality` ✨

**"Is this good enough?"**

Subjective judgment call about completeness or quality.

| Field | Required | Description |
|-------|----------|-------------|
| `artifact` | Yes | What's being evaluated |
| `assessment` | Yes | Agent's quality assessment |
| `gaps` | No | Known shortcomings |
| `bar` | No | Quality standard being measured against |

---

### 7. `exception` ⚡

**"Something unexpected happened"**

Error or unusual situation, need guidance.

| Field | Required | Description |
|-------|----------|-------------|
| `situation` | Yes | What happened |
| `recommendation` | Yes | Agent's suggested action |
| `options` | No | Possible ways to handle it |
| `risk` | No | What could go wrong |

---

### 8. `prioritization` 🎯

**"Multiple things need doing, what first?"**

Agent has competing tasks or directions.

| Field | Required | Description |
|-------|----------|-------------|
| `candidates` | Yes | Array of work items |
| `constraints` | Yes | Time/resource limits |
| `analysis` | No | Brief analysis per candidate |
| `suggestion` | No | Agent's recommended order |

---

## Implementation Estimate

| Component | Lines | Files |
|-----------|-------|-------|
| `types.go` | ~100 | 1 new |
| `validate.go` | ~50 | 1 new |
| `decision_impl.go` changes | ~30 | modify |
| `bot.go` changes | ~20 | modify |
| Built-in validator scripts | ~100 | 8 new |
| Tests | ~100 | 2 new |
| **Total** | **~400** | 12 files |

## Migration

**Single phase:**
1. Add `--type` flag to `gt decision request`
2. Add type validation (Go code + scripts)
3. Update Slack rendering to show emoji/label
4. Ship default validator scripts

No schema changes. No new bead types. Types are optional - existing decisions continue to work.

## Success Metrics

- Agents use typed decisions >50% of the time
- Type validation catches missing context before creation
- Humans can glance at emoji to understand decision category
