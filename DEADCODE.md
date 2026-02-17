# Dead Code Removal Plan

> Generated 2026-02-16 from full source audit (quench cloc + import graph analysis).
>
> **Current**: 199,402 code lines (quench total: 123,599 source + 75,803 test)
>
> **Target**: Remove ~32–35k lines across code, docs, and non-code assets
>
> **Previous round**: Phases 1–9 removed ~55k lines (completed 2026-02-13, commit c8c9884b)

The previous DEADCODE plan removed the `internal/tmux/` package, local execution
lifecycle commands, deprecated CLI commands, OJ integration, and generated code.
This plan targets the remaining dead code: orphaned packages, local-only commands
that survived the K8s migration, the web dashboard subsystem, and outdated docs.

---

## Phase 1 — Orphaned Packages (~4,300 lines)

Zero risk. These packages have **zero import statements** referencing them anywhere
in the codebase. They compile but are completely unreachable.

### 1a. `internal/connection/` (1,460 lines)

Old K8s connection abstraction (`Connection` interface, `K8sConnection`,
`MachineRegistry`). Superseded by `terminal.Backend`.

**Files:**
- `internal/connection/connection.go` (149)
- `internal/connection/k8s.go` (341)
- `internal/connection/registry.go` (166)
- `internal/connection/address.go` (138)
- `internal/connection/address_test.go` (450)
- `internal/connection/k8s_test.go` (216)

### 1b. `internal/swarm/` (1,340 lines)

Swarm manager with landing/integration logic. Imports `rig`, `bdcmd`, `polecat`
but nothing imports `swarm`.

**Files:**
- `internal/swarm/manager.go` (287)
- `internal/swarm/landing.go` (235)
- `internal/swarm/types.go` (180)
- `internal/swarm/integration.go` (266)
- `internal/swarm/manager_test.go` (152)
- `internal/swarm/types_test.go` (196)
- `internal/swarm/integration_test.go` (24)

### 1c. `internal/suggest/` (388 lines)

Fuzzy command suggestion utility. Nothing imports it.

**Files:**
- `internal/suggest/suggest.go` (268)
- `internal/suggest/suggest_test.go` (120)

### 1d. `internal/validator/builtin/` (361 lines)

Builtin validator implementations. The parent `internal/validator/` package never
references the `builtin` sub-package — validators are never registered.

**Files:** entire `internal/validator/builtin/` directory

### 1e. `internal/keepalive/` (275 lines)

Keepalive ticker. Imports `internal/workspace` but nothing imports `keepalive`.

**Files:**
- `internal/keepalive/keepalive.go` (137)
- `internal/keepalive/keepalive_test.go` (138)

### 1f. `internal/agent/` (265 lines)

Agent state types. Imports `internal/util` but nothing imports `agent`.

**Files:**
- `internal/agent/state.go` (76)
- `internal/agent/state_test.go` (189)

### 1g. `internal/mq/` (199 lines)

Merge queue ID generation. Nothing imports it.

**Files:**
- `internal/mq/id.go` (56)
- `internal/mq/id_test.go` (143)

---

## Phase 2 — Web Dashboard Cascade (~10,100 lines)

The `gt dashboard` command starts a local HTTP server for convoy tracking. It is
the **sole consumer** of `internal/web/`, which is the **sole consumer** of
`internal/activity/`. Removing the command removes the entire dependency chain.

Not invoked in any K8s entrypoint, formula, or deploy script. The `openBrowser()`
function dispatches to OS-specific commands (`open`, `xdg-open`) — a local-only
pattern that cannot function inside a pod.

### 2a. Delete dashboard command (156 lines)

**Files:**
- `internal/cmd/dashboard.go` (98)
- `internal/cmd/dashboard_test.go` (58)

### 2b. Delete `internal/web/` package (5,836 Go + 2,842 static)

Cascade: only imported by `cmd/dashboard.go`.

**Go files:**
- `internal/web/fetcher.go` (1,401)
- `internal/web/api.go` (1,099)
- `internal/web/handler_test.go` (1,088)
- `internal/web/browser_e2e_test.go` (397)
- `internal/web/api_test.go` (378)
- `internal/web/templates.go` (374)
- `internal/web/fetcher_test.go` (330)
- `internal/web/handler.go` (323)
- `internal/web/templates_test.go` (235)
- `internal/web/commands.go` (211)

**Static assets:**
- `internal/web/static/dashboard.css` (1,876)
- `internal/web/static/dashboard.js` (1,100)
- `internal/web/templates/convoy.html` (866)

### 2c. Delete `internal/activity/` package (315 lines)

Cascade: only imported by `internal/web/`.

**Files:**
- `internal/activity/activity.go` (137)
- `internal/activity/activity_test.go` (178)

