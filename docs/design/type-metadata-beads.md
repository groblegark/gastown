# Type Metadata Beads Design

> Extending the beads type system with configurable validation rules
> Part of epic: bd-epc-decision_type_templates_subtype

## Problem Statement

We want decision beads to have structured context requirements based on their type (architecture, debugging, feature, etc.). Rather than hardcoding these rules, we need a flexible system where:

1. Type validation rules are themselves beads (versionable, auditable)
2. Validators can run custom scripts (also beads)
3. The system is extensible to other bead types, not just decisions

## Proposal: Type Metadata Beads

Extend the beads type system by allowing **type metadata beads** that define validation rules, required fields, and UI metadata for any bead type/subtype combination.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Bead Creation                             │
│  bd decision create --subtype architecture --context "..."       │
└─────────────────────────┬───────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Type Metadata Lookup                          │
│  Find: type-meta bead for type=decision, subtype=architecture    │
└─────────────────────────┬───────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Validation Pipeline                           │
│  For each required_field:                                        │
│    1. Check field present in context                             │
│    2. If validator_bead specified, run it                        │
│    3. Validator loads script_bead, executes                      │
└─────────────────────────┬───────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Result                                        │
│  Pass: Create bead with type + subtype                          │
│  Fail: Return errors with helpful messages                       │
└─────────────────────────────────────────────────────────────────┘
```

## New Bead Types

### 1. `type-meta` - Type Metadata Bead

Defines validation rules and UI metadata for a type/subtype combination.

**Prefix:** `meta-`

**Fields:**
```yaml
ID: meta-decision-architecture
Type: type-meta

# What this metadata applies to
for_type: decision
for_subtype: architecture        # null for base type rules
parent_subtype: null             # for inheritance (e.g., "architecture" for "architecture/database")

# UI metadata
emoji: "📐"
label: "Architecture Decision"
description: "Technical design choices between approaches, libraries, or patterns"

# Required fields/sections in context
required_fields:
  - name: problem
    heading: "## Problem"
    description: "What problem are we solving?"
    validator_bead: null          # optional: reference to validator bead

  - name: constraints
    heading: "## Constraints"
    description: "Technical/business constraints"
    validator_bead: null

  - name: alternatives_considered
    heading: "## Alternatives Considered"
    description: "Options that were evaluated"
    validator_bead: null

# Optional fields (documented but not enforced)
optional_fields:
  - name: tradeoffs
    heading: "## Tradeoffs"
    description: "Pros/cons of each alternative"

  - name: recommendation
    heading: "## Recommendation"
    description: "Preferred option and rationale"

# Example of good context
example: |
  ## Problem
  API response times averaging 200ms, target is <50ms.

  ## Constraints
  - Must work with Kubernetes multi-pod deployment
  - No new managed services (budget)

  ## Alternatives Considered
  1. Redis - distributed, handles multi-pod
  2. In-memory - simple but no sharing
```

**Subtype with inheritance:**
```yaml
ID: meta-decision-architecture-database
Type: type-meta

for_type: decision
for_subtype: architecture/database
parent_subtype: architecture      # inherits from parent

emoji: "🗄️"
label: "Database Architecture Decision"
description: "Database design and schema decisions"

# Additional required fields (on top of parent's)
required_fields:
  - name: schema_impact
    heading: "## Schema Impact"
    description: "Database schema changes required"

  - name: migration_plan
    heading: "## Migration Plan"
    description: "How to migrate existing data"
    validator_bead: null
```

### 2. `validator` - Validator Bead

Defines a validation rule that can be applied to fields.

**Prefix:** `vld-`

**Fields:**
```yaml
ID: vld-url-exists
Type: validator

name: "url-exists"
description: "Verify URL is accessible (HTTP 200)"

# Reference to script that performs validation
script_bead: scr-check-url-exists

# How to extract the value to validate from context
# Options: "section_content", "first_line", "all_urls", "json_field"
extract_mode: all_urls

# Error message template (can use {value}, {error} placeholders)
error_template: "URL {value} is not accessible: {error}"

# Validation behavior
fail_fast: false        # continue checking other URLs if one fails
timeout_ms: 5000        # max time for validation
```

### 3. `script` - Script Bead

Stores executable validation logic.

**Prefix:** `scr-`

**Fields:**
```yaml
ID: scr-check-url-exists
Type: script

name: "check-url-exists"
description: "Check if a URL returns HTTP 200"

# Input contract
inputs:
  - name: value
    description: "The URL to check"
    passed_as: arg1     # $1 in bash

# Output contract
success_exit_code: 0
error_output: stderr    # where to read error message

