# Controller Agent Bead Creation Flow Audit

> **Task**: bd-puds.3
> **Date**: 2026-02-10
> **Status**: AUDIT COMPLETE

## Executive Summary

The controller operates in a **bead-first** architecture: agent beads must exist in Dolt before pods are created. The controller **never creates agent beads**. It only reads them (via daemon HTTP API) and creates/deletes K8s pods to match. The gap where pods exist without corresponding beads occurs when the bead-creation step is skipped or the beads lack required labels.

---

## 1. Controller Architecture Overview

The controller is a standalone Go binary (`controller/cmd/controller/main.go`) that runs as a K8s Deployment. It is NOT a traditional controller-runtime operator -- it has no CRDs, no informers, and no watches on K8s resources. Instead, it:

1. **Watches beads events** via SSE or JetStream (daemon mutation stream)
2. **Periodically reconciles** desired state (agent beads) against actual state (K8s pods)
3. **Creates/deletes pods** to converge

### Two Paths to Pod Creation

**Path A: Event-driven (real-time)**
- `beadswatcher` (SSE or NATS) receives a mutation event from the daemon
- `handleEvent()` maps it to `AgentSpawn`, `AgentDone`, `AgentKill`, or `AgentStuck`
- On `AgentSpawn`: calls `pods.CreateAgentPod()` to create a K8s pod
- On `AgentDone`/`AgentKill`: calls `pods.DeleteAgentPod()`

**Path B: Reconciler (periodic, every 60s)**
- `reconciler.Reconcile()` runs on startup and every `SyncInterval` (default 60s)
- Calls `daemon.ListAgentBeads()` to get desired state from Dolt
- Calls `pods.ListAgentPods()` to get actual state from K8s
- Creates missing pods, deletes orphan pods, recreates failed pods

---

## 2. What Triggers Pod Creation

### Event-Driven Path

The `beadswatcher` triggers `AgentSpawn` on two conditions:
- **Mutation type `create`** on an agent bead (issue with `gt:agent` label or `issue_type=agent`)
- **Status change to `in_progress`** on an agent bead (reactivation)

Key file: `controller/internal/beadswatcher/watcher.go`, lines 281-307.

### Reconciler Path

The reconciler calls `daemonclient.ListAgentBeads()` which queries:
```
POST /bd.v1.BeadsService/List
{
  "exclude_status": ["closed"],
  "labels": ["gt:agent", "execution_target:k8s"]
}
```

Key file: `controller/internal/daemonclient/client.go`, lines 79-145.

Both labels (`gt:agent` AND `execution_target:k8s`) are required. The daemon-side query uses AND semantics. There is also a client-side filter at line 122 that double-checks `execution_target:k8s`.

---

## 3. Does the Controller Check for Existing Agent Beads Before Creating Pods?

**Yes, but indirectly.** The controller does not "check" -- it derives pod creation entirely from bead existence:

- **Event path**: A pod is created whenever an `AgentSpawn` event fires. There is no check for whether the pod already exists before calling `CreateAgentPod()`. If the pod already exists, the K8s API returns an `AlreadyExists` error.
- **Reconciler path**: The reconciler explicitly diffs desired (beads) against actual (pods) and only creates pods in the `desired` set that are not in the `actual` set (lines 100-122 of `reconciler.go`).

---

## 4. Does the Controller CREATE Agent Beads?

**No.** The controller never creates, updates, or closes agent beads in Dolt. It is purely a consumer of bead state.

The only write operations the controller performs on beads are:
1. **`status.ReportBackendMetadata()`** -- writes `coop_url`, `pod_name`, `pod_namespace`, and `backend` into the bead's `notes` field via `daemon.UpdateBeadNotes()` (HTTP POST to `/bd.v1.BeadsService/Update`)
2. **`status.ReportPodStatus()`** -- in the `HTTPReporter`, this only logs (no actual write). The `BdReporter` variant calls `bd agent state` but is not used in production (production uses `HTTPReporter`).

---

## 5. Who Creates Agent Beads?

Agent beads are created by **gt/bd CLI commands** running either locally or inside existing agent pods:

| Agent Type | Creator | Function | Bead ID Pattern |
|-----------|---------|----------|----------------|
| Mayor | `gt mayor start --k8s` | `runMayorStartK8s()` in `internal/cmd/mayor.go:146` | `hq-mayor` |
| Deacon | `gt deacon start --k8s` | Similar pattern | `hq-deacon` |
| Polecat | `gt sling` (when execution_target=k8s) | `spawnPolecatForK8sCMD()` in `internal/cmd/polecat_spawn.go:594` | `{prefix}-{rig}-polecat-{name}` |
| Polecat | `gt sling` (via sling package) | `spawnPolecatForK8s()` in `internal/sling/spawn.go:367` | Same as above |
| Crew | `registry.CreateSession()` | `internal/registry/registry.go:395` | `{prefix}-{rig}-crew-{name}` |
| Witness/Refinery | `gt doctor --fix` | `internal/doctor/agent_beads_check.go:179` | `{prefix}-{rig}-witness` / `{prefix}-{rig}-refinery` |
| Any role | `registry.CreateSession()` | Generic session creation | Variable |

### Bead Creation Steps

Each of these creators performs two critical steps:

1. **Create the agent bead** via `beadsClient.CreateOrReopenAgentBead()` with:
   - `type=agent`
   - `labels=gt:agent`
   - `status=pinned`
   - `agent_state=spawning`

2. **Add the `execution_target:k8s` label** via `beadsClient.AddLabel(id, "execution_target:k8s")`

Both steps are required for the controller to pick up the bead.

---

## 6. Labels and Fields the Controller Expects

### Required Labels (for reconciler ListAgentBeads)

| Label | Purpose | Added By |
|-------|---------|----------|
| `gt:agent` | Identifies this as an agent bead | `CreateAgentBead()` (automatic) |
| `execution_target:k8s` | Tells controller this agent needs a K8s pod | Explicit `AddLabel()` call |

### Labels Used for Identity Parsing (preferred)

| Label | Example | Purpose |
|-------|---------|---------|
| `rig:{name}` | `rig:beads` | Identifies the rig |
| `role:{type}` | `role:polecat` | Identifies the role |
| `agent:{name}` | `agent:coral` | Identifies the agent name |

**These structured labels are NOT added by the standard bead creation flow.** They appear in test fixtures (e.g., `watcher_test.go:389`) but the production code in `polecat_spawn.go`, `mayor.go`, `sling/spawn.go`, and `registry.go` does NOT add `rig:`, `role:`, or `agent:` labels.

### Fallback Identity Parsing

When structured labels are absent, the controller falls back to:
1. **Actor field** parsing: `"gastown/polecats/rictus"` parsed as `rig/role/name`
2. **Bead ID parsing**: `parseAgentBeadID()` which handles patterns like:
   - `hq-mayor` -> rig=town, role=mayor, name=hq
   - `gt-gastown-polecat-toast` -> rig=gastown, role=polecat, name=toast

File: `controller/internal/beadswatcher/watcher.go:347-414` and `controller/internal/daemonclient/client.go:292-306`.

### Pod Name Derivation

Pod names are derived as: `gt-{rig}-{role}-{name}` (see `AgentPodSpec.PodName()` at `podmanager/manager.go:185`).

### Pod Labels

Each agent pod gets these K8s labels:
- `app.kubernetes.io/name=gastown`
- `gastown.io/rig={rig}`
- `gastown.io/role={role}`
- `gastown.io/agent={name}`

The `gastown.io/agent` label is critical -- the reconciler uses it to distinguish agent pods from infrastructure pods (controller itself, git-mirror, etc.) at line 84 of `reconciler.go`.

---

## 7. Where coop_url is Set in Bead Notes

After creating a pod on `AgentSpawn`, the controller writes backend metadata to the agent bead's notes field:

```go
// main.go:197-207
if spec.CoopSidecar != nil {
    coopPort := spec.CoopSidecar.Port
    if coopPort == 0 {
        coopPort = podmanager.CoopDefaultPort  // 8080
    }
    _ = status.ReportBackendMetadata(ctx, agentBeadID, statusreporter.BackendMetadata{
        PodName:   spec.PodName(),
        Namespace: spec.Namespace,
        Backend:   "coop",
        CoopURL:   fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", spec.PodName(), spec.Namespace, coopPort),
    })
}
```

This writes to the bead's notes via `daemon.UpdateBeadNotes()` (HTTP POST to `/bd.v1.BeadsService/Update`). The notes contain:
```
backend: coop
pod_name: gt-gastown-polecat-furiosa
pod_namespace: gastown-uat
coop_url: http://gt-gastown-polecat-furiosa.gastown-uat.svc.cluster.local:8080
```

On `AgentDone`/`AgentKill`, the backend metadata is cleared (empty `BackendMetadata{}`).

**Note**: When `CoopBuiltin` is true (as in gastown-uat), `spec.CoopSidecar` is nil, so backend metadata is NOT written via this code path. The CoopURL is set differently: the agent's built-in coop is accessible at the same pod IP.

---

## 8. Helm Chart Configuration

