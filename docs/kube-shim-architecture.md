# Kube Shim Architecture: Backend Abstraction and Known Gaps

> **Date**: 2026-02-14
> **Bead**: bd-1dbny
> **Parent Epic**: bd-gc81j (Sub-epic C: CLI shims for kube/beads-centric world)
> **Audience**: Contributors working on K8s habitat migration

## Purpose

This document captures the current state of the Backend interface abstraction —
how gastown CLI commands interact with agent terminal sessions — and catalogues
the known gaps that prevent full K8s-only operation. It serves as the reference
for the remaining K8s habitat work.

---

## 1. The Backend Interface

**File**: `internal/terminal/backend.go`

The `Backend` interface is the sole abstraction for all terminal I/O with agent
sessions. It has 18 methods covering session lifecycle, terminal capture, input,
environment, and state inspection.

```go
type Backend interface {
    // Session discovery
    HasSession(session string) (bool, error)

    // Terminal capture
    CapturePane(session string, lines int) (string, error)
    CapturePaneAll(session string) (string, error)
    CapturePaneLines(session string, lines int) ([]string, error)

    // Input
    NudgeSession(session string, message string) error
    SendKeys(session string, keys string) error
    SendInput(session string, text string, enter bool) error

    // Lifecycle
    KillSession(session string) error
    RespawnPane(session string) error
    SwitchSession(session string, cfg SwitchConfig) error
    AttachSession(session string) error
    IsPaneDead(session string) (bool, error)
    SetPaneDiedHook(session, agentID string) error

    // State
    IsAgentRunning(session string) (bool, error)
    GetAgentState(session string) (string, error)
    SetEnvironment(session, key, value string) error
    GetEnvironment(session, key string) (string, error)
    GetPaneWorkDir(session string) (string, error)
}
```

### Sole Implementation: CoopBackend

**File**: `internal/terminal/coop.go` (636 lines)

`CoopBackend` implements `Backend` by making HTTP requests to the Coop sidecar
API running alongside each agent pod. It maintains a `map[string]string` mapping
session names to Coop base URLs (e.g., `"claude" → "http://10.0.1.5:8080"`).

Every Backend method translates to an HTTP call:

| Method | Coop Endpoint |
|--------|---------------|
| `HasSession` | `GET /api/v1/health` |
| `CapturePane` | `GET /api/v1/screen/text` |
| `NudgeSession` | `POST /api/v1/agent/nudge` |
| `SendKeys` | `POST /api/v1/input/keys` |
| `SendInput` | `POST /api/v1/input` |
| `KillSession` | `POST /api/v1/signal` (SIGTERM) |
| `IsAgentRunning` | `GET /api/v1/status` |
| `GetAgentState` | `GET /api/v1/agent/state` |
| `SetEnvironment` | `PUT /api/v1/env/:key` |
| `GetEnvironment` | `GET /api/v1/env/:key` |
| `GetPaneWorkDir` | `GET /api/v1/session/cwd` |
| `SwitchSession` | `PUT /api/v1/session/switch` |
| `AttachSession` | `exec coop attach <url>` |
| `IsPaneDead` | `GET /api/v1/agent/state` (checks "exited"/"crashed") |
| `SetPaneDiedHook` | no-op |
| `RespawnPane` | `PUT /api/v1/session/switch` (switch-to-self) |

CoopBackend also exposes two methods beyond the interface:
- `AgentState(session) → *CoopAgentState` — rich state with prompt context
- `RespondToPrompt(session, req)` — answer permission/plan prompts

---

## 2. ResolveBackend: Routing to the Right Agent

**File**: `internal/terminal/resolve.go` (175 lines)

`ResolveBackend(agentID string) Backend` is the primary entry point. It:

1. Takes an agent identity string (e.g., `"gastown/crew/k8s"`, `"mayor"`)
2. Tries the ID as-is, then with `"hq-"` prefix for town-level shortnames
3. For each candidate, calls `resolveCoopConfig(id)`:
   - Runs `bd show <id> --json` to get the agent bead
   - Parses the bead's `notes` field for key-value metadata
   - Looks for: `coop_url`, `coop_token`, `pod_name`, `pod_namespace`
4. If found, creates a `CoopBackend` with the session `"claude"` registered
5. If not found, returns an empty `CoopBackend` (methods will error)

### Metadata Written by Controller

The controller's status reporter (`statusreporter/reporter.go`) writes backend
metadata to each agent bead's notes field during periodic sync:

```
backend: coop
pod_name: gt-gastown-crew-k8s
pod_namespace: gastown-next
coop_url: http://10.0.1.5:8080
```

This is the bridge between K8s pod discovery and the CLI's Backend abstraction.

### ResolveAgentPodInfo

`ResolveAgentPodInfo(address string) → *AgentPodInfo` provides K8s-specific
pod metadata (pod name, namespace, coop URL) for commands that need to do
`kubectl port-forward` or similar operations. Used by `gt peek` as a fallback.

