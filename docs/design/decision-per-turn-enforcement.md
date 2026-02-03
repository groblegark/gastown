# Design: Decision Per-Turn Enforcement

**Issue**: fhc-dh4
**Author**: crew/decisions
**Date**: 2026-02-03

## Problem

Agents can bypass decision checkpoints after creating one decision. The current `turn-check` logic has a fallback that allows turns to pass if ANY pending decision exists for the session:

```go
// Current turn-check logic (simplified):
if turnMarkerExists(sessionID) {
    return nil  // OK - decision created this turn
}
if checkAgentHasDecisionForSession(sessionID) {
    return nil  // OK - has pending decision (THE PROBLEM)
}
return block()
```

If an agent creates a decision in turn 1 and it remains pending, turns 2, 3, 4... all pass without requiring new decisions.

## Desired Behavior

- Agent MUST create a decision every turn
- Previous pending decisions don't satisfy the requirement
- Forces regular check-ins with the human overseer

## Solution Options

### Option A: Remove DB Fallback (Recommended)

**Simplest fix.** Remove `checkAgentHasDecisionForSession` from turn-check.

The turn marker system already implements per-turn semantics:
- `turn-clear` (UserPromptSubmit): Clears marker at turn start
- `gt decision request`: Sets marker when decision created
- `turn-check` (Stop): Checks if marker exists

```go
// Fixed turn-check logic:
func runDecisionTurnCheck(...) {
    if turnMarkerExists(input.SessionID) {
        return nil  // OK - decision created this turn
    }
    // No fallback - block if no marker
    return block()
}
```

**Pros:**
- Minimal code change (delete ~10 lines)
- Uses existing marker infrastructure
- Clear semantics: one decision per turn, period

**Cons:**
- Loses resilience if marker file is lost (rare edge case)

### Option B: Turn Number Tracking

Track turn numbers explicitly. Each decision records which turn it was created in.

```
Session state: { turn_number: 5 }
Decision record: { created_at_turn: 5 }
```

**turn-clear:**
```go
incrementTurnNumber(sessionID)
clearTurnMarker(sessionID)
```

**turn-check:**
```go
currentTurn := getTurnNumber(sessionID)
if getDecisionTurn(sessionID) == currentTurn {
    return nil
}
return block()
```

**Pros:**
- Explicit turn tracking (auditable)
- Could support "decision in last N turns" policies later

**Cons:**
- More state to manage
- Overkill for current requirements

### Option C: Timestamp-Based

Record turn start time, verify decision created after.

```go
// turn-clear:
setTurnStartTime(sessionID, time.Now())

// turn-check:
startTime := getTurnStartTime(sessionID)
if hasDecisionCreatedAfter(sessionID, startTime) {
    return nil
}
return block()
```

**Pros:**
- Natural time-based semantics

**Cons:**
- Clock skew issues
- More complex queries
- Timestamp comparison is fragile

## Recommendation

**Option A: Remove DB Fallback**

The turn marker already implements the correct per-turn semantics. The DB fallback was added for resilience but creates the bypass problem. Remove it.

## Implementation

### Changes Required

**File: `internal/cmd/decision_impl.go`**

```go
func runDecisionTurnCheck(cmd *cobra.Command, args []string) error {
    input, err := readTurnHookInput()
    if err != nil {
        return nil  // Hooks should never fail
    }

    if input.SessionID == "" {
        return nil  // No session, no enforcement
    }

    // Check turn marker file - created by gt decision request
    if turnMarkerExists(input.SessionID) {
        return nil  // Decision was created this turn
    }

    // REMOVED: checkAgentHasDecisionForSession fallback
    // This was allowing agents to skip turns after creating one decision

    if decisionTurnCheckSoft {
        return nil  // Soft mode doesn't block
    }

    // Block - no decision this turn
    result := &TurnBlockResult{
        Decision: "block",
        Reason:   "You must offer a decision point every turn...",
    }
    out, _ := json.Marshal(result)
    fmt.Println(string(out))
    return NewSilentExit(1)
}
```

### Lines to Delete

```go
// DELETE these lines from runDecisionTurnCheck:

// Fall back to checking decision records in beads database
hasDecisionThisSession := checkAgentHasDecisionForSession(input.SessionID)

if decisionTurnCheckVerbose {
    fmt.Fprintf(os.Stderr, "[turn-check] Has decision this session (db check): %v\n", hasDecisionThisSession)
}

if hasDecisionThisSession || decisionTurnCheckSoft {
    // ...
}
```

### Test Updates

Update `internal/cmd/decision_integration_test.go`:
- Remove tests for "turn-check skips when agent has pending decisions"
- Add test: "turn-check blocks on turn 2 even with pending decision from turn 1"

## Edge Cases

### First Turn of Session
No change - first turn still requires a decision.

### Agent Creates Multiple Decisions Per Turn
Fine - marker is set on first decision, subsequent ones are allowed.

### Marker File Lost
Agent will be blocked. This is correct behavior - they should create a decision.

### Soft Mode (`--soft`)
Mayor uses soft mode (doesn't hard-block). This still works - soft mode returns early before the block.

## Migration

No migration needed. The change is backwards compatible:
- Agents that create decisions every turn: no change
- Agents that were skipping turns: will now be blocked (desired)

## Verification

After deployment:
1. Create a decision, leave it pending
2. Start a new turn (trigger UserPromptSubmit)
3. Try to end turn without creating a new decision
4. Verify: Should be blocked, not allowed through