### Controller Deployment Template

File: `helm/gastown/templates/agent-controller/deployment.yaml`

Key env vars injected into the controller:

| Env Var | Source | Purpose |
|---------|--------|---------|
| `NAMESPACE` | `.Release.Namespace` | K8s namespace |
| `BD_DAEMON_HOST` | Computed from release name | Daemon service FQDN |
| `BD_DAEMON_PORT` | `bd-daemon.daemon.tcp.port` (9876) | Daemon RPC port |
| `BD_DAEMON_HTTP_PORT` | `bd-daemon.daemon.http.port` (9080) | Daemon HTTP port for SSE + API |
| `BD_DAEMON_TOKEN` | K8s Secret | Auth token |
| `AGENT_IMAGE` | `agentController.agentImage` | Default image for spawned pods |
| `COOP_IMAGE` or `COOP_BUILTIN` | Config | Coop mode |
| `CLAUDE_CREDENTIALS_SECRET` | Config | Claude OAuth credentials |
| `DAEMON_TOKEN_SECRET` | Config or default | Token for agent pods |
| `GT_TOWN_NAME` | Config or release name | Town identifier |
| `NATS_URL` | Config or auto from NATS subchart | Event transport |

### gastown-uat Values

- `agentController.enabled: true`
- `agentController.coopBuiltin: true` (no sidecar, built into agent image)
- `agentController.credentialsSecret: "claude-credentials"`
- `agentController.townName: "gastown-uat"`
- `agentController.natsURL: ""` (SSE transport, not NATS)

### gastown-ha Values

- No `agentController` section at all (not deployed)
- Uses `bd-daemon` chart directly (not the `gastown` wrapper)

---

## 9. The Gap: Why Pods Exist Without Agent Beads

### Root Cause Analysis

The system is designed as **bead-first**: someone must create an agent bead with `gt:agent` + `execution_target:k8s` labels, and only then does the controller create a pod. There is no reverse path where the controller creates beads for pods.

Pods can exist without corresponding beads in these scenarios:

#### Scenario 1: Bead Created Then Closed, Pod Still Running

The reconciler only queries beads with `exclude_status=["closed"]`. If an agent bead is closed (via `gt done`, `CloseAndClearAgentBead`, or manual close), the reconciler will see the pod as an orphan and delete it on the next sync cycle. However, there is a 60-second window where the pod runs without a matching bead in the desired set.

#### Scenario 2: Bead Created Without `execution_target:k8s` Label

If an agent bead is created with `gt:agent` but WITHOUT `execution_target:k8s`:
- The **event path** still fires (the watcher looks at `gt:agent` or `issue_type=agent`, NOT at `execution_target:k8s`)
- The **reconciler** will NOT list it (ListAgentBeads filters on both labels)
- Result: Event creates a pod, but reconciler doesn't know about the bead. On next sync, the reconciler sees an orphan pod and deletes it.

#### Scenario 3: Bead Exists in Wrong Database or With Wrong ID Format

If the bead ID can't be parsed by `parseAgentBeadID()` or `extractFromLabels()`, the event is dropped (line 312-314 of watcher.go: `if role == "" || name == "" { return Event{}, false }`). But the reconciler would still try to parse it, and if parsing fails, `rig == "" || role == "" || name == ""` causes the bead to be skipped (client.go:133-134).

#### Scenario 4: Agent Bead Exists But Labels/Status Not Matching Controller Query

The `ListAgentBeads()` query sends both labels in the request body, relying on the daemon's AND semantics. If the daemon implementation changes or if the bead has status `closed` (or another excluded status), it won't appear in the reconciler's desired set.

#### Scenario 5: gastown-next Has No Agent Bead Provisioner Running

**This is the most likely gap for gastown-next.** In gastown-uat:
- A human or existing agent runs `gt sling --k8s` or `gt mayor start --k8s` which creates the bead
- The controller detects the bead and creates the pod

In a fresh namespace (gastown-next) where:
- The controller is deployed but no agent beads exist in Dolt
- Nobody has run `gt mayor start --k8s` or `gt doctor --fix` to seed the initial agent beads
- The mayor pod would need to exist to run `gt sling` for polecats, but the mayor bead doesn't exist yet

**The bootstrap problem**: The controller needs beads to create pods, but beads are created by agents running inside pods. Without an external bootstrapper, the first agent bead must be created manually or via a job.

#### Scenario 6: Direct Pod Creation Outside Controller

If pods are created via `kubectl` or a Helm chart that directly defines agent pods (like the `helm/agent-pod` chart), they would exist without any corresponding beads. The reconciler would see them as orphans and delete them -- UNLESS they lack the `gastown.io/agent` label (which makes the reconciler ignore them, per line 84 of reconciler.go).

