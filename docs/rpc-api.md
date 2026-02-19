# Gas Town RPC API Reference

> **Protocol**: [Connect-RPC](https://connectrpc.com/) (HTTP/1.1 + HTTP/2, Protobuf + JSON)
> **Package**: `gastown.v1`
> **Generated code**: `gen/gastown/v1/` (protobuf) and `gen/gastown/v1/gastownv1connect/` (Connect handlers)

## Quick Start

```bash
# Start the RPC server
gt rpc serve --port 8443 --town /path/to/workspace --api-key my-secret-key

# Health check
curl http://localhost:8443/health

# List pending decisions (JSON)
curl -X POST http://localhost:8443/gastown.v1.DecisionService/ListPending \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: my-secret-key" \
  -d '{}'

# List open issues
curl -X POST http://localhost:8443/gastown.v1.BeadsService/ListIssues \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: my-secret-key" \
  -d '{"status": "open", "limit": 10}'
```

---

## Table of Contents

1. [Authentication](#authentication)
2. [Error Handling](#error-handling)
3. [Services Overview](#services-overview)
4. [StatusService](#statusservice)
5. [BeadsService](#beadsservice)
6. [AgentService](#agentservice)
7. [SlingService](#slingservice)
8. [MailService](#mailservice)
9. [DecisionService](#decisionservice)
10. [ConvoyService](#convoyservice)
11. [TerminalService](#terminalservice)
12. [ActivityService](#activityservice)
13. [Streaming Patterns](#streaming-patterns)
14. [Proto Schema Versioning](#proto-schema-versioning)
15. [Go Client Examples](#go-client-examples)
16. [curl Examples](#curl-examples)

---

## Authentication

### API Key

All RPC requests (except health checks) require authentication via the `X-GT-API-Key` header:

```
X-GT-API-Key: <your-api-key>
```

The API key is set when starting the RPC server (`--api-key` flag or `GT_API_KEY` env var).

### TLS

Optional HTTPS with cert/key files:

```bash
gt rpc serve --port 8443 --cert /path/to/cert.pem --key /path/to/key.pem
```

### Per-Rig Authorization (bd daemon)

The bd daemon supports per-rig API key scoping via `BD_RPC_AUTH_KEYS`:

```json
{
  "token-abc": {
    "name": "gastown-client",
    "rigs": ["gastown"],
    "operations": ["*"]
  },
  "token-readonly": {
    "name": "monitor",
    "rigs": ["*"],
    "operations": ["list", "show", "stats", "health"]
  }
}
```

---

## Error Handling

Connect-RPC uses standard error codes:

| Code | Meaning | When |
|------|---------|------|
| `CodeInvalidArgument` | Bad request | Missing required fields, invalid IDs |
| `CodeNotFound` | Resource not found | Unknown issue/agent/decision ID |
| `CodeUnauthenticated` | No/invalid API key | Missing or wrong `X-GT-API-Key` |
| `CodePermissionDenied` | Insufficient scope | API key lacks rig/operation access |
| `CodeUnavailable` | Server error | Daemon down, tmux unavailable |
| `CodeInternal` | Unexpected error | Panics, storage failures |

Error responses include a human-readable message:

```json
{
  "code": "not_found",
  "message": "issue gt-abc123 not found"
}
```

---

## Services Overview

| Service | Proto File | RPCs | Purpose |
|---------|-----------|------|---------|
| **StatusService** | `status.proto` | 5 | Town/rig/agent status, health checks |
| **BeadsService** | `beads.proto` | 13 | Issue tracking (CRUD, search, deps, comments) |
| **AgentService** | `agent.proto` | 9 | Agent lifecycle (spawn, stop, nudge, watch) |
| **SlingService** | `sling.proto` | 5 | Work dispatch (assign beads to agents) |
| **MailService** | `mail.proto` | 6 | Inter-agent messaging |
| **DecisionService** | `decision.proto` | 6 | Human-in-the-loop decision gates |
| **ConvoyService** | `convoy.proto` | 6 | Batch work tracking |
| **TerminalService** | `terminal.proto` | 5 | Terminal output access (peek, watch, send input) |
| **ActivityService** | `activity.proto` | 4 | Event feed and log streaming |

---

## StatusService

Town and infrastructure health monitoring.

### GetTownStatus

Returns the full status of the town including all rigs and agents.

```
POST /gastown.v1.StatusService/GetTownStatus
```

**Request:**
```json
{
  "fast": true,
  "verbose": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `fast` | bool | Skip mail lookups for faster response |
| `verbose` | bool | Include detailed agent info |

**Response:** `TownStatus` with overseer info, global agents (Mayor, Deacon), and per-rig status.

### GetRigStatus

Returns status for a specific rig.

```
POST /gastown.v1.StatusService/GetRigStatus
```

**Request:**
```json
{
  "rig_name": "gastown"
}
```

### GetAgentStatus

Returns status for a specific agent.

```
POST /gastown.v1.StatusService/GetAgentStatus
```

**Request:**
```json
{
  "address": {
    "rig": "gastown",
    "role": "crew",
    "name": "mobile"
  }
}
```

### HealthCheck

Returns structured health of all system components (daemon, dolt, tmux, beads).

```
POST /gastown.v1.StatusService/HealthCheck
```

**Request:** `{}` (empty)

**Response:**
```json
{
  "status": "healthy",
  "components": [
    {"name": "daemon", "healthy": true, "latency_ms": 1, "message": "running"},
    {"name": "dolt", "healthy": true, "latency_ms": 12, "message": "connected"},
    {"name": "tmux", "healthy": true, "latency_ms": 3, "message": "server running"},
    {"name": "beads", "healthy": true, "latency_ms": 5, "message": "42 issues"}
  ]
}
```

### WatchStatus (streaming)

Streams status updates in real-time.

```
POST /gastown.v1.StatusService/WatchStatus
```

**Request:**
```json
{
  "rigs": ["gastown"],
  "include_agents": true
}
```

**Response:** Server-sent stream of `StatusUpdate` messages (town, rig, or agent updates).

---

## BeadsService

Full issue tracking CRUD — the primary data service.

### ListIssues

```
POST /gastown.v1.BeadsService/ListIssues
```

**Request:**
```json
{
  "status": "open",
  "type": "ISSUE_TYPE_TASK",
  "assignee": "gastown/crew/mobile",
  "priority": 2,
  "limit": 20,
  "offset": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `open`, `closed`, `in_progress`, `blocked`, `deferred` |
| `type` | IssueType | Filter by type enum |
| `label` | string | Filter by label |
| `priority` | int32 | 0-4 (-1 = no filter) |
| `parent` | string | Filter by parent issue ID |
| `assignee` | string | Filter by assignee |
| `no_assignee` | bool | Only unassigned issues |
| `limit` | int32 | Max results |
| `offset` | int32 | Pagination offset |

### GetIssue

```
POST /gastown.v1.BeadsService/GetIssue
```

**Request:**
```json
{"id": "gt-abc123"}
```

### CreateIssue

```
POST /gastown.v1.BeadsService/CreateIssue
```

**Request:**
```json
{
  "title": "Fix login flow",
  "type": "ISSUE_TYPE_BUG",
  "priority": 1,
  "description": "Login fails when...",
  "assignee": "gastown/crew/mobile",
  "labels": ["auth", "urgent"],
  "actor": "gastown/crew/mobile"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | **Required.** Issue title |
| `type` | IssueType | Default: `ISSUE_TYPE_TASK` |
| `priority` | int32 | 0-4 (0=critical, 4=backlog) |
| `description` | string | Full description |
| `parent` | string | Parent issue ID for hierarchy |
| `assignee` | string | Initial assignee |
| `labels` | string[] | Initial labels |
| `actor` | string | Who is creating (for attribution) |
| `id` | string | Specific ID (for deterministic IDs like agent beads) |
| `ephemeral` | bool | Don't export to JSONL |

### UpdateIssue

```
POST /gastown.v1.BeadsService/UpdateIssue
```

**Request:**
```json
{
  "id": "gt-abc123",
  "status": "in_progress",
  "assignee": "gastown/crew/mobile",
  "add_labels": ["wip"]
}
```

All fields except `id` are optional — only set fields are updated.

### CloseIssues

Close one or more issues at once.

```
POST /gastown.v1.BeadsService/CloseIssues
```

**Request:**
```json
{
  "ids": ["gt-abc123", "gt-def456"],
  "reason": "Completed in PR #42"
}
```

### ReopenIssues

```
POST /gastown.v1.BeadsService/ReopenIssues
```

**Request:**
```json
{"ids": ["gt-abc123"]}
```

### SearchIssues

Full-text search across issues.

```
POST /gastown.v1.BeadsService/SearchIssues
```

**Request:**
```json
{
  "query": "login auth",
  "status": "open",
  "type": "ISSUE_TYPE_BUG",
  "limit": 10
}
```

### GetReadyIssues

Returns issues ready to work (open, no blocking dependencies).

```
POST /gastown.v1.BeadsService/GetReadyIssues
```

**Request:**
```json
{
  "label": "auth",
  "limit": 5
}
```

### GetBlockedIssues

Returns issues blocked by unresolved dependencies.

```
POST /gastown.v1.BeadsService/GetBlockedIssues
```

**Request:**
```json
{"limit": 20}
```

### AddDependency / RemoveDependency

```
POST /gastown.v1.BeadsService/AddDependency
```

**Request:**
```json
{
  "issue_id": "gt-def456",
  "depends_on_id": "gt-abc123",
  "type": "DEPENDENCY_TYPE_DEPENDS_ON"
}
```

Dependency types: `DEPENDS_ON`, `BLOCKS`, `TRACKS`.

### ListDependencies

```
POST /gastown.v1.BeadsService/ListDependencies
```

**Request:**
```json
{
  "issue_id": "gt-abc123",
  "direction": "down"
}
```

Direction: `up` (what this depends on) or `down` (what depends on this).

### AddComment / ListComments

```
POST /gastown.v1.BeadsService/AddComment
```

**Request:**
```json
{
  "issue_id": "gt-abc123",
  "text": "Found the root cause — null pointer in auth middleware",
  "author": "gastown/crew/mobile"
}
```

### ManageLabels

Add or remove labels from an issue.

```
POST /gastown.v1.BeadsService/ManageLabels
```

**Request:**
```json
{
  "issue_id": "gt-abc123",
  "add": ["reviewed", "ready"],
  "remove": ["needs-review"]
}
```

### GetStats

```
POST /gastown.v1.BeadsService/GetStats
```

**Request:** `{}`

**Response:**
```json
{
  "total_issues": 142,
  "open_issues": 38,
  "closed_issues": 96,
  "in_progress_issues": 5,
  "blocked_issues": 3
}
```

---

## AgentService

Manage crew workers and polecats (ephemeral agents).

### ListAgents

```
POST /gastown.v1.AgentService/ListAgents
```

**Request:**
```json
{
  "rig": "gastown",
  "type": "AGENT_TYPE_CREW",
  "include_stopped": false,
  "include_global": true
}
```

Agent types: `CREW`, `POLECAT`, `WITNESS`, `REFINERY`, `MAYOR`, `DEACON`.

### GetAgent

```
POST /gastown.v1.AgentService/GetAgent
```

**Request:**
```json
{"agent": "gastown/crew/mobile"}
```

**Response:** Agent details plus last 20 lines of terminal output.

### SpawnPolecat

Creates a new ephemeral agent in a rig.

```
POST /gastown.v1.AgentService/SpawnPolecat
```

**Request:**
```json
{
  "rig": "gastown",
  "name": "furiosa",
  "account": "default",
  "hook_bead": "gt-abc123"
}
```

### StartCrew / StopAgent

```
POST /gastown.v1.AgentService/StartCrew
```

**Request:**
```json
{
  "rig": "gastown",
  "name": "mobile",
  "create": true
}
```

```
POST /gastown.v1.AgentService/StopAgent
```

**Request:**
```json
{
  "agent": "gastown/crew/mobile",
  "force": false,
  "reason": "Maintenance window"
}
```

### CreateCrew / RemoveCrew

Create or remove crew workspaces. These write agent beads; the controller watches for bead events and manages pods.

```
POST /gastown.v1.AgentService/CreateCrew
```

**Request:**
```json
{
  "name": "backend",
  "rig": "gastown",
  "branch": true
}
```

```
POST /gastown.v1.AgentService/RemoveCrew
```

**Request:**
```json
{
  "name": "backend",
  "rig": "gastown",
  "purge": false,
  "reason": "No longer needed"
}
```

### NudgeAgent

Send a message to an agent's terminal session.

```
POST /gastown.v1.AgentService/NudgeAgent
```

**Request:**
```json
{
  "agent": "gastown/polecats/furiosa",
  "message": "Please prioritize the auth fix",
  "urgent": true
}
```

### PeekAgent

Read recent terminal output from an agent.

```
POST /gastown.v1.AgentService/PeekAgent
```

**Request:**
```json
{
  "agent": "gastown/crew/mobile",
  "lines": 100
}
```

### WatchAgents (streaming)

Stream agent status updates.

```
POST /gastown.v1.AgentService/WatchAgents
```

**Request:**
```json
{
  "rig": "gastown",
  "include_global": true,
  "interval_ms": 5000
}
```

**Response stream:** `AgentUpdate` messages with `update_type`: `spawned`, `started`, `stopped`, `state_changed`.

---

## SlingService

Work dispatch — assigning beads to agents.

### Sling

Assign a bead to a target agent. Optionally spawns a new polecat.

```
POST /gastown.v1.SlingService/Sling
```

**Request:**
```json
{
  "bead_id": "gt-abc123",
  "target": "gastown",
  "args": "Fix the login flow, focus on the OAuth callback",
  "create": true,
  "merge_strategy": "MERGE_STRATEGY_MR"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `bead_id` | string | Issue to assign |
| `target` | string | Agent/rig path (empty = self) |
| `args` | string | Natural language instructions |
| `subject` | string | Context subject |
| `message` | string | Context message |
| `create` | bool | Spawn polecat if needed |
| `force` | bool | Force spawn even with unread mail |
| `no_convoy` | bool | Skip auto-convoy creation |
| `convoy` | string | Add to existing convoy |
| `no_merge` | bool | Skip merge queue |
| `merge_strategy` | MergeStrategy | `DIRECT`, `MR`, or `LOCAL` |
| `account` | string | Claude Code account handle |
| `agent` | string | Override agent runtime |

**Response:**
```json
{
  "bead_id": "gt-abc123",
  "target_agent": "gastown/polecats/furiosa",
  "convoy_id": "convoy-xyz",
  "polecat_spawned": true,
  "polecat_name": "furiosa",
  "bead_title": "Fix login flow"
}
```

### SlingFormula

Instantiate and sling a formula (template).

```
POST /gastown.v1.SlingService/SlingFormula
```

**Request:**
```json
{
  "formula": "code-review",
  "target": "gastown",
  "on_bead": "gt-abc123",
  "vars": {"reviewer": "alice", "branch": "feature/auth"},
  "create": true
}
```

### SlingBatch

Sling multiple beads in parallel, each to its own polecat.

```
POST /gastown.v1.SlingService/SlingBatch
```

**Request:**
```json
{
  "bead_ids": ["gt-abc123", "gt-def456", "gt-ghi789"],
  "rig": "gastown",
  "args": "Complete each task independently",
  "create_convoy": true,
  "convoy_name": "sprint-17"
}
```

### Unsling

Remove work from an agent's hook.

```
POST /gastown.v1.SlingService/Unsling
```

**Request:**
```json
{
  "bead_id": "gt-abc123",
  "force": true
}
```

### GetWorkload

Return all hooked work for an agent.

```
POST /gastown.v1.SlingService/GetWorkload
```

**Request:**
```json
{"agent": "gastown/crew/mobile"}
```

---

## MailService

Inter-agent and human messaging.

### SendMessage

```
POST /gastown.v1.MailService/SendMessage
```

**Request:**
```json
{
  "to": {"rig": "gastown", "role": "crew", "name": "mobile"},
  "subject": "Code review needed",
  "body": "Please review PR #42 when you have a chance",
  "priority": "PRIORITY_HIGH",
  "type": "MESSAGE_TYPE_TASK",
  "delivery": "DELIVERY_QUEUE"
}
```

Message types: `TASK`, `SCAVENGE`, `NOTIFICATION`, `REPLY`.
Delivery modes: `QUEUE` (inbox), `INTERRUPT` (injected into terminal).

### ListInbox

```
POST /gastown.v1.MailService/ListInbox
```

**Request:**
```json
{
  "address": {"rig": "gastown", "role": "crew", "name": "mobile"},
  "unread_only": true,
  "limit": 20
}
```

### ReadMessage / MarkRead / DeleteMessage

```
POST /gastown.v1.MailService/ReadMessage
```

```json
{"message_id": "msg-abc123"}
```

### WatchInbox (streaming)

Stream new messages in real-time.

```
POST /gastown.v1.MailService/WatchInbox
```

**Request:**
```json
{
  "address": {"rig": "gastown", "role": "crew", "name": "mobile"}
}
```

---

## DecisionService

Human-in-the-loop decision gates for agent workflows.

### CreateDecision

```
POST /gastown.v1.DecisionService/CreateDecision
```

**Request:**
```json
{
  "question": "Deploy auth service to production?",
  "context": "All tests passing. 3 PRs merged since last deploy.",
  "options": [
    {"label": "Yes, deploy now", "description": "Push to prod immediately", "recommended": true},
    {"label": "No, defer", "description": "Wait for next release window"}
  ],
  "requested_by": {"rig": "gastown", "role": "crew", "name": "mobile"},
  "urgency": "URGENCY_HIGH",
  "blockers": ["gt-abc123"],
  "parent_bead": "gt-epic-456"
}
```

### ListPending

```
POST /gastown.v1.DecisionService/ListPending
```

**Request:**
```json
{
  "min_urgency": "URGENCY_MEDIUM",
  "requested_by": "gastown/crew/mobile"
}
```

### Resolve

```
POST /gastown.v1.DecisionService/Resolve
```

**Request:**
```json
{
  "decision_id": "dec-abc123",
  "chosen_index": 1,
  "rationale": "Tests green, staging verified, deploy window open"
}
```

`chosen_index` is **1-indexed** (1 = first option).

### Cancel

```
POST /gastown.v1.DecisionService/Cancel
```

**Request:**
```json
{
  "decision_id": "dec-abc123",
  "reason": "No longer relevant — superseded by dec-def456"
}
```

### WatchDecisions (streaming)

```
POST /gastown.v1.DecisionService/WatchDecisions
```

**Request:**
```json
{"min_urgency": "URGENCY_LOW"}
```

---

## ConvoyService

Track batches of related work items.

### CreateConvoy

```
POST /gastown.v1.ConvoyService/CreateConvoy
```

**Request:**
```json
{
  "name": "Sprint 17 auth fixes",
  "issue_ids": ["gt-abc123", "gt-def456"],
  "owner": "gastown/crew/mobile",
  "merge_strategy": "mr"
}
```

### ListConvoys

```
POST /gastown.v1.ConvoyService/ListConvoys
```

**Request:**
```json
{
  "status": "CONVOY_STATUS_FILTER_OPEN",
  "tree": true
}
```

### GetConvoyStatus

```
POST /gastown.v1.ConvoyService/GetConvoyStatus
```

**Request:**
```json
{"convoy_id": "convoy-xyz"}
```

**Response:**
```json
{
  "convoy": {
    "id": "convoy-xyz",
    "title": "Sprint 17 auth fixes",
    "status": "open",
    "progress": "2/5"
  },
  "tracked": [
    {"id": "gt-abc123", "title": "Fix OAuth", "status": "closed", "assignee": "gastown/polecats/furiosa"},
    {"id": "gt-def456", "title": "Add MFA", "status": "in_progress", "worker": "gastown/crew/mobile"}
  ],
  "completed": 2,
  "total": 5
}
```

### AddToConvoy / CloseConvoy

```
POST /gastown.v1.ConvoyService/AddToConvoy
```

```json
{
  "convoy_id": "convoy-xyz",
  "issue_ids": ["gt-ghi789"]
}
```

### WatchConvoys (streaming)

```
POST /gastown.v1.ConvoyService/WatchConvoys
```

---

## TerminalService

Read and interact with agent terminal sessions.

### PeekSession

Capture terminal output from a tmux session.

```
POST /gastown.v1.TerminalService/PeekSession
```

**Request:**
```json
{
  "session": "gt-gastown-furiosa",
  "lines": 100,
  "all": false
}
```

### ListSessions

```
POST /gastown.v1.TerminalService/ListSessions
```

**Request:**
```json
{"prefix": "gt-"}
```

### HasSession

```
POST /gastown.v1.TerminalService/HasSession
```

**Request:**
```json
{"session": "gt-gastown-furiosa"}
```

### SendInput

Send text input to a terminal session. Used for remote nudging.

```
POST /gastown.v1.TerminalService/SendInput
```

**Request:**
```json
{
  "session": "gt-gastown-furiosa",
  "input": "Please check bd ready for new work",
  "nudge": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `session` | string | Session name |
| `input` | string | Text to send |
| `nudge` | bool | If true, send with Enter key and serialization. If false, raw keystrokes. |

### WatchSession (streaming)

Stream terminal output updates in real-time.

```
POST /gastown.v1.TerminalService/WatchSession
```

**Request:**
```json
{
  "session": "gt-gastown-furiosa",
  "lines": 50,
  "interval_ms": 1000
}
```

---

## ActivityService

Event feed and log streaming.

### ListEvents

```
POST /gastown.v1.ActivityService/ListEvents
```

**Request:**
```json
{
  "filter": {
    "types": ["sling", "hook", "done"],
    "actor": "gastown/crew/mobile",
    "after": "2026-02-18T00:00:00Z",
    "visibility": "VISIBILITY_FEED"
  },
  "limit": 50,
  "curated": true
}
```

Event types: `SLING`, `HOOK`, `UNHOOK`, `HANDOFF`, `DONE`, `MAIL`, `SPAWN`, `KILL`, `NUDGE`, `BOOT`, `HALT`, `SESSION_START`, `SESSION_END`, `SESSION_DEATH`, `DECISION_REQUESTED`, `DECISION_RESOLVED`, `MERGE_STARTED`, `MERGED`, `MERGE_FAILED`, and more.

### EmitEvent

Write a custom event to the activity log.

```
POST /gastown.v1.ActivityService/EmitEvent
```

**Request:**
```json
{
  "type": "deploy",
  "actor": "ci-bot",
  "payload": {"version": "2026.218.8", "environment": "production"},
  "visibility": "VISIBILITY_BOTH"
}
```

### StreamLogs (streaming)

Stream log entries from agent log sources.

```
POST /gastown.v1.ActivityService/StreamLogs
```

**Request:**
```json
{
  "agent": "gastown/crew/mobile",
  "log_type": "activity",
  "tail_lines": 50,
  "follow": true
}
```

Log types: `activity` (events feed), `town` (lifecycle log), `daemon` (daemon log).

### WatchEvents (streaming)

Stream events in real-time with optional backfill.

```
POST /gastown.v1.ActivityService/WatchEvents
```

**Request:**
```json
{
  "filter": {"types": ["decision_requested", "decision_resolved"]},
  "include_backfill": true,
  "backfill_count": 10
}
```

---

## Streaming Patterns

### Server-Sent Events (SSE)

Streaming RPCs (`WatchStatus`, `WatchAgents`, `WatchDecisions`, `WatchSession`, `WatchEvents`, `WatchInbox`, `WatchConvoys`, `StreamLogs`) use Connect-RPC's server streaming.

**Go client example:**

```go
stream, err := client.WatchDecisions(ctx, connect.NewRequest(&gastownv1.WatchDecisionsRequest{}))
if err != nil {
    log.Fatal(err)
}
for stream.Receive() {
    decision := stream.Msg()
    fmt.Printf("New decision: %s — %s\n", decision.Id, decision.Question)
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}
```

**curl SSE example:**

```bash
curl -N -X POST http://localhost:8443/gastown.v1.DecisionService/WatchDecisions \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: my-key" \
  -d '{"min_urgency": "URGENCY_LOW"}'
```

### Polling Fallback

The `rpcclient` package implements `WatchDecisions` as a polling loop (5-second interval) for environments where streaming isn't available. See `internal/rpcclient/client.go`.

---

## Proto Schema Versioning

### Package

All services are in the `gastown.v1` package. The `v1` suffix is the API version.

### Compatibility Rules

1. **Adding fields**: Always safe. New fields get default values for old clients.
2. **Removing fields**: Mark as `reserved` in proto. Never reuse field numbers.
3. **Renaming fields**: Safe for protobuf (wire format uses numbers). Breaks JSON clients.
4. **Adding enum values**: Safe. Unknown values preserved by protobuf.
5. **Adding RPCs**: Safe. Old clients simply don't call them.

### Breaking Changes

Breaking changes require a new version (`gastown.v2`):
- Changing field types or numbers
- Removing RPCs
- Changing RPC signatures
- Renaming services

### Code Generation

```bash
# Regenerate Go code from proto definitions
buf generate proto

# Lint proto files
buf lint proto

# Check backward compatibility
buf breaking --against .git#branch=main
```

Generated output:
- `gen/gastown/v1/*.pb.go` — Protobuf messages
- `gen/gastown/v1/gastownv1connect/*.go` — Connect-RPC handlers and clients

---

## Go Client Examples

### Using the rpcclient Package

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/steveyegge/gastown/internal/rpcclient"
)

func main() {
    // Create client
    client := rpcclient.NewClient("http://localhost:8443",
        rpcclient.WithAPIKey("my-secret-key"),
        rpcclient.WithTimeout(30*time.Second))

    ctx := context.Background()

    // List pending decisions
    decisions, err := client.ListPendingDecisions(ctx)
    if err != nil {
        log.Fatal(err)
    }
    for _, d := range decisions {
        fmt.Printf("[%s] %s (urgency: %s)\n", d.ID, d.Question, d.Urgency)
    }

    // Resolve a decision
    err = client.ResolveDecision(ctx, "dec-abc123", 1, "Approved — tests green")
    if err != nil {
        log.Fatal(err)
    }

    // Watch for new decisions
    client.WatchDecisions(ctx, func(d rpcclient.Decision) error {
        fmt.Printf("New: %s — %s\n", d.ID, d.Question)
        return nil
    })
}
```

### Using Raw Connect-RPC

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "connectrpc.com/connect"

    gastownv1 "github.com/steveyegge/gastown/gen/gastown/v1"
    "github.com/steveyegge/gastown/gen/gastown/v1/gastownv1connect"
)

func main() {
    // Create Connect-RPC clients
    beadsClient := gastownv1connect.NewBeadsServiceClient(
        http.DefaultClient,
        "http://localhost:8443",
        connect.WithInterceptors(authInterceptor("my-key")),
    )

    agentClient := gastownv1connect.NewAgentServiceClient(
        http.DefaultClient,
        "http://localhost:8443",
        connect.WithInterceptors(authInterceptor("my-key")),
    )

    ctx := context.Background()

    // List ready issues
    resp, err := beadsClient.GetReadyIssues(ctx,
        connect.NewRequest(&gastownv1.GetReadyIssuesRequest{Limit: 5}))
    if err != nil {
        log.Fatal(err)
    }
    for _, issue := range resp.Msg.Issues {
        fmt.Printf("[P%d] %s: %s\n", issue.Priority, issue.Id, issue.Title)
    }

    // List running agents
    agents, err := agentClient.ListAgents(ctx,
        connect.NewRequest(&gastownv1.ListAgentsRequest{
            Rig:            "gastown",
            IncludeGlobal:  true,
        }))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%d agents running\n", agents.Msg.Running)
}

// authInterceptor adds the API key header.
func authInterceptor(apiKey string) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            req.Header().Set("X-GT-API-Key", apiKey)
            return next(ctx, req)
        }
    }
}
```

---

## curl Examples

### Health Check

```bash
curl -s http://localhost:8443/health | jq .
```

### List Open Issues

```bash
curl -s -X POST http://localhost:8443/gastown.v1.BeadsService/ListIssues \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"status": "open", "limit": 10}' | jq .
```

### Create an Issue

```bash
curl -s -X POST http://localhost:8443/gastown.v1.BeadsService/CreateIssue \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "title": "Fix authentication timeout",
    "type": "ISSUE_TYPE_BUG",
    "priority": 1,
    "description": "OAuth tokens expire after 5 minutes instead of 1 hour",
    "labels": ["auth", "production"]
  }' | jq .
```

### Close Issues

```bash
curl -s -X POST http://localhost:8443/gastown.v1.BeadsService/CloseIssues \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"ids": ["gt-abc123"], "reason": "Fixed in commit abc123"}' | jq .
```

### Search Issues

```bash
curl -s -X POST http://localhost:8443/gastown.v1.BeadsService/SearchIssues \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"query": "login oauth", "status": "open"}' | jq .
```

### Get Ready Work

```bash
curl -s -X POST http://localhost:8443/gastown.v1.BeadsService/GetReadyIssues \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"limit": 5}' | jq .
```

### Sling Work to an Agent

```bash
curl -s -X POST http://localhost:8443/gastown.v1.SlingService/Sling \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "bead_id": "gt-abc123",
    "target": "gastown",
    "args": "Fix the OAuth callback — tokens expire too quickly",
    "create": true,
    "merge_strategy": "MERGE_STRATEGY_MR"
  }' | jq .
```

### List Agents

```bash
curl -s -X POST http://localhost:8443/gastown.v1.AgentService/ListAgents \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"rig": "gastown", "include_global": true}' | jq .
```

### Peek at Agent Terminal

```bash
curl -s -X POST http://localhost:8443/gastown.v1.AgentService/PeekAgent \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"agent": "gastown/crew/mobile", "lines": 50}' | jq -r .output
```

### Send a Decision

```bash
# Create a decision
curl -s -X POST http://localhost:8443/gastown.v1.DecisionService/CreateDecision \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "question": "Deploy to production?",
    "options": [
      {"label": "Yes", "recommended": true},
      {"label": "No"}
    ],
    "urgency": "URGENCY_HIGH"
  }' | jq .

# List pending decisions
curl -s -X POST http://localhost:8443/gastown.v1.DecisionService/ListPending \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{}' | jq .

# Resolve a decision (chose option 1)
curl -s -X POST http://localhost:8443/gastown.v1.DecisionService/Resolve \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "decision_id": "dec-abc123",
    "chosen_index": 1,
    "rationale": "All tests green, staging verified"
  }' | jq .
```

### Send Mail

```bash
curl -s -X POST http://localhost:8443/gastown.v1.MailService/SendMessage \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "to": {"rig": "gastown", "role": "crew", "name": "mobile"},
    "subject": "Review needed",
    "body": "Please review PR #42",
    "priority": "PRIORITY_HIGH"
  }' | jq .
```

### Watch Events (SSE)

```bash
# Stream activity events (ctrl-C to stop)
curl -N -X POST http://localhost:8443/gastown.v1.ActivityService/WatchEvents \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"include_backfill": true, "backfill_count": 5}'
```

### Convoy Operations

```bash
# Create a convoy
curl -s -X POST http://localhost:8443/gastown.v1.ConvoyService/CreateConvoy \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{
    "name": "Sprint 17",
    "issue_ids": ["gt-abc123", "gt-def456"],
    "owner": "gastown/crew/mobile"
  }' | jq .

# Check convoy progress
curl -s -X POST http://localhost:8443/gastown.v1.ConvoyService/GetConvoyStatus \
  -H "Content-Type: application/json" \
  -H "X-GT-API-Key: $GT_API_KEY" \
  -d '{"convoy_id": "convoy-xyz"}' | jq .
```