---

## 3. Which Commands Use Backend

### Direct Backend Users (23 core files)

| Command | Backend Methods Used | Notes |
|---------|---------------------|-------|
| `gt peek` | `ResolveBackend`, `CapturePaneLines` | Port-forward fallback for cross-namespace |
| `gt nudge` | `ResolveBackend` (5 calls), `HasSession`, `NudgeSession`, `IsAgentRunning` | Universal messaging; role shortcuts |
| `gt down` | `NewCoopBackend`, `HasSession`, `SendKeys`, `KillSession`, `ResolveBackend` | Graceful shutdown orchestration |
| `gt recover` | `HasSession`, `SendKeys`, `NudgeSession`, `IsPaneDead`, `KillSession` | Multi-level agent recovery |
| `gt exit` | `ResolveBackend`, `HasSession`, `KillSession` | Session termination |
| `gt done` | Backend via session kill | Work completion cleanup |
| `gt start` | `ResolveBackend` | Session initialization |
| `gt context` | `ResolveBackend`, `CapturePaneLines` | Context usage monitoring |
| `gt session` | `ResolveBackend`, `HasSession`, `KillSession`, `NudgeSession` | Session management |
| `gt deacon` | `ResolveBackend`, `HasSession`, `NudgeSession`, `KillSession` | Deacon lifecycle |
| `gt statusline` | `NewCoopBackend`, `GetEnvironment`, `GetPaneWorkDir`, `CapturePaneLines`, `IsAgentRunning` | Status bar generation |
| `gt handoff` | Backend for session operations | Work handoff between agents |
| `gt switch` | Backend for session switch | Credential rotation |
| `gt costs` | Backend for work directory | Cost tracking |
| `gt mail` (router) | `NewCoopBackend`, `HasSession`, `NudgeSession`, `ResolveBackend` | Mail notification delivery |
| `gt mail send` | `ResolveBackend`, `NudgeSession` | Direct mail nudge |

### Manager-Level Backend Users (8 packages)

Each agent role manager takes a `Backend` parameter in its constructor:

| Package | Manager | Key Methods |
|---------|---------|-------------|
| `internal/polecat` | `SessionManager` | `SendInput`, `IsAgentRunning`, `HasSession` |
| `internal/crew` | `Manager` | `HasSession`, `KillSession` |
| `internal/witness` | `Manager` | Backend for polecat management |
| `internal/refinery` | `Manager` | Backend for session ops |
| `internal/deacon` | `Manager` | Backend for dog management |
| `internal/mayor` | `Manager` | Backend for town-level ops |
| `internal/dog` | `SessionManager` | `HasSession`, `KillSession`, `GetPaneWorkDir` |
| `internal/boot` | `boot.go` | Backend for watchdog |

### Session Registry

**File**: `internal/registry/registry.go`

The `SessionRegistry` discovers all agent sessions by querying the daemon for
agent beads and reading their backend metadata. It powers `gt status` and
provides the session list for commands that operate on multiple agents.

---

## 4. Known Gaps and K8s Incompatibilities

### Gap A: `exec.Command("gt", ...)` — 26 invocations across 19 files

These shell out to the `gt` binary instead of calling Go functions directly.
In K8s agent pods, the `gt` binary is available, so these technically work,
but they're fragile (subprocess overhead, error handling, PATH assumptions).

**Most common subcommands:**

| Subcommand | Count | Files |
|------------|-------|-------|
| `mail` (inbox/send/delete/check) | 8 | daemon/lifecycle.go, cmd/resume.go, cmd/polecat_spawn.go, cmd/prime.go, cmd/deacon.go, sling/spawn.go, daemon/daemon.go |
| `convoy check` | 2 | daemon/convoy_watcher.go, convoy/observer.go |
| `sling` | 2 | cmd/formula.go, cmd/swarm.go |
| `nudge` | 1 | notify/decision_resolved.go |
| `daemon stop/run` | 1 | doctor/repo_fingerprint_check.go |
| `boot triage` | 1 | boot/boot.go |
| `rig boot` | 1 | sling/spawn.go |
| Others | 8 | Various |

**Bead**: bd-5buh8 (P2) — Replace exec.Command("gt",...) with direct calls

### Gap B: `workspace.FindFromCwd()` — 116 files

Many commands locate the town root by walking up from the current working
directory. In K8s pods the workspace is always at a known path
(`/home/agent/gt`), but `FindFromCwd()` still works because the entrypoint
sets the working directory correctly.

**Assessment**: Not a blocker. The function works in K8s. It's a design smell
(should use explicit config rather than CWD probing) but doesn't prevent
K8s-only operation. No immediate action needed.

### Gap C: Remaining tmux references — 4 files