---

## 10. Specific Answers to Research Questions

### Q: Does the controller check for an existing agent bead before creating a pod?

The **event path** does not check -- it creates a pod whenever an `AgentSpawn` event fires. The **reconciler path** does check -- it only creates pods for beads that exist in the desired set and don't have corresponding pods.

### Q: Does it CREATE an agent bead if one doesn't exist?

**No.** The controller is read-only with respect to bead creation. It only writes backend metadata (`notes` field) and optionally pod status.

### Q: What labels/fields does it expect on agent beads?

- **Required labels**: `gt:agent` AND `execution_target:k8s` (for reconciler listing)
- **Required status**: NOT `closed` (excluded from list query)
- **Identity resolution**: Prefers `rig:X`, `role:Y`, `agent:Z` labels; falls back to actor field or bead ID parsing
- **Bead ID format**: Must be parseable by `parseAgentBeadID()` (e.g., `hq-mayor`, `gt-gastown-polecat-toast`)

### Q: Where does it set coop_url in bead notes after pod is ready?

In `handleEvent()` for `AgentSpawn` events, immediately after `CreateAgentPod()` succeeds. See `main.go:197-207`. The URL format is `http://{podName}.{namespace}.svc.cluster.local:{coopPort}`. Only set when `spec.CoopSidecar != nil` (NOT when `CoopBuiltin=true`).

### Q: Are there different helm values that affect bead creation?

The controller does not create beads, so helm values don't affect bead creation. However:
- `agentController.enabled` determines whether the controller runs at all
- `agentController.coopBuiltin` affects whether coop_url is written to bead notes
- gastown-ha does not deploy the agent controller

---

## 11. Recommendations

1. **Bootstrap job or init container**: Add a K8s Job or init container that runs `gt doctor --fix` to seed initial agent beads (mayor, deacon, witness, refinery per rig) on first deployment.

2. **Add structured labels**: The `rig:`, `role:`, and `agent:` labels should be added during bead creation (in `CreateAgentBead` or the K8s spawn functions). Currently identity resolution relies on bead ID parsing which is fragile for non-standard ID formats.

3. **CoopBuiltin backend metadata**: When `CoopBuiltin=true`, the controller does not write `coop_url` to bead notes because `spec.CoopSidecar` is nil. This means `ResolveBackend()` can't discover built-in coop agents. Either add a code path for CoopBuiltin metadata writing, or have the agent self-register its coop_url on startup.

4. **Reconciler orphan deletion timing**: The reconciler deletes orphan pods immediately on detection. Consider adding a grace period annotation to prevent race conditions between bead creation and the next reconciler sync.

---

## Key Source Files

| File | Purpose |
|------|---------|
| `controller/cmd/controller/main.go` | Controller entry point, event handler, spec builders |
| `controller/internal/reconciler/reconciler.go` | Periodic bead-vs-pod reconciliation loop |
| `controller/internal/daemonclient/client.go` | HTTP client for daemon List/Update API |
| `controller/internal/beadswatcher/watcher.go` | SSE event stream consumer |
| `controller/internal/beadswatcher/nats_watcher.go` | JetStream event consumer |
| `controller/internal/podmanager/manager.go` | K8s pod CRUD, spec builder, labels |
| `controller/internal/podmanager/defaults.go` | Role-specific pod defaults |
| `controller/internal/statusreporter/reporter.go` | Backend metadata and pod status reporting |
| `controller/internal/config/config.go` | Controller configuration from env/flags |
| `helm/gastown/templates/agent-controller/deployment.yaml` | Helm template for controller Deployment |
| `helm/gastown/values.yaml` | Default values (controller disabled) |
| `helm/gastown/values/gastown-uat.yaml` | UAT values (controller enabled, coopBuiltin) |
| `internal/beads/beads_agent.go` | Agent bead CRUD (CreateAgentBead, etc.) |
| `internal/beads/agent_ids.go` | Agent bead ID generation and parsing |
| `internal/cmd/polecat_spawn.go` | K8s polecat spawn (bead creation + label) |
| `internal/cmd/mayor.go` | K8s mayor spawn (bead creation + label) |
| `internal/sling/spawn.go` | Sling package K8s polecat spawn |
| `internal/registry/registry.go` | Generic session creation (CreateSession) |
| `internal/doctor/agent_beads_check.go` | Doctor check for missing agent beads |
| `docs/design/k8s-reconciliation-loops.md` | Design doc for reconciliation architecture |