---

## Phase 3 — Local-Only Commands & Packages (~2,700 lines)

Commands that operate on local machine state. None function in K8s pods.

### 3a. `gt dolt` — local Dolt server management (1,105 lines)

Starts/stops local `dolt sql-server` processes, manages PID files, reads local
log files. In K8s, Dolt runs as a separate service managed by the daemon.
`internal/doltserver/` is only imported by this file.

**Files:**
- `internal/cmd/dolt.go` (394)
- `internal/doltserver/doltserver.go` (597)
- `internal/doltserver/doltserver_test.go` (114)

### 3b. `gt uninstall` + `internal/wrappers/` (349 lines)

Removes shell hooks, wrapper scripts (gt-codex, gt-opencode), state/config/cache
directories from a local machine. `internal/wrappers/` is only imported by
`uninstall.go`.

**Files:**
- `internal/cmd/uninstall.go` (171)
- `internal/wrappers/wrappers.go` (106)
- `internal/wrappers/scripts/gt-codex` (18)
- `internal/wrappers/scripts/gt-opencode` (18)
- `internal/wrappers/scripts/` directory

### 3c. `gt shell` + `internal/shell/` (534 lines)

Install/remove shell hooks in ~/.zshrc or ~/.bashrc. K8s pods get env vars from
pod specs. `internal/shell/` is used by `uninstall.go`, `disable.go`, `shell.go`,
and `doctor/global_state_check.go`. All four consumers are removed in this plan
(global_state_check.go needs a small edit — see 3d).

**Files:**
- `internal/cmd/shell.go` (99)
- `internal/shell/integration.go` (303)
- `internal/shell/integration_test.go` (132)

**Requires:** Update `internal/doctor/global_state_check.go` to drop its
shell-related check (~10 lines).

### 3d. `gt disable` + `gt enable` (126 lines)

Toggle Gas Town enabled/disabled on a local machine. K8s agents are either
running pods or not.

**Files:**
- `internal/cmd/disable.go` (72)
- `internal/cmd/enable.go` (54)

### 3e. `gt stale` (122 lines)

Compares binary build commit against local source repo HEAD. In K8s, binaries are
baked into container images — there is no source repo to compare against.

**Files:**
- `internal/cmd/stale.go` (122)

### 3f. `gt cleanup` (127 lines)

Finds and kills orphaned Claude processes via OS process inspection. In K8s, each
agent runs in its own pod managed by the container runtime.

Note: `internal/util/orphan.go` is still used by preflight/postflight/polecat and
cannot be cascade-removed here.

**Files:**
- `internal/cmd/cleanup.go` (127)

### 3g. `install_integration_test.go` (340 lines)

Tests the removed `gt install` command (deleted in previous DEADCODE round, but
the test file survived).

**Files:**
- `internal/cmd/install_integration_test.go` (340)

---

## Phase 4 — Dead Non-Code Files (~10,700 lines)

### 4a. Towers of Hanoi formula files (10,221 lines)

Generated durability/stress-test formulas with pre-computed moves. The generator
script `scripts/gen_hanoi.py` can regenerate them on demand.

**Files:**
- `internal/formula/formulas/towers-of-hanoi-10.formula.toml` (6,188)
- `internal/formula/formulas/towers-of-hanoi-9.formula.toml` (3,116)
- `internal/formula/formulas/towers-of-hanoi-7.formula.toml` (812)
- `internal/formula/formulas/towers-of-hanoi.formula.toml` (105)

### 4b. `npm-package/` directory (442 lines)

npm distribution wrapper for local `gt` installation via `npm install`. Irrelevant
in K8s-only mode where the binary is baked into container images.

**Files:** entire `npm-package/` directory
- `npm-package/scripts/postinstall.js` (209)
- `npm-package/bin/gt.js` (54)
- `npm-package/scripts/test.js` (53)
- `npm-package/package.json` (51)
- `npm-package/README.md` (42)
- `npm-package/LICENSE` (21)
- `npm-package/.npmignore` (12)

### 4c. SSH config files (56 lines)

Local dev SSH configs for deprecated gt11 host.

**Files:**
- `config/ssh-config-macbook.conf` (34)
- `config/ssh-config-gt11.conf` (22)

---

## Phase 5 — Outdated Documentation (~2,900 lines)

### 5a. Completed migration plans (657 lines)

**Files:**
- `docs/rwx-ci-migration-plan.md` (657) — RWX CI migration complete

### 5b. Tmux-era design docs (266 lines)

**Files:**
- `docs/tmux-session-namespace-design.md` (266) — tmux fully removed

### 5c. Local-only guides (545 lines)

**Files:**
- `docs/INSTALLING.md` (308) — local install guide, K8s-only now
- `docs/colonization-runbook.md` (237) — local "colonization" setup