# The actual script
interpreter: /bin/bash
script: |
  #!/bin/bash
  url="$1"

  # Validate URL format first
  if ! [[ "$url" =~ ^https?:// ]]; then
    echo "Invalid URL format: $url" >&2
    exit 1
  fi

  # Check URL is accessible
  status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null)

  if [ "$status" = "200" ]; then
    exit 0
  else
    echo "HTTP $status" >&2
    exit 1
  fi

# Security constraints
allowed_commands:
  - curl
  - grep
  - test
max_runtime_ms: 10000
network_access: true
filesystem_access: false
```

## Subtype Field on Existing Beads

Add `subtype` field to all bead types (nullable):

```sql
ALTER TABLE issues ADD COLUMN subtype TEXT;
```

Usage:
```bash
bd decision create --subtype architecture \
  --prompt "Which caching strategy?" \
  --context "## Problem\n..."

bd bug create --subtype performance \
  --title "API latency regression"
```

The subtype triggers validation lookup:
1. Find `meta-{type}-{subtype}` bead
2. If has `parent_subtype`, also load parent's rules
3. Combine required_fields from all ancestors
4. Validate context against combined rules

## Validation Flow

```
1. User: bd decision create --subtype architecture/database --context "..."

2. System: Load type metadata chain
   - meta-decision-architecture-database (subtype)
   - meta-decision-architecture (parent)
   - meta-decision (base, if exists)

3. System: Combine required_fields from all levels
   - problem (from architecture)
   - constraints (from architecture)
   - alternatives_considered (from architecture)
   - schema_impact (from architecture/database)
   - migration_plan (from architecture/database)

4. System: For each required field
   a. Parse context looking for heading (## Problem)
   b. If not found → add to missing list
   c. If found and has validator_bead:
      - Load validator bead
      - Load script bead
      - Execute script with extracted value
      - If fails → add to validation errors

5. Result:
   - If missing fields → error with list and descriptions
   - If validation errors → error with details
   - If all pass → create bead with type=decision, subtype=architecture/database
```

## CLI Commands

### Type Metadata Management

```bash
# List all type metadata
bd type-meta list
bd type-meta list --for-type decision

# Show type metadata details
bd type-meta show decision/architecture
bd type-meta show decision/architecture/database

# Create type metadata (interactive or from YAML)
bd type-meta create decision/architecture --from type-meta.yaml
bd type-meta create decision/architecture \
  --emoji "📐" \
  --label "Architecture Decision" \
  --required "problem:What problem?" \
  --required "constraints:Technical constraints"

# Show effective rules (with inheritance resolved)
bd type-meta effective decision/architecture/database
```

### Validator Management

```bash
bd validator list
bd validator show url-exists
bd validator create url-exists --script scr-check-url-exists
bd validator test url-exists --value "https://example.com"
```

### Script Management

```bash
bd script list
bd script show check-url-exists
bd script create check-url-exists --from script.sh
bd script run check-url-exists --arg "https://example.com"
```

## Security Model for Scripts

**Concerns:**
- Scripts run arbitrary shell commands
- Could access filesystem, network, secrets
- Could hang or consume resources

**Mitigations:**

1. **Allowlist commands** - Scripts declare which commands they use
2. **Timeout enforcement** - Kill after max_runtime_ms
3. **Resource limits** - ulimit on memory, CPU
4. **Network/filesystem flags** - Explicit opt-in
5. **Sandboxing** - Consider running in container/nsjail
6. **Audit logging** - Log all script executions

**Trust model:**
- Scripts are beads → changes are audited
- Only admins can create/modify script beads (permission check)
- Scripts are reviewed like code

## Default Type Metadata

Ship these type metadata beads by default. Decision types answer: **"Why is the agent asking the human?"** - organized by what the agent needs from the human, derived from actual Gas Town formula patterns.

### Decision Types (Agent-Centric)

#### 1. `confirmation` - "I'm about to do X, is that right?"
**High-stakes action needs human sign-off before proceeding.**

Derived from: mol-shutdown-dance (death warrants), mol-town-shutdown (blockers)

**When to use:** Agent is confident about the action but it's irreversible or high-risk.

**Required context:**
- `action` - What the agent is about to do
- `why` - Why this action is needed
- `reversible` - Can this be undone? (true/false)
- `impact` - What happens if this goes wrong

**Example options:**
- "Proceed with shutdown"
- "Wait, let me check something first"
- "Abort - don't do this"

---

#### 2. `ambiguity` - "The requirements could mean A or B"
**Multiple valid interpretations, need human to clarify intent.**

Derived from: design.formula (Open Questions), feature spec interpretation

**When to use:** Agent found multiple reasonable ways to interpret the task.

**Required context:**
- `interpretations` - List of valid interpretations found
- `leaning` - Which interpretation the agent thinks is right
- `why_unclear` - What's ambiguous in the original request

**Example options:**
- "Interpretation A: strict mode"
- "Interpretation B: permissive mode"
- "Both - implement A with B as fallback"

---

#### 3. `tradeoff` - "Option A vs B, each has pros/cons"
**No clear winner - depends on human priorities.**

Derived from: code-review synthesis (conflicting findings), architecture decisions

**When to use:** Agent evaluated options but the "right" choice depends on values/priorities the agent can't determine.

**Required context:**
- `options` - The alternatives considered
- `analysis` - Pros and cons of each
- `recommendation` - Agent's suggestion if forced to choose
- `deciding_factor` - What would tip the balance

**Example options:**
- "Redis: Fast but adds ops complexity"
- "SQLite: Simple but single-node only"
- "Neither - rethink the approach"

---

#### 4. `stuck` - "I can't proceed without X"
**Agent is blocked and needs something from the human.**

Derived from: polecat escalation patterns, mol-convoy-feed (no capacity)

**When to use:** Agent hit a wall - missing info, external dependency, access needed.

**Required context:**
- `blocker` - What's preventing progress
- `tried` - What the agent already attempted
- `need` - What would unblock (specific ask)

**Example options:**
- "I'll get you the credentials"
- "Skip this part, move on"
- "Let me take over this piece"

---

#### 5. `checkpoint` - "Here's where I am, any course corrections?"
**Periodic check-in during long work.**

Derived from: rule-of-five (iterative refinement), shiny workflow stages

**When to use:** Agent wants to confirm direction before investing more effort. Good for expensive or long-running work.

**Required context:**
- `progress` - What's been accomplished so far
- `next_steps` - What the agent plans to do next
- `concerns` - Anything the agent is uncertain about

**Example options:**
- "Looks good, continue"
- "Adjust: focus more on X"
- "Stop - we need to rethink this"

---

#### 6. `quality` - "Is this good enough?"
**Subjective judgment call about completeness or quality.**

Derived from: rule-of-five (excellence pass), code-review (merge readiness)

**When to use:** Agent finished something but "good enough" is subjective.

**Required context:**
- `artifact` - What was produced (link/description)
- `assessment` - Agent's evaluation of quality
- `gaps` - Known limitations or missing pieces
- `bar` - What standard the agent was aiming for

**Example options:**
- "Ship it - good enough for now"
- "Polish more - address the gaps first"
- "Rethink - doesn't meet the bar"

---

#### 7. `exception` - "Something unexpected happened"
**Error or unusual situation, need guidance on how to proceed.**

Derived from: mol-orphan-scan (RESET/RECOVER/ESCALATE), mol-refinery-patrol (test failures)

**When to use:** Agent encountered something outside normal flow and isn't sure how to handle it.

**Required context:**
- `situation` - What happened (error, unexpected state, edge case)
- `options` - Possible ways to proceed
- `recommendation` - What agent thinks is best
- `risk` - What could go wrong with each option

**Example options:**
- "Retry - probably transient"
- "Skip and continue"
- "Abort and investigate"
- "I'll handle this manually"

---

#### 8. `prioritization` - "Multiple things need doing, what first?"
**Agent has competing tasks or directions.**

Derived from: mol-convoy-feed (dispatch order), triage patterns

**When to use:** Agent can see multiple valid work items but needs human to set priority.

**Required context:**
- `candidates` - What's competing for attention
- `analysis` - Why each matters (urgency, impact)
- `constraints` - Time/resource limitations
- `suggestion` - Agent's proposed order

**Example options:**
- "A first, then B"
- "B first - A can wait"
- "Do both in parallel"
- "Skip C entirely"

### Validators

Validators ensure decision context is useful for humans:

- `vld-not-empty` - Required field has content
- `vld-has-options` - Decision has 2-4 actionable options
- `vld-options-distinct` - Options are meaningfully different
- `vld-recommendation-present` - Agent provided a recommendation
- `vld-bead-exists` - Referenced bead ID exists
- `vld-json-valid` - Valid JSON structure

### Scripts

- `scr-check-not-empty` - String length > 0
- `scr-check-options-count` - 2 <= count <= 4
- `scr-check-options-distinct` - No duplicate option labels
- `scr-check-has-recommendation` - Context includes recommendation field
- `scr-check-bead-exists` - bd show returns 0
- `scr-check-json-valid` - jq parses successfully

## Migration Path

### Phase 1: Schema
- Add `subtype` column to issues table
- Add new bead types: `type-meta`, `validator`, `script`
- Create default type metadata beads

### Phase 2: Validation
- Implement type metadata lookup
- Implement required field validation
- Implement validator/script execution

### Phase 3: CLI
- Add `--subtype` flag to relevant commands
- Add `bd type-meta/validator/script` commands
- Update `bd decision create` to validate

### Phase 4: Integration
- Update `gt decision request --type` to pass subtype
- Update Slack rendering for subtypes
- Add type emoji/label to notifications

## Open Questions

1. **Inheritance depth** - How many levels of parent_subtype? Cap at 3?

2. **Override vs extend** - Can subtype override parent's required field (make optional)?

3. **Validator composition** - Can a field have multiple validators (AND/OR)?

4. **Async validation** - How to handle slow validators (URL checks)?

5. **Validation caching** - Cache script results for same input?

6. **Cross-type metadata** - Can type-meta apply to multiple types?

## Success Metrics

- All new decisions have valid subtype within 30 days
- Validation catches >90% of incomplete decisions before creation
- Script execution adds <500ms to decision creation
- Zero security incidents from script execution
