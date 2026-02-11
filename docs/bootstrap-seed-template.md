# Bootstrap Seed Template for New Gastown Namespaces

Exported from `gastown-uat` on 2026-02-10.

This document catalogs the essential beads and config that a new namespace
needs to function. The gastown-uat Dolt database contains ~33,000 issues total
but only a small subset is required for bootstrapping.

---

## Overview: Issue Type Distribution

| Type | Count | Seed? |
|------|-------|-------|
| task | 23,331 | No |
| message | 2,704 | No |
| wisp | 2,048 | No |
| gate | 1,780 | No |
| epic | 878 | No |
| merge-request | 688 | No |
| bug | 627 | No |
| convoy | 459 | No |
| feature | 204 | No |
| agent | 132 | Created dynamically |
| advice | 113 | No |
| event | 61 | No |
| formula | 47 | **YES** |
| molecule | 13 | Template molecules only |
| route | 9 | **YES** (active only) |
| config | 6 | **YES** |
| role | 2 | **YES** |
| chore | 8 | No |
| warrant | 1 | No |
| decision | 1 | No |

---

## 1. Config Table (Dolt `beads.config`)

These key-value pairs must be seeded into the Dolt config table. Values marked
with `TEMPLATE` need per-namespace customization.

```sql
-- Core routing and prefixes
INSERT INTO config (`key`, value) VALUES
  ('allowed_prefixes', 'hq,hq-cv'),                          -- TEMPLATE: adjust if different prefix routing
  ('issue_prefix', 'hq'),                                     -- Fixed: HQ prefix for town-level issues
  ('issue-prefix', 'gt'),                                     -- Legacy key, kept for compat
  ('routing_enabled', 'true'),                                -- Enable cross-rig routing
  ('types.custom', 'agent,role,rig,convoy,slot,queue,event,message,molecule,gate,merge-request,config,route');

-- Deploy config
INSERT INTO config (`key`, value) VALUES
  ('deploy.nats_url', 'nats://localhost:4222'),                -- TEMPLATE: adjust if NATS runs externally
  ('deploy.redis_url', 'redis://{{NAMESPACE}}-redis-master:6379'),  -- TEMPLATE: namespace-specific
  ('deploy.slack_channel', '{{SLACK_CHANNEL_ID}}');            -- TEMPLATE: namespace-specific Slack channel

-- Slack config
INSERT INTO config (`key`, value) VALUES
  ('slack.enabled', 'true'),                                   -- TEMPLATE: set false if no Slack
  ('slack.routing_mode', 'rig'),                               -- Per-rig channel routing
  ('slack.dynamic_channels', 'true'),                          -- Auto-create channels
  ('slack.default_channel', '{{SLACK_CHANNEL_ID}}'),           -- TEMPLATE: same as deploy.slack_channel
  ('slack.channel_names', '{}'),                               -- Start empty, populated dynamically
  ('slack.overrides', '{}');                                    -- Start empty

-- Compaction config (defaults)
INSERT INTO config (`key`, value) VALUES
  ('auto_compact_enabled', 'false'),
  ('compaction_enabled', 'false'),
  ('compact_batch_size', '50'),
  ('compact_model', 'claude-3-5-haiku-20241022'),
  ('compact_parallel_workers', '5'),
  ('compact_tier1_days', '30'),
  ('compact_tier1_dep_levels', '2'),
  ('compact_tier2_commits', '100'),
  ('compact_tier2_days', '90'),
  ('compact_tier2_dep_levels', '5');
```

### Template Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `{{NAMESPACE}}` | K8s namespace name | `gastown-prod` |
| `{{SLACK_CHANNEL_ID}}` | Default Slack channel | `C0ABGPN267N` |

---

## 2. Route Beads (type=route)

Routes map bead ID prefixes to rigs. A new namespace needs routes for each
rig it will host. The route title encodes `prefix → rig` and the description
provides context.

### Active Routes (from gastown-uat)