### 5d. Old reports and audits (846 lines)

**Files:**
- `docs/reports/hq-8af330.5-bug-categorization.md` (144)
- `docs/reports/hq-8af330.4-failure-patterns.md` (131)
- `docs/reports/setup-validation-report.md` (118)
- `docs/reports/hq-7b9b91-convoy-investigation.md` (117)
- `docs/reports/semantic-id-validation.md` (112)
- `docs/reports/gt-mol-bjz.md` (44)
- `docs/reports/README.md` (9)
- `docs/audit/gt-kube-compatibility.md` (145) — completed audit
- `gastown/reports/audit-hq-mol-1s1.md` (26)

### 5e. Hanoi generator script (103 lines)

Only needed if keeping Towers of Hanoi TOML files (removed in Phase 4a).

**Files:**
- `scripts/gen_hanoi.py` (103)

---

## Phase 6 — Tmux Vestiges in Surviving Code (est. ~2,000–4,000 lines)

Lower confidence — requires surgical edits within active files rather than whole-file
deletion. Each item needs careful verification before removal.

### 6a. `internal/cmd/agents.go` — tmux `display-menu` (~200 lines)

`runAgents()` calls `exec.LookPath("tmux")` and invokes `tmux display-menu` with
tmux color codes (`#[fg=red,bold]`). The `AgentTypeColors` map and
`categorizeSession()` function parse legacy tmux session names. Useless in K8s pods.

### 6b. `internal/cmd/witness.go` — tmux `attach-session` (~20 lines)

`runWitnessAttach` calls `tmux attach-session`. Witnesses run in K8s pods.

### 6c. `internal/config/types.go` — `RuntimeTmuxConfig` struct (~40 lines)

`RuntimeTmuxConfig` with `ProcessNames`, `ReadyPromptPrefix`, `ReadyDelayMs`.
`SleepForReadyDelay()` is called from `polecat/session_manager.go` but is a no-op
when `ReadyDelayMs == 0` (always true in K8s configs).

### 6d. `internal/lock/lock.go` — `getActiveTmuxSessions()` (~30 lines)

Called by `CleanStaleLocks()` for tmux session cross-checking. In K8s mode, there
are no tmux sessions — the check always finds nothing.

### 6e. `internal/util/orphan.go` — tmux stubs (~50 lines)

`getTmuxSessionPIDs()` returns an empty map unconditionally. Dead session-crossing
logic in `FindZombieClaudeProcesses` uses it.

### 6f. `internal/cmd/context.go` — `TMUX_SESSION` env var (~5 lines)

Fallback `os.Getenv("TMUX_SESSION")` that can never trigger in K8s mode.

### 6g. `internal/cmd/crew.go` — `--no-tmux` flag (~10 lines)

`crewNoTmux bool` flag is a tmux vestige. In K8s mode, there's no local clone path.

### 6h. `internal/rpcserver/server.go` — stubs (~50 lines)

`checkTmux()` returns "not applicable (K8s mode)". `ListSessions()` returns empty
list. Both can be removed.

### 6i. `internal/sling/spawn.go` — `ExecutionTargetLocal` (~30 lines)

`ResolveExecutionTarget()` local path and `ExecutionTargetLocal` constant can never
succeed — all callers error if target is not K8s.

### 6j. Doctor checks for local scenarios (est. ~1,000 lines)

Several doctor checks are vestigial for K8s-only mode:
- `global_state_check.go` (111) — checks shell integration (local-only)
- `precheckout_hook_check.go` (222) — one-time migration, long completed
- `worktree_check.go` (192) — local worktree validation
- `lifecycle_check.go` (133) — stale lifecycle messages from local execution
- `stale_binary_check.go` (84) — binary vs source repo, skips gracefully in K8s
- `workspace_check.go` (391) — local workspace filesystem validation

---

## Ledger

| Phase | Description | Code | Non-code | Total |
|-------|-------------|-----:|--------:|------:|
| 1 | Orphaned packages | 4,288 | — | 4,288 |
| 2 | Web dashboard cascade | 6,307 | 3,842 | 10,149 |
| 3 | Local-only commands | 2,703 | — | 2,703 |
| 4 | Dead non-code files | 316 | 10,403 | 10,719 |
| 5 | Outdated documentation | — | 2,912 | 2,912 |
| 6 | Tmux vestiges (est.) | 2,000–4,000 | — | 2,000–4,000 |
| **Total** | | **~15,600–17,600** | **~17,157** | **~32,800–34,800** |

Phases 1–3 are mechanically verifiable (import graph analysis, zero-importer proof).
Phase 4–5 are file deletions with no code dependencies.
Phase 6 requires surgical edits within active files and per-item verification.
