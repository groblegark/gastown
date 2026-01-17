# Client-Agnostic Session Logging Architecture

> Design notes for session logging that works across any agent client

## Overview

This document describes how to build session logging that works for any agent client,
not just Claude Code. Gas Town supports multiple agent runtimes (claude, gemini, codex,
cursor, auggie, amp, and custom agents), so session logging should be designed to work
with all of them.

## Background

### Current State

Session logging is tightly coupled to Claude Code:

1. **Session ID**: Uses `CLAUDE_SESSION_ID` as primary identifier
2. **Log Ingestion**: Parses Claude Code's `~/.claude/` conversation files
3. **Hooks**: Uses Claude Code's hook system (SessionStart, Stop, etc.)
4. **Log Format**: Assumes Claude Code's JSON conversation structure

This works well for Claude Code but doesn't extend to other clients.

### The Problem

Different agent clients have different capabilities:

| Client | Hooks | Session ID Env | Log Format | Resume Style |
|--------|-------|----------------|------------|--------------|
| Claude | Yes | `CLAUDE_SESSION_ID` | JSON conversations | `--resume <id>` |
| Gemini | Yes | `GEMINI_SESSION_ID` | Unknown | `--resume <id>` |
| Codex | No | JSONL output | JSONL | `codex resume <id>` |
| Cursor | No | None | Unknown | `--resume <id>` |
| Auggie | No | None | Unknown | `--resume <id>` |
| AMP | No | None | Unknown | `amp threads continue <id>` |

A solution that only works for Claude Code leaves 5/6 of our supported clients without
proper session observability.

## Research Questions & Answers

### 1. What's the common interface across agent clients?

**Minimal common interface:**

1. **Session boundary detection** - All clients have a start and end
2. **Tmux containment** - All run in tmux sessions (Gas Town invariant)
3. **Working directory** - All have a cwd we can observe
4. **Exit/outcome** - We can detect success/failure/crash

**Available on some clients:**

1. **Session ID** - Claude, Gemini have env vars; Codex emits to JSONL
2. **Hooks** - Claude, Gemini support hooks; others don't
3. **Log files** - Claude has conversation files; others vary

**Conclusion:** The minimal contract is session start/end detection via tmux. Everything
else is optional enrichment based on client capabilities.

### 2. Where should logging hooks live?

**Option A: In gastown (orchestrator)**
- Pros: Centralized, client-agnostic, always works
- Cons: Can only observe external signals (tmux, files), not internal state

**Option B: In bd (beads CLI)**
- Pros: Already cross-client, runs in every session
- Cons: Beads is for issue tracking, not session logging

**Option C: In the client (via hooks/config)**
- Pros: Deep integration, real-time tool call visibility
- Cons: Client-specific, not all clients support hooks

**Option D: Hybrid approach** (Recommended)
- Base layer: Gas Town orchestrator provides session boundary detection for ALL clients
- Enhancement layer: Clients that support hooks get richer data (tool calls, etc.)
- Ingestion layer: Post-session log parsing when log formats are known

**Conclusion:** Hybrid approach with graceful degradation based on client capabilities.

### 3. Can we define a protocol that clients implement?

Yes, but it should be optional. Clients that want deep integration can implement a
simple JSON protocol. Clients that don't still get basic session tracking via tmux.

**Proposed Protocol:** GT Session Protocol (GTSP)

```
Event: session_start
  Required: session_id, timestamp
  Optional: client_name, client_version, work_unit

Event: session_end
  Required: session_id, timestamp, outcome
  Optional: commits, files_changed, tool_call_count

Event: step_started (optional)
  Required: session_id, step_id, timestamp

Event: step_completed (optional)
  Required: session_id, step_id, timestamp, status

Event: tool_call (optional)
  Required: session_id, tool_name, timestamp
  Optional: arguments_summary, result_summary, duration_ms
```

Clients emit these events to a well-known location that Gas Town monitors.

### 4. What's the minimal contract for session observability?

**Tier 1: Guaranteed (all clients)**
- Session start detected (tmux session creation)
- Session end detected (pane death or `gt done`)
- Working directory
- Duration
- Agent identity (from tmux session name)
- Git state (branch, uncommitted changes)

**Tier 2: Available if client supports env vars**
- Session ID (for resume/linking)
- Parent session (for handoff chains)

**Tier 3: Available if client supports hooks**
- Tool call events
- Token usage
- Step-level tracking
- Real-time updates

**Tier 4: Available if client log format is known**
- Post-session conversation ingestion
- Full message history
- Detailed tool call arguments/results

## Proposed Architecture

### Core Principle: Observe, Don't Require

Gas Town should observe what it can from each client, not require clients to implement
specific interfaces. The session logging system should:

1. Work with ANY client that runs in tmux (Tier 1)
2. Get richer data from clients that expose it (Tiers 2-4)
3. Never break if a client doesn't support advanced features