| ID | Title | Description | Notes |
|----|-------|-------------|-------|
| gt-0xebli | `hq- -> .` | Route for prefix hq- to town root | **REQUIRED**: Town-level prefix |
| gt-pxw8kp | `hq-cv- -> .` | Route for prefix hq-cv- to path . | **REQUIRED**: Convoy prefix |
| gt-gjli5p | `gt- -> gastown` | Route for prefix gt- to path . | TEMPLATE: per-rig |
| gt-0dwxyl | `bd- -> beads` | Route for prefix bd- to path . | TEMPLATE: per-rig |
| gt-917qk7 | `gu- -> gastown_ui` | Route for prefix gu- to path . | TEMPLATE: per-rig |
| gt-odan2d | `lo- -> local/mayor/rig` | Route for prefix lo- to path local/mayor/rig | TEMPLATE: per-rig |
| gt-39lnyi | `fhc- -> fics_helm_chart/mayor/rig` | Route for prefix fhc- to path fics_helm_chart/mayor/rig | TEMPLATE: per-rig |

### Route Bead JSON Template

```json
{
  "id": "{{ROUTE_ID}}",
  "title": "{{PREFIX}}- -> {{RIG_NAME}}",
  "description": "Route for prefix {{PREFIX}}- to path .",
  "status": "open",
  "priority": 2,
  "issue_type": "route"
}
```

**Required routes for any namespace:**
- `hq-` -> town root (always needed)
- `hq-cv-` -> town root (convoy prefix, always needed)
- One route per rig in the namespace

**Note:** Route IDs are auto-generated (`gt-XXXXXX`). New namespaces will get
fresh IDs. The `routes` Dolt table exists but is empty -- routes are stored as
beads with `issue_type=route`.

---

## 3. Rig Beads (label: gt:rig)

Each rig needs an identity bead. These are simple beads that define the rig's
prefix and state. The bead ID follows the pattern `{prefix}-rig-{rig_name}`.

### Active Rigs (from gastown-uat)

| ID | Title | Prefix | Repo | Owner |
|----|-------|--------|------|-------|
| bd-rig-beads | beads | bd | (none specified) | oddjobs/crew/runbook_beads |
| lo-rig-local | local | lo | https://gitlab.com/PiHealth/CoreFICS/local.git | oddjobs/crew/runbook_beads |
| gu-rig-gastown_ui | gastown_ui | gu | https://github.com/groblegark/gastown_ui | oddjobs/crew/runbook_beads |
| qn-rig-quench | quench | qn | https://github.com/groblegark/quench.git | mayor |
| fhc-rig-fics_helm_chart | fics_helm_chart | fhc | https://gitlab.com/pihealth/corefics/fics-helm-chart.git | gastown/crew/decisions |

### Rig Bead JSON Template

```json
{
  "id": "{{PREFIX}}-rig-{{RIG_NAME}}",
  "title": "{{RIG_NAME}}",
  "description": "Rig identity bead for {{RIG_NAME}}.\n\nrepo: {{GIT_REPO_URL}}\nprefix: {{PREFIX}}\nstate: active",
  "status": "open",
  "priority": 2,
  "issue_type": "task",
  "labels": ["gt:rig"]
}
```

**Notable:** The gastown rig itself does NOT have a gt:rig bead, nor does
oddjobs. These are "implicit" rigs that exist by convention but lack rig beads.
Only rigs with explicit `{prefix}-rig-{name}` beads are listed. A new namespace
should create rig beads for ALL rigs including the primary one.

---

## 4. Role Beads (type=role, label: gt:role)

Role beads define the behavior and instructions for each agent role type.
These are long-form documents stored as bead descriptions.

### gt-mayor-role -- Mayor Role Definition

```json
{
  "id": "gt-mayor-role",
  "title": "Mayor Role Definition",
  "status": "open",
  "priority": 2,
  "issue_type": "role",
  "work_type": "mutex",
  "labels": ["gt:role"]
}
```

**Description (full text):**