| File | Status |
|------|--------|
| `internal/terminal/tmux_shim.go` | **DELETE** — legacy compatibility shim |
| `internal/terminal/connection.go` | **REVIEW** — may contain tmux code paths |
| `scripts/macbook-tmux-session.sh` | Keep — local dev tool, not deployed |
| `docs/tmux-session-namespace-design.md` | Keep — historical documentation |

**Beads**: bd-e52ls (P0, blocked), bd-0kka6 (P1)

### Gap D: `LocalConnection` in connection package

**Bead**: bd-0kka6 (P1)

The connection package still has a `LocalConnection` type for direct tmux
interaction. This must be removed — all connections should go through
`CoopBackend` via `ResolveBackend()`.

### Gap E: NudgeAgent RPC still uses tmux path

**Bead**: bd-0hum5 (P2)

The `NudgeAgent` RPC handler in the daemon falls back to tmux-style session
names. Should use `ResolveBackend()` → `CoopBackend.NudgeSession()`.

### Gap F: K8sConnection → coop direct

**Bead**: bd-f1inf (P1)

`K8sConnection` type uses `kubectl exec` for terminal I/O. Should be replaced
with direct Coop HTTP calls via `CoopBackend`.

### Gap G: tmux default in 11 managers

**Bead**: bd-t51zq (P1)

Several role managers default to creating tmux sessions when no Backend is
provided. These defaults should be removed — Backend is now always injected.

### Gap H: LocalBackend() in handoff.go

**Bead**: bd-541oe (P2)

`handoff.go` has a `LocalBackend()` fallback that creates a tmux backend.
Should be removed — handoff should require an explicit Backend.

### Gap I: SSH/Tmux integration tests

**Bead**: bd-tecmp (P2)

Integration tests that spin up tmux sessions for testing. Should be rewritten
to use Coop mocks or deleted.

---

## 5. Dependency Graph for Remaining Work

```
bd-e52ls (P0): Delete internal/tmux/
  ├── BLOCKED BY bd-0hum5: Rewrite NudgeAgent RPC → coop API
  ├── BLOCKED BY bd-0kka6: Remove LocalConnection
  └── BLOCKED BY bd-5buh8: Replace exec.Command("gt",...) with direct calls

bd-t51zq (P1): Remove tmux default from managers
  └── Independent

bd-f1inf (P1): K8sConnection → coop direct
  └── Independent

bd-541oe (P2): Remove LocalBackend() in handoff.go
  └── Independent

bd-tecmp (P2): Delete SSH/Tmux integration tests
  └── Independent
```

---

## 6. Architecture Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│  CLI Command (gt peek, gt nudge, gt down, ...)                   │
│                                                                  │
│  1. Resolve agent identity                                       │
│  2. Call terminal.ResolveBackend(agentID)                         │
│     └─→ bd show <id> --json → parse notes → coop_url             │
│  3. Use Backend methods (CapturePane, NudgeSession, ...)         │
└──────────────────────┬───────────────────────────────────────────┘
                       │ HTTP
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  CoopBackend                                                     │
│  sessions: {"claude" → "http://10.0.1.5:8080"}                   │
│                                                                  │
│  NudgeSession("claude", msg)                                     │
│    → POST http://10.0.1.5:8080/api/v1/agent/nudge                │
│                                                                  │
│  CapturePane("claude", 50)                                       │
│    → GET http://10.0.1.5:8080/api/v1/screen/text                 │
└──────────────────────┬───────────────────────────────────────────┘
                       │ HTTP (pod IP:8080)
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  Coop Sidecar (inside agent pod)                                 │
│  ├── PTY management (agent process)                              │
│  ├── Port 8080: Full API                                         │
│  ├── Port 9090: Health-only                                      │
│  └── NATS: Event pub/sub                                         │
└──────────────────────────────────────────────────────────────────┘
```

### Backend Metadata Flow

```
Controller status reporter (every 60s)
  │
  ├─ Lists pods with label app.kubernetes.io/name=gastown
  ├─ For each pod:
  │   ├─ Read gastown.io/{agent,rig,role} labels → reconstruct bead ID
  │   ├─ Read pod IP + container ports → construct coop_url
  │   └─ Write to bead notes via daemon HTTP API:
  │       backend: coop
  │       coop_url: http://<pod-ip>:8080
  │       pod_name: <pod-name>
  │       pod_namespace: <namespace>
  │
  └─ ResolveBackend() reads these notes on every call
```

---

## 7. Session Naming Convention

All Coop-based sessions use the name `"claude"` — the Coop API doesn't use
session names internally, but the Backend interface requires one. The session
name is used as a map key in `CoopBackend.sessions` to look up the base URL.

For multi-session pods (not currently used), additional session names could be
registered via `AddSession()`.

Legacy tmux naming (now dead): `gt-<rig>-<role>-<agent>` (e.g.,
`gt-gastown-crew-k8s`). These names appear in some old code paths but are
never matched by Coop sessions.