### Component Design

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Session Observer                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐  │
│  │  Tmux Monitor    │  │  Hook Receiver   │  │  Log Ingester    │  │
│  │  (all clients)   │  │  (claude/gemini) │  │  (post-session)  │  │
│  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘  │
│           │                     │                     │            │
│           └─────────────────────┼─────────────────────┘            │
│                                 │                                   │
│                    ┌────────────▼────────────┐                     │
│                    │    Session Aggregator   │                     │
│                    │    (merges all sources) │                     │
│                    └────────────┬────────────┘                     │
│                                 │                                   │
│                    ┌────────────▼────────────┐                     │
│                    │    Session Storage      │                     │
│                    │    (~/.gt/.sessions/)   │                     │
│                    └─────────────────────────┘                     │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 1. Tmux Monitor (Tier 1 - All Clients)

The base layer that works for every client.

**Implementation:**
- Watch for tmux session/pane events
- Detect session start: new tmux session with `gt-` or `hq-` prefix
- Detect session end: pane death, `gt done`, or clean exit
- Extract metadata: agent identity from session name, cwd, duration

**Data Captured:**
```go
type BaseSessionData struct {
    TmuxSession   string    // e.g., "gt-gastown-Toast"
    Agent         string    // e.g., "gastown/polecats/Toast"
    Role          string    // e.g., "polecat"
    Rig           string    // e.g., "gastown"
    CWD           string    // Working directory
    StartedAt     time.Time
    EndedAt       time.Time
    Duration      time.Duration
    Outcome       string    // "done", "crash", "killed", "handoff"
    GitBranch     string    // Current branch at start
    ExitCode      int       // If available
}
```

### 2. Hook Receiver (Tier 3 - Clients with Hook Support)

Enhanced data for clients that support the hook system.

**Current Support:**
- Claude Code: SessionStart, PreCompact, UserPromptSubmit, Stop
- Gemini: Similar hook system (TBD: verify exact hooks)

**Proposed Enhancement:**
Add `gt session report` command that hooks can call to report events:

```bash
# In Claude Code settings.json SessionStart hook:
gt session report --event=start --session-id=$CLAUDE_SESSION_ID

# In Stop hook:
gt session report --event=end --session-id=$CLAUDE_SESSION_ID --outcome=done
```

This decouples the reporting from the specific hook mechanism.

**For clients without hooks:**
Use startup fallback commands (already implemented in `runtime.go`):
```go
// StartupFallbackCommands returns commands that approximate hooks
func StartupFallbackCommands(role string, rc *config.RuntimeConfig) []string {
    // For clients without hooks, inject these via tmux
    return []string{"gt prime && gt nudge deacon session-started"}
}
```

### 3. Log Ingester (Tier 4 - Known Log Formats)

Post-session ingestion for clients with parseable log formats.

**Current Support:**
- Claude Code: `~/.claude/projects/<hash>/.claude/conversations/`

**Design:**
```go
// LogIngester is an interface for client-specific log parsing
type LogIngester interface {
    // CanIngest returns true if this ingester can handle the given client
    CanIngest(clientName string) bool

    // IngestSession parses logs and returns enriched session data
    IngestSession(sessionID string) (*SessionEnrichment, error)
}

// SessionEnrichment contains data extracted from client logs
type SessionEnrichment struct {
    ToolCalls    []ToolCall
    TokenUsage   TokenUsage
    MessageCount int
    Errors       []string
}
```

**Registry Pattern:**
```go
var ingesters = map[string]LogIngester{
    "claude": &ClaudeLogIngester{},
    // "gemini": &GeminiLogIngester{}, // Future
    // "codex": &CodexLogIngester{},   // Future
}
```

### 4. Session Aggregator

Merges data from all sources into a unified session record.

```go
type AggregatedSession struct {
    // From Tmux Monitor (guaranteed)
    BaseSessionData

    // From Hook Receiver (if available)
    SessionID     string   // Client's session ID
    ParentID      string   // For handoff chains
    ChainID       string   // Work chain ID

    // From Log Ingester (if available)
    ToolCalls     []ToolCall
    TokenUsage    TokenUsage
    MessageCount  int

    // Metadata
    ClientName    string   // "claude", "gemini", etc.
    DataTiers     []int    // Which tiers contributed data
}
```

## Extending AgentPresetInfo

The existing `AgentPresetInfo` structure should be extended to capture session logging
capabilities:

```go
type AgentPresetInfo struct {
    // ... existing fields ...

    // Session Observability (new)
    SessionLogging *SessionLoggingConfig `json:"session_logging,omitempty"`
}

type SessionLoggingConfig struct {
    // SessionIDSource indicates how to get the session ID
    // "env:<VAR_NAME>" - from environment variable
    // "jsonl" - parse from JSONL output
    // "none" - no session ID available
    SessionIDSource string `json:"session_id_source,omitempty"`

    // LogLocation is where the client stores logs (for ingestion)
    // Empty means no known log location
    LogLocation string `json:"log_location,omitempty"`

    // LogFormat indicates the log file format
    // "claude-json", "jsonl", "unknown"
    LogFormat string `json:"log_format,omitempty"`

    // HookEvents lists which lifecycle events the client supports
    // e.g., ["session_start", "session_end", "tool_call"]
    HookEvents []string `json:"hook_events,omitempty"`
}
```

**Default Configurations:**

```go
var defaultSessionLogging = map[AgentPreset]*SessionLoggingConfig{
    AgentClaude: {
        SessionIDSource: "env:CLAUDE_SESSION_ID",
        LogLocation:     "~/.claude/projects",
        LogFormat:       "claude-json",
        HookEvents:      []string{"session_start", "pre_compact", "user_prompt", "stop"},
    },
    AgentGemini: {
        SessionIDSource: "env:GEMINI_SESSION_ID",
        LogLocation:     "", // TBD
        LogFormat:       "unknown",
        HookEvents:      []string{"session_start", "stop"},
    },
    AgentCodex: {
        SessionIDSource: "jsonl",
        LogLocation:     "", // Captures from output
        LogFormat:       "jsonl",
        HookEvents:      nil, // No hooks
    },
    // Cursor, Auggie, AMP: minimal config (no known session logging features)
}
```

## GT Session Protocol (GTSP)

A simple, optional protocol for clients that want deep integration.

### Event Format

Events are JSON objects written to `~/.gt/.session-events.jsonl`:

```json
{"type":"session_start","ts":"2026-01-17T10:00:00Z","session_id":"abc-123","client":"claude","agent":"gastown/polecats/Toast"}
{"type":"tool_call","ts":"2026-01-17T10:01:00Z","session_id":"abc-123","tool":"Bash","duration_ms":150}
{"type":"session_end","ts":"2026-01-17T10:30:00Z","session_id":"abc-123","outcome":"done","commits":["abc1234"]}
```

### Integration Methods

**Method 1: Hook-based (Claude, Gemini)**
```json
{
  "hooks": {
    "SessionStart": [{
      "hooks": [{"type": "command", "command": "gt session report start"}]
    }],
    "Stop": [{
      "hooks": [{"type": "command", "command": "gt session report end"}]
    }]
  }
}
```

**Method 2: Wrapper Script (Codex, Cursor, etc.)**
```bash
#!/bin/bash
# gt-codex-wrapper.sh
gt session report start --client=codex
codex "$@"
gt session report end --client=codex --exit-code=$?
```

**Method 3: Tmux-based (automatic, all clients)**
Gas Town's witness/polecat manager already tracks tmux session lifecycle.
This is the fallback that works for every client.

## Migration Path

### Phase 1: Extend AgentPresetInfo (Low Risk)

1. Add `SessionLoggingConfig` to `AgentPresetInfo`
2. Populate defaults for known clients
3. No behavioral changes yet

### Phase 2: Abstract Session ID Handling

1. Replace hardcoded `CLAUDE_SESSION_ID` references with config-driven lookup
2. Use existing `runtime.SessionIDFromEnv()` pattern
3. Extend to support JSONL parsing for Codex

### Phase 3: Implement Session Aggregator

1. Create `internal/sessions/aggregator.go`
2. Merge Tmux Monitor data with optional Hook/Ingester data
3. Store in `~/.gt/.sessions/`

### Phase 4: Client-Specific Ingesters (Optional)

1. Implement `ClaudeLogIngester` (already designed)
2. Add ingesters for other clients as log formats become known
3. Each ingester is independent; missing ingesters don't break anything

## Constraints

- **Research/design only** - No implementation in this task
- **Must not break existing Claude Code functionality** - Backward compatible
- **Should be opt-in for other clients** - Graceful degradation is key

## Open Questions

1. **Gemini log format** - What does Gemini store and where?
2. **Codex JSONL schema** - Exact format for session ID extraction?
3. **Cursor/Auggie/AMP logs** - Do they have parseable log formats?
4. **GTSP adoption** - Would any client maintainers implement this protocol?

## Summary

The key insight is that session logging should be layered:

1. **Guaranteed layer (Tier 1):** Tmux-based observation works for ALL clients
2. **Enhancement layers (Tiers 2-4):** Richer data from clients that expose it
3. **Graceful degradation:** Missing capabilities don't break the system

This approach lets Gas Town provide useful session observability for every supported
client while getting maximum value from clients like Claude Code that expose rich
integration points.

## Related Documentation

- [Session as First-Class Object](session-first-class.md) - Session data model
- [Formula Execution Observer](formula-execution-observer.md) - Execution tracing
- [Agent Configuration](../../internal/config/agents.go) - AgentPresetInfo