```
You are the Mayor - global coordinator of Gas Town. You sit above all rigs,
coordinating work across the entire workspace.

session_pattern: gt-mayor
work_dir_pattern: {town}
needs_pre_sync: false
start_command: exec claude --dangerously-skip-permissions

default_molecule: mol-mayor-patrol
capabilities:
  - dispatch_work
  - cross_rig_coordination
  - escalation_handling

## Responsibilities

- Work dispatch: Spawn workers for issues, coordinate batch work on epics
- Cross-rig coordination: Route work between rigs when needed
- Escalation handling: Resolve issues Witnesses cannot handle
- Strategic decisions: Architecture, priorities, integration planning

NOT your job: Per-worker cleanup, session killing, nudging workers (Witness
handles that)

## Propulsion Principle

If you find something on your hook, YOU RUN IT.

Your pinned molecule persists across sessions. Hook has work then Run it.
Hook empty then Check mail. Nothing anywhere then Wait for user.

## Key Commands

### Communication
- gt mail inbox - Check your messages
- gt mail read <id> - Read a specific message
- gt mail send <addr> -s "Subject" -m "Message" - Send mail

### Status
- gt status - Overall town status
- gt rigs - List all rigs
- gt polecats <rig> - List polecats in a rig

### Work Management
- bd ready - Issues ready to work (no blockers)
- gt sling <bead> <rig> - Assign work to polecat in rig

## Session End Protocol

- git status, git add, bd sync, git commit, git push
- gt handoff - hand off to fresh session
```

### gt-refinery-role -- Refinery Role Definition

```json
{
  "id": "gt-refinery-role",
  "title": "Refinery Role Definition",
  "status": "closed",
  "priority": 2,
  "issue_type": "role",
  "work_type": "mutex",
  "labels": ["gt:role"]
}
```

**Description (full text):**

```
You are the Refinery - merge queue processor for your rig. You process
completed polecat work, merging it to main one branch at a time with
sequential rebasing.

Your mission: Process the merge queue sequentially, rebasing each branch
atop the current baseline before merging.

session_pattern: gt-{rig}-refinery
work_dir_pattern: {town}/{rig}/refinery/rig
needs_pre_sync: true
start_command: exec claude --dangerously-skip-permissions

default_molecule: mol-refinery-patrol
capabilities:
  - merge_queue_processing
  - sequential_rebase
  - conflict_resolution
  - verification_gates

## The Engineer Mindset

You are Scotty in the engine room. The merge queue is your warp core.

The Beads Promise: Work is never lost. If you discover ANY problem:
1. Fix it now (preferred if quick), OR
2. File a bead and proceed (tracked for cleanup crew)

There is NO third option. Never "disavow" by noting something exists and moving on.

The Scotty Test: Before proceeding past any failure, ask yourself:
"Would Scotty walk past a warp core leak because it existed before his shift?"

## Propulsion Principle

If you find something on your hook, YOU RUN IT.

Your work is defined by the mol-refinery-patrol molecule. Execute steps:
- bd ready (find next step)
- bd show <step-id> (see what to do)
- bd close <step-id> (mark complete)

## Sequential Rebase Protocol

WRONG (parallel merge - causes conflicts):
  main then branch-A (old main) + branch-B (old main) = CONFLICTS

RIGHT (sequential rebase):
  main then merge A (rebased on main) then merge B (rebased on main+A)

After every merge, main moves. Next branch MUST rebase on new baseline.

## Verification Gate

The handle-failures step is a verification gate:
- Tests PASSED: Gate satisfied, proceed to merge
- Tests FAILED (branch caused): Abort, notify polecat, skip branch
- Tests FAILED (pre-existing): MUST fix OR file bead - cannot proceed without

## Commands

### Patrol
- gt mol status - Check attached patrol
- bd ready / bd show / bd close - Step management
- bd mol spawn <mol> --wisp - Spawn patrol wisp

### Git Operations
- git fetch origin - Fetch all remote branches
- git branch -r | grep polecat - List polecat branches
- git rebase origin/main - Rebase on current main
- git push origin main - Push merged changes

### Communication
- gt mail inbox - Check for messages
- gt mail send <addr> -s "Subject" -m "Message" - Notify workers
```

**Note:** No witness role bead, polecat role bead, or deacon role bead exists
in the database. Only mayor and refinery have explicit role beads. The witness,
polecat, and deacon roles are defined implicitly through their formulas and
agent entrypoint code.

---

## 5. Config Beads (type=config)

### hq-cfg-claude-hooks-global -- Claude Hooks Config (ESSENTIAL)

Controls the stop-decision hook behavior for all agents.

```json
{
  "id": "hq-cfg-claude-hooks-global",
  "title": "Claude Hooks: stop decision config",
  "status": "closed",
  "priority": 0,
  "issue_type": "config",
  "labels": ["config:claude-hooks", "scope:global"],
  "metadata": {
    "stop_decision": {
      "default_action": "stop",
      "enabled": true,
      "options": [
        {
          "id": "continue",
          "label": "Continue working on the task",
          "short": "Continue working"
        },
        {
          "id": "stop",
          "label": "Allow Claude to stop",
          "short": "Stop"
        }
      ],
      "poll_interval": "3s",
      "prompt": "You are about to stop. Summarize what you accomplished and what remains.",
      "require_agent_decision": true,
      "require_context": true,
      "timeout": "60m",
      "urgency": "normal"
    }
  },
  "rig": "*"
}
```

### hq-cfg-town-{NAME} -- Town Identity Config

Registers the town identity. Each namespace is a town.

```json
{
  "id": "hq-cfg-town-{{TOWN_NAME}}",
  "title": "identity: town-{{TOWN_NAME}}",
  "description": "identity: town-{{TOWN_NAME}}\n\nrig: {{TOWN_NAME}}\ncategory: identity\ncreated_at: {{TIMESTAMP}}\nmetadata: {\"created_at\":\"{{TIMESTAMP}}\",\"name\":\"{{TOWN_NAME}}\",\"owner\":\"{{OWNER_EMAIL}}\",\"public_name\":\"{{PUBLIC_NAME}}\",\"type\":\"town\",\"version\":2}",
  "status": "open",
  "priority": 2,
  "issue_type": "config",
  "labels": ["config:identity", "gt:config", "town:{{TOWN_NAME}}"]
}
```

**Example (from gastown-uat):**
- `hq-cfg-town-math` -- identity for "math" town
- `hq-cfg-town-testtown` -- identity for "testtown" test town

### hq-cfg-rig-{TOWN}-{RIG} -- Rig Registry Config

Registers a rig within a town. Stores git URL and prefix mapping.

```json
{
  "id": "hq-cfg-rig-{{TOWN}}-{{RIG_NAME}}",
  "title": "rig-registry: rig-{{TOWN}}-{{RIG_NAME}}",
  "description": "rig-registry: rig-{{TOWN}}-{{RIG_NAME}}\n\nrig: {{TOWN}}/{{RIG_NAME}}\ncategory: rig-registry\ncreated_by: mayor\ncreated_at: {{TIMESTAMP}}\nmetadata: {\"added_at\":\"{{TIMESTAMP}}\",\"beads\":{\"prefix\":\"{{PREFIX}}\"},\"git_url\":\"{{GIT_URL}}\"}",
  "status": "open",
  "priority": 2,
  "issue_type": "config",
  "labels": ["config:rig-registry", "gt:config", "rig:{{RIG_NAME}}", "town:{{TOWN}}"]
}
```

### hq-cfg-account-{HANDLE} -- Account Config (Optional)

Registers service accounts. Only needed if using account-based auth.

```json
{
  "id": "hq-cfg-account-{{HANDLE}}",
  "title": "accounts: account-{{HANDLE}}",
  "description": "accounts: account-{{HANDLE}}\n\nrig: *\ncategory: accounts\ncreated_by: {{CREATOR}}\ncreated_at: {{TIMESTAMP}}\nmetadata: {\"config_dir\":\"{{CONFIG_DIR}}\",\"created_at\":\"{{TIMESTAMP}}\",\"description\":\"{{DESCRIPTION}}\",\"email\":\"{{EMAIL}}\",\"handle\":\"{{HANDLE}}\"}",
  "status": "open",
  "priority": 2,
  "issue_type": "config",
  "labels": ["config:accounts", "gt:config", "scope:global"]
}
```

---

## 6. Agent Beads (type=agent, label: gt:agent)

Agent beads are created dynamically when agents are spawned. They should NOT
be pre-seeded except for the mayor, which is the bootstrap agent.

### hq-mayor -- Mayor Agent Bead (Bootstrap)

The only agent bead that should be pre-seeded. Created when the mayor pod starts.

```json
{
  "id": "hq-mayor",
  "title": "hq-mayor",
  "description": "hq-mayor\n\nrole_type: mayor\nrig: null\nagent_state: spawning\nhook_bead: null\ncleanup_status: null\nactive_mr: null\nnotification_level: null\nowned_formulas: null",
  "notes": "backend: coop\ncoop_url: http://gt-town-mayor-hq.{{NAMESPACE}}.svc.cluster.local:8080\npod_name: gt-town-mayor-hq\npod_namespace: {{NAMESPACE}}",
  "status": "pinned",
  "priority": 2,
  "issue_type": "agent",
  "pinned": true,
  "labels": ["execution_target:k8s", "gt:agent"]
}
```

### Agent Bead Template (Polecat)

Polecats are created on-demand. Template for reference:

```json
{
  "id": "{{PREFIX}}-{{RIG}}-polecat-{{NAME}}",
  "title": "{{PREFIX}}-{{RIG}}-polecat-{{NAME}}",
  "description": "{{PREFIX}}-{{RIG}}-polecat-{{NAME}}\n\nrole_type: polecat\nrig: {{RIG}}\nagent_state: spawning\nhook_bead: {{ISSUE_ID}}\ncleanup_status: null\nactive_mr: null\nnotification_level: null\nowned_formulas: null",
  "status": "open",
  "priority": 2,
  "issue_type": "agent",
  "labels": ["execution_target:k8s", "gt:agent"]
}
```

### Agent Types in gastown-uat

| Agent Type | Count | Created By | Notes |
|------------|-------|------------|-------|
| Polecat (worker) | ~80 | gt sling / controller | Ephemeral, auto-created |
| Crew (persistent) | ~30 | gt crew add | Human-managed workspaces |
| Witness | 5 | Per-rig auto | Monitors polecat health |
| Refinery | 5 | Per-rig auto | Processes merge queue |
| Mayor | 1 | Bootstrap | Global coordinator |
| Deacon | 1 | Bootstrap | Daemon patrol agent |

---

## 7. Formula Beads (type=formula, is_template=true)

Formulas define reusable workflow templates. All 47 formulas should be seeded.
They are stored with `is_template=true` and contain step definitions in metadata.

### Full Formula List (from gastown-uat)

| ID | Title |
|----|-------|
| hq-formula-beads-release | beads-release |
| hq-formula-code-review | code-review |
| hq-formula-deploy-e2e-tenant | deploy-e2e-tenant |
| hq-formula-design | design |
| hq-formula-e2e-deflake | e2e-deflake |
| hq-formula-e2e-fix | e2e-fix |
| hq-formula-e2e-test-fix | e2e-test-fix |
| hq-formula-gastown-github-release | gastown-github-release |
| hq-formula-gastown-release | gastown-release |
| hq-formula-mol-boot-triage | mol-boot-triage |
| hq-formula-mol-cicd-fix | mol-cicd-fix |
| hq-formula-mol-convoy-cleanup | mol-convoy-cleanup |
| hq-formula-mol-convoy-feed | mol-convoy-feed |
| hq-formula-mol-deacon-patrol | mol-deacon-patrol |
| hq-formula-mol-dep-propagate | mol-dep-propagate |
| hq-formula-mol-digest-generate | mol-digest-generate |
| hq-formula-mol-formula-iterate | mol-formula-iterate |
| hq-formula-mol-gastown-boot | mol-gastown-boot |
| hq-formula-mol-goblin-scout-patrol | mol-goblin-scout-patrol |
| hq-formula-mol-orphan-scan | mol-orphan-scan |
| hq-formula-mol-patch-build-install | mol-patch-build-install |
| hq-formula-mol-polecat-code-review | mol-polecat-code-review |
| hq-formula-mol-polecat-conflict-resolve | mol-polecat-conflict-resolve |
| hq-formula-mol-polecat-lease | mol-polecat-lease |
| hq-formula-mol-polecat-review-pr | mol-polecat-review-pr |
| hq-formula-mol-polecat-work | mol-polecat-work |
| hq-formula-mol-query-consistency-audit | mol-query-consistency-audit |
| hq-formula-mol-refinery-patrol | mol-refinery-patrol |
| hq-formula-mol-session-gc | mol-session-gc |
| hq-formula-mol-setup-validate | mol-setup-validate |
| hq-formula-mol-shutdown-dance | mol-shutdown-dance |
| hq-formula-mol-sync-workspace | mol-sync-workspace |
| hq-formula-mol-town-shutdown | mol-town-shutdown |
| hq-formula-mol-upstream-pr-intake | mol-upstream-pr-intake |
| hq-formula-mol-witness-patrol | mol-witness-patrol |
| hq-formula-pihealth-commit-audit | pihealth-commit-audit |
| hq-formula-rule-of-five | rule-of-five |
| hq-formula-security-audit | security-audit |
| hq-formula-setup-claude-flow | setup-claude-flow |
| hq-formula-shiny | shiny |
| hq-formula-shiny-enterprise | shiny-enterprise |
| hq-formula-shiny-secure | shiny-secure |
| hq-formula-towers-of-hanoi | towers-of-hanoi |
| hq-formula-towers-of-hanoi-10 | towers-of-hanoi-10 |
| hq-formula-towers-of-hanoi-7 | towers-of-hanoi-7 |
| hq-formula-towers-of-hanoi-9 | towers-of-hanoi-9 |
| hq-formula-upgrade-beads | upgrade-beads |

### Essential Formulas (must seed for basic operation)

The following are required for patrol loops and core workflows:

- `hq-formula-mol-polecat-work` -- Polecat work lifecycle (10 steps)
- `hq-formula-mol-witness-patrol` -- Witness patrol loop (12 steps)
- `hq-formula-mol-refinery-patrol` -- Refinery merge queue processing
- `hq-formula-mol-deacon-patrol` -- Deacon daemon patrol
- `hq-formula-mol-polecat-conflict-resolve` -- Conflict resolution
- `hq-formula-mol-shutdown-dance` -- Graceful shutdown
- `hq-formula-mol-gastown-boot` -- Boot triage
- `hq-formula-mol-session-gc` -- Session garbage collection
- `hq-formula-mol-orphan-scan` -- Orphan polecat detection

### Formula Bead Structure

Formulas store their step definitions in the `metadata` field:

```json
{
  "id": "hq-formula-{{NAME}}",
  "title": "{{NAME}}",
  "description": "{{FORMULA_DESCRIPTION}}",
  "status": "open",
  "priority": 0,
  "issue_type": "formula",
  "is_template": true,
  "labels": ["formula-type:workflow"],
  "metadata": {
    "formula": "{{NAME}}",
    "type": "workflow",
    "version": {{VERSION}},
    "source": "{{LOCAL_PATH_TO_TOML}}",
    "description": "{{FORMULA_DESCRIPTION}}",
    "vars": {
      "issue": {
        "description": "The issue ID assigned to this polecat",
        "required": true
      }
    },
    "steps": [
      {
        "id": "step-id",
        "title": "Step title",
        "description": "Step instructions",
        "needs": ["previous-step-id"]
      }
    ]
  }
}
```

**Note:** Formulas are synced from `.beads/formulas/*.formula.toml` files in the
beads repo. The `source` field in metadata points to the local filesystem path
where the TOML was loaded from. New namespaces should load formulas from the
same TOML source files.

---

## 8. Molecule Beads (type=molecule)

Template molecules are created when agents start patrol loops. Only three
template molecules exist:

| ID | Title | Description |
|----|-------|-------------|
| beads-mol-witness_patrol | Witness Patrol | Per-rig worker monitor patrol loop with progressive nudging |
| beads-mol-refinery_patrol | Refinery Patrol | Merge queue processor patrol loop with verification gates |
| beads-mol-deacon_patrol | Deacon Patrol | Mayor's daemon patrol loop for handling callbacks, health checks, and cleanup |

These are created dynamically by `bd mol spawn` and do not need explicit seeding.
The formula beads provide the templates that molecules are instantiated from.

---

## 9. Dolt Tables

The following tables exist in the `beads` database and are created by the
daemon on first run:

| Table | Seed Data? | Notes |
|-------|-----------|-------|
| issues | YES (essential beads only) | Main bead storage |
| labels | YES (for seeded beads) | Label associations |
| config | YES | Key-value config (see Section 1) |
| metadata | Auto | repo_id, version info |
| routes | No | Empty; routes stored as beads |
| comments | No | Created during operation |
| dependencies | No | Created during operation |
| events | No | Created during operation |
| decision_points | No | Created during operation |
| interactions | No | Created during operation |
| blocked_issues | No | Computed cache |
| blocked_issues_cache | No | Computed cache |
| child_counters | No | Computed cache |
| compaction_snapshots | No | Compaction tracking |
| dirty_issues | No | Sync tracking |
| export_hashes | No | Export tracking |
| federation_peers | No | Multi-cluster federation |
| issue_snapshots | No | History tracking |
| repo_mtimes | No | Sync tracking |

---

## 10. Bootstrap Sequence for New Namespace

### Step 1: Deploy Helm chart
Creates Dolt, daemon, Redis, controller pods. Dolt init creates empty tables.

### Step 2: Seed config table
Insert all rows from Section 1 with namespace-specific values.

### Step 3: Seed essential beads via `bd create` or direct SQL

**Priority order:**

1. **Config beads** (Section 5)
   - `hq-cfg-claude-hooks-global` (stop decision config)
   - `hq-cfg-town-{NAME}` (town identity)
   - `hq-cfg-rig-{TOWN}-{RIG}` for each rig

2. **Role beads** (Section 4)
   - `gt-mayor-role`
   - `gt-refinery-role`

3. **Route beads** (Section 2)
   - `hq-` route (town root)
   - `hq-cv-` route (convoys)
   - One route per rig

4. **Rig beads** (Section 3)
   - One per rig

5. **Formula beads** (Section 7)
   - All 47, or at minimum the 9 essential ones

6. **Mayor agent bead** (Section 6)
   - `hq-mayor` with correct coop_url for the namespace

### Step 4: Start mayor pod
The agent-controller creates the mayor pod, which self-registers and begins
operating.

### Step 5: Verify
```bash
BD_DAEMON_HOST=localhost:PORT BD_DAEMON_TOKEN=TOKEN bd list --type config
BD_DAEMON_HOST=localhost:PORT BD_DAEMON_TOKEN=TOKEN bd list --type route
BD_DAEMON_HOST=localhost:PORT BD_DAEMON_TOKEN=TOKEN bd list --type role
BD_DAEMON_HOST=localhost:PORT BD_DAEMON_TOKEN=TOKEN bd list --label gt:rig
BD_DAEMON_HOST=localhost:PORT BD_DAEMON_TOKEN=TOKEN bd list --type formula --limit 0
```

---

## 11. What NOT to Seed

The following should NOT be copied from gastown-uat:

- **Work beads** (tasks, bugs, epics, features): These are gastown-uat-specific work items
- **Agent beads** (except hq-mayor): Created dynamically by controller
- **Message beads**: Runtime mail between agents
- **Convoy beads**: Work tracking, created by swarm/sling
- **Molecule beads**: Instantiated from formulas at runtime
- **Wisp beads**: Ephemeral sub-tasks
- **Gate beads**: Runtime workflow gates
- **Merge-request beads**: Created by `gt done`
- **Decision/warrant beads**: Runtime artifacts
- **Slack channel config**: Populated dynamically when `slack.dynamic_channels=true`

---

## Appendix: gastown-uat Namespace Summary

- **Total beads:** ~33,000
- **Essential seed beads:** ~60 (2 roles + 7 routes + 5 rigs + 6 config + ~47 formulas)
- **Dolt config rows:** ~28
- **Active rigs:** 5 (beads, local, gastown_ui, quench, fics_helm_chart)
- **Active agents:** ~15 pinned, ~130 total (mostly historical)
- **Towns registered:** gastown9, math, testtown
