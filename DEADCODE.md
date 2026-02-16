# Dead Code Removal Plan

> Generated 2026-02-16 from git log analysis (250 commits) and full source audit.
>
> **Current**: 139,360 source + 81,088 test = 220,448 total lines
>
> **Target**: Remove ~59–65k source + ~14–25k test lines

The codebase went K8s-only in v0.7.0 (2026-02-13) but only deleted the `internal/tmux/`
_package_ — the tmux _usage_ (447 references in 120 Go files) and the entire local
execution model built on top of it are still compiled and shipped.

---

## Phase 1 — Generated & Staging Code (~25k source)

Zero risk. No behavioral change.

### 1a. Gitignore `gen/` protobuf output (15,482 lines)

All files in `gen/gastown/v1/` are deterministically regenerated from `proto/gastown/v1/`.
Add `gen/` to `.gitignore` and remove tracked files.

**Files:**
- `gen/gastown/v1/*.pb.go` (10 files)
- `gen/gastown/v1/gastownv1connect/*.connect.go` (9 files)

### 1b. Delete `mobile/` directory (9,332 lines)

Research/proto staging area with no app code — no Swift, no Kotlin, no consumers.
Contains duplicated proto definitions and generated Go that mirrors `gen/`.

**Files:** entire `mobile/` directory
- `mobile/gen/` — duplicated generated code (8,213 lines)
- `mobile/proto/` — subset of `proto/` definitions
- `mobile/RESEARCH.md` — Connect-RPC framework comparison
- `mobile/go.mod`, `mobile/go.sum`, `mobile/buf.*`

---

## Phase 2 — Disabled CI & Legacy Scripts (~3.6k)

Zero risk. Already disabled or unused.

### 2a. Delete disabled GitHub Actions workflows (1,190 lines)

All 13 workflows are `.yml.disabled`, fully replaced by `.rwx/` configs.

**Files:**
- `.github/workflows/ci.yml.disabled` (249)
- `.github/workflows/release.yml.disabled` (161)
- `.github/workflows/e2e.yml.disabled` (112)
- `.github/workflows/build.yml.disabled` (93)
- `.github/workflows/helm.yml.disabled` (93)
- `.github/workflows/docker-agent.yml.disabled` (81)
- `.github/workflows/toolchain-image.yml.disabled` (76)
- `.github/workflows/docker.yml.disabled` (72)
- `.github/workflows/docker-controller.yml.disabled` (68)
- `.github/workflows/block-internal-prs.yml.disabled` (51)
- `.github/workflows/windows-ci.yml.disabled` (49)
- `.github/workflows/integration.yml.disabled` (46)
- `.github/workflows/fork-release.yml.disabled` (39)

### 2b. Delete legacy macOS/tmux scripts (~940 lines)

Local-mode developer scripts for a tmux-on-macOS workflow that no longer exists.

**Files:**
- `scripts/macbook-tmux-session.sh` (117) — macOS tmux session setup
- `scripts/macbook-tunnel.sh` (99) — SSH tunnel to EC2
- `scripts/macbook-tunnel-persistent.sh` (45) — persistent variant
- `scripts/gt11-to-macbook.sh` (75) — host-to-host transfer
- `scripts/test-gce-install.sh` (233) — GCE install test
- `scripts/sync-claude-credentials.sh` (82) — local credential sync

### 2c. Delete pre-controller deploy artifacts (~290 lines)

Superseded by Helm chart + controller.

**Files:**
- `deploy/k8s/polecat-pod.yaml` (78) — hand-written pod manifest, replaced by controller
- `deploy/k8s/polecat-ssh-keys.yaml` (26) — SSH keys for SSH backend (deleted)
- `deploy/agent-entrypoint.sh` (112) — old entrypoint, superseded by `deploy/agent/entrypoint.sh`
- `deploy/polecat/entrypoint.sh` (74) — old polecat entrypoint

### 2d. Delete decision shell scripts (~1,162 lines)

Shell implementations of functionality that now lives in Go (`internal/decision/`,
`internal/rpcserver/`).

**Files:**
- `scripts/decision-receiver.sh` (498) — HTTP decision receiver
- `scripts/decision-notify.sh` (370) — decision notification sender
- `scripts/decision-send.sh` (140) — decision submission
- `scripts/migrate-stale-decisions.sh` (154) — one-time migration

---

## Phase 3 — Formally Deprecated Commands (~7.5k source, ~1.6k test)

Low risk. These commands are annotated as deprecated in cobra, hidden, or
explicitly documented as tmux-only.

### 3a. `gt swarm` — fully deprecated (770 source)

Cobra `Deprecated:` field set. All subcommands print migration instructions and redirect
to `gt convoy`. No test file exists.

**Files:**
- `internal/cmd/swarm.go`

### 3b. `gt statusline` — tmux-only hidden command (893 source, 130 test)

`Hidden: true` with comment `// Internal command called by tmux`. Renders agent status
icons in the tmux status bar. No callers exist after tmux package deletion.

**Files:**
- `internal/cmd/statusline.go`
- `internal/cmd/statusline_test.go`
- `internal/statusline/cache.go` (168)
- `internal/statusline/updater.go` (173)
- `internal/statusline/cache_test.go` — if exists

### 3c. `gt costs` — deprecated scraper + one-time migration (1,475 source, 114 test)

Contains a deprecated Claude Code cost scraper (`DEPRECATED: Claude Code no longer displays
cost in a scrapable format`) and `gt costs migrate` for a one-time legacy bead migration
that has already run in all active towns.

**Files:**
- `internal/cmd/costs.go`
- `internal/cmd/costs_test.go`

### 3d. `gt config seed` — deprecated for K8s (920 source, 920 test)

Prints deprecation warning when `BEADS_DOLT_SERVER_MODE=1`. Replaced by `gt bootstrap
--seed-config`. The verify test (540 lines) tests the seed output.

**Files:**
- `internal/cmd/config_seed.go`
- `internal/cmd/config_seed_test.go` (380)
- `internal/cmd/config_seed_verify_test.go` (540)

### 3e. `gt synthesis` — experimental, never shipped (668 source, 95 test)

Multi-agent synthesis workflows. Never graduated from experimental.

**Files:**
- `internal/cmd/synthesis.go`
- `internal/cmd/synthesis_test.go`

### 3f. `gt theme` — tmux-only operation (397 source)

`runThemeApply` is documented as "a tmux-only operation" in its own comments. Tries to
extract rig from tmux session name. No test file.

**Files:**
- `internal/cmd/theme.go`

### 3g. `gt init` — deprecated for K8s (186 source)

Prints deprecation warning in K8s mode. Replaced by `gt bootstrap`.

**Files:**
- `internal/cmd/init.go`

### 3h. `gt feed --window` — tmux-only feature (258 source)

The `--window` flag is documented as "a tmux-only feature (windows are a tmux concept)".
The core feed curator is fine; only the tmux window creation path is dead.

**Files:**
- `internal/cmd/feed.go` — remove `--window` flag and tmux window creation code path

---

## Phase 4 — Local Execution Lifecycle Commands (~12k source, ~4.5k test)

Medium risk. These commands manage the local tmux-based agent lifecycle. In K8s, the
controller creates pods, the daemon coordinates, and coop manages sessions. Verify no K8s
code paths import these before deleting.

### 4a. `gt start` / `gt up` — local process launchers (1,753 source, ~516 test)

Launch deacon, mayor, witnesses, refineries, and crew sessions locally. In K8s, the
controller reconciler handles all of this.

**Files:**
- `internal/cmd/start.go` (1,009)
- `internal/cmd/up.go` (744)
- `internal/cmd/up_test.go` (216)

### 4b. `gt down` — local process stopper (526 source, 24 test)

Stops local services. The `--nuke` flag is documented as "not applicable in K8s" but its
code path still references `tmux-server`. In K8s, use bead `agent_state=stopping` or
controller scale-to-zero.

**Files:**
- `internal/cmd/down.go`
- `internal/cmd/down_test.go`

### 4c. `gt deacon` — local watchdog (1,350 source)

Manages the town-level health orchestrator. In K8s, liveness/readiness probes and the
controller reconciler serve this role.

**Files:**
- `internal/cmd/deacon.go`
- `internal/deacon/` package — assess whether the deacon still runs as an agent inside
  a K8s pod; if so, keep the package but remove the CLI command's local lifecycle code

### 4d. `gt dog` — local cleanup agents (1,023 source, 367 test)

Dogs fix messes polecats leave behind. In K8s, pod lifecycle and the controller handle
cleanup. The `internal/dog/` package may still be used from agent code.

**Files:**
- `internal/cmd/dog.go`
- `internal/cmd/dog_test.go`

### 4e. `gt orphans` — local process orphan detection (835 source)

Finds orphaned git commits and orphaned processes via `git fsck --unreachable` and process
table scanning. Pods can't orphan processes in K8s.

**Files:**
- `internal/cmd/orphans.go`
- `internal/util/orphan.go` — process scanning helpers
- `internal/util/orphan_windows.go` — Windows-specific (K8s runs Linux)

### 4f. `gt gitinit` — local worktree setup (633 source, 149 test)

Initializes git worktrees for polecats. In K8s, `deploy/agent/entrypoint.sh` handles
workspace setup.

**Files:**
- `internal/cmd/gitinit.go`
- `internal/cmd/gitinit_test.go`

### 4g. `gt install` — local workspace installation (679 source)

Full Gas Town installation into a local workspace. Replaced by `gt bootstrap` for K8s.

**Files:**
- `internal/cmd/install.go`

### 4h. `gt session` — local session management (787 source)

Session lifecycle helpers (create, list, kill) wrapping the terminal backend. In K8s, coop
sessions are managed by the daemon.

**Files:**
- `internal/cmd/session.go`

### 4i. `gt handoff` — tmux session compaction (804 source, 222 test)

Gracefully suspends a Claude session and restarts with fresh context. This is a tmux
session operation; coop backends handle context rotation differently.

**Files:**
- `internal/cmd/handoff.go`
- `internal/cmd/handoff_test.go`

### 4j. `gt dialog` — SSH tunnel decision gateway (809 source, 353 test)

Establishes an SSH reverse tunnel from macOS laptop to EC2 for remote human-in-the-loop
decisions. Replaced by the Slack bot decision flow in K8s.

**Files:**
- `internal/cmd/dialog.go`
- `internal/cmd/dialog_test.go`

### 4k. `gt seance` + `gt prime_seance` — local session inspection (1,459 source, 1,449 test)

Agent session inspection and context preparation for local tmux sessions. The K8s prime
flow is different (entrypoint + `gt prime` inside the pod).

**Files:**
- `internal/cmd/seance.go` (733)
- `internal/cmd/prime_seance.go` (726)
- `internal/cmd/seance_test.go` (382)
- `internal/cmd/prime_seance_test.go` (1,067)

### 4l. Cycle commands — local lifecycle rotation (~400 source, ~118 test)

Automated agent restart/rotation cycles. In K8s, the controller handles pod rotation.

**Files:**
- `internal/cmd/cycle.go`
- `internal/cmd/town_cycle.go`
- `internal/cmd/polecat_cycle.go`
- `internal/cmd/crew_cycle.go`
- `internal/cmd/polecat_cycle_test.go` (118)

### 4m. `gt recover` — local recovery (~400 source)

Recovers from local failures. K8s pods simply restart.

**Files:**
- `internal/cmd/recover.go`

---

## Phase 5 — Local-Mode Branches in Surviving Commands (~3.5k source, ~1.8k test)

Higher risk. Surgical removal of local-mode code paths within commands that remain useful
in K8s. Each requires careful branch-by-branch analysis.

### 5a. `internal/cmd/sling.go` + `polecat_spawn.go` — local spawn path

`sling.go` has two dispatch paths: OJ/K8s dispatch and "legacy tmux spawn". The local path
creates worktrees, spawns tmux sessions, and manages pane IDs. Remove the local branch,
keeping the K8s/OJ path.

**Estimated removal:** ~1,500 source, ~800 test

**Files to modify:**
- `internal/cmd/sling.go` (932) — remove legacy local-spawn branch
- `internal/cmd/sling_helpers.go` (753) — remove tmux resolution helpers
- `internal/cmd/polecat_spawn.go` (655) — remove local worktree+clone path
- `internal/cmd/sling_test.go` (1,611) — remove local-path tests
- `internal/sling/spawn.go` (416) — remove `SpawnResult.Pane`, local spawn helpers
- `internal/sling/types.go` — remove `TargetPane` field, tmux comments

### 5b. `internal/cmd/crew_helpers.go` — `attachToTmuxSession`

`attachToTmuxSession()` directly invokes `tmux attach-session` / `switch-client`. Called
from `crew_at.go:264` and `mayor.go:284`. Will fail in K8s pods.

`crewSessionName()` generates tmux-style session names — called from ~12 files. Rename to
`crewSessionID()` and remove the tmux documentation framing.

**Estimated removal:** ~300 source, ~100 test

**Files to modify:**
- `internal/cmd/crew_helpers.go` — delete `attachToTmuxSession`, rename `crewSessionName`
- `internal/cmd/crew_at.go` — replace attach call with coop attach
- `internal/cmd/mayor.go` — replace attach call with coop attach

### 5c. `internal/cmd/crew_lifecycle.go` — local session management

Local tmux session start/stop code paths within crew lifecycle operations.

**Estimated removal:** ~500 source, ~200 test

### 5d. `internal/cmd/status.go` — tmux session polling

`gt status` polls tmux sessions for activity timestamps. Replace with daemon RPC /
coop state queries.

**Estimated removal:** ~400 source, ~200 test

### 5e. `internal/cmd/nudge.go` — tmux send-keys fallback

`gt nudge` has a tmux `send-keys` fallback path alongside the coop API path.

**Estimated removal:** ~300 source, ~200 test

### 5f. `internal/cmd/prime.go` + `prime_output.go` — local-mode paths

Prime has local-mode session detection and tmux-specific output formatting.

**Estimated removal:** ~400 source, ~300 test

### 5g. Refinery deprecated methods

`internal/refinery/manager.go` has deprecated methods: `ProcessMR`, `runTests`,
`pushWithRetry`, `getMergeConfig`, `completeMR`, `notifyWorkerConflict`,
`notifyWorkerMerged`, `RegisterMR`, `Retry`. These are ~30–40% of the file. The
refinery agent (Claude) now handles merge logic autonomously.

**Estimated removal:** ~500 source, ~500 test

---

## Phase 6 — Web Dashboard Rewrite (~3.4k source, ~2k test)

`web/fetcher.go` (1,719 lines) is the dashboard data layer. It has 24 direct tmux
references and acquires all session data via `tmux list-sessions`, `tmux capture-pane`,
and `tmux window_activity`. In K8s, these panels render empty.

### 6a. Replace `web/fetcher.go` with daemon RPC / coop data source

The fetcher interface (`ConvoyFetcher`) is clean — the implementation just needs to
query daemon RPC for agent status and coop for session activity instead of tmux.

**Files:**
- `internal/web/fetcher.go` (1,719) — rewrite
- `internal/web/fetcher_test.go` (417) — rewrite
- `internal/web/setup.go` (967) — local setup wizard, delete or adapt
- `internal/web/templates.go` (375) — remove tmux display references
- `internal/web/handler.go` (323) — remove tmux-dependent routes
- `internal/web/api.go` (1,099) — audit for tmux assumptions
- `internal/web/handler_test.go` (1,088) — rewrite
- `internal/web/browser_e2e_test.go` (397) — rewrite

---

## Phase 7 — Doctor Check Consolidation (~4.1k source, ~2.1k test)

Many of the 60+ doctor checks validate local filesystem layout that doesn't exist in K8s.

### 7a. Delete local-only checks

**Checks to delete:**
- `sparse-checkout` — `sparse_checkout_check.go` (132 source, 654 test)
- `local-dolt` — `local_dolt_check.go` (67) — warns about local dolt conflicting with
  remote; always a no-op when there's no local dolt
- `sqlite3-available` — `sqlite3_check.go` (87) — always returns OK for Dolt backend
- `legacy-gastown` — in `config_check.go` — removes old `.gastown/` dirs; migration
  is complete

### 7b. Remove tmux-specific logic from surviving checks

- `crash-reports` — `crash_report_check.go` (186 source) — remove tmux from
  `relevantProcesses` and the "TMUX CRASHED" diagnostic. Keep claude/node crash detection.
- `boot-health` — `boot_check.go` (124) — remove "tmux session active" / "degraded mode
  (no tmux)" detail strings
- `claude-settings` — `claude_settings_check.go` (814 source) — remove `sessionName`
  field and tmux session cycling from the Fix path

### 7c. Audit remaining checks for local filesystem assumptions

Checks that reference worktrees, clones, bare repos, or local git hooks may need
K8s-aware alternatives:

- `rig_check.go` (1,495 source, 835 test) — `polecat-clones-valid`,
  `mayor-clone-exists`, `bare-repo-refspec`, `bare-repo-integrity`
- `branch_check.go` (554) — `clone-divergence`
- `crew_check.go` (366) — `crew-worktrees`
- `worktree_check.go` (192) — `beads-sync-worktree`
- `precheckout_hook_check.go` (222) — one-time migration cleanser
- `oj_daemon_check.go` (220 source, 191 test) — checks local OJ daemon

---

## Phase 8 — Mail Legacy JSONL + Scattered Cleanup (~1.7k source, ~2k test)

### 8a. Remove mail JSONL legacy mode

`internal/mail/mailbox.go` has 16 `if m.legacy` branches for a JSONL flat-file mode.
Still called from `crew_status.go:114` and `crew_lifecycle.go:291`. Remove after crew
workers migrate to beads-based mailboxes.

**Files to modify:**
- `internal/mail/mailbox.go` — remove `legacy` field, `NewMailbox(path)` constructor,
  all `if m.legacy` branches, JSONL file I/O (~500 lines)
- `internal/mail/mailbox_test.go` — remove legacy mode tests (~1,000 lines)
- `internal/mail/router.go` — remove `addressToSessionIDs` / `addressToSessionID`
  legacy resolution (~200 lines)
- `internal/mail/types.go` — remove `SkipNotify` tmux notification field

### 8b. Remove scattered tmux references (447 references in 120 files)

After phases 1–7, many of the 447 tmux references will already be gone. Sweep the
remaining files for:

- Comments mentioning tmux (variable naming, doc strings)
- `TMUX_PANE` env var reads
- Session name helpers with "tmux" in their name
- Error messages referencing tmux

**Key files with residual tmux references:**
- `internal/beads/beads_agent.go`, `fields.go`
- `internal/boot/boot.go`
- `internal/config/agents.go`, `env.go`, `loader.go`, `roles.go`, `types.go`
- `internal/constants/constants.go`
- `internal/crew/manager.go`
- `internal/daemon/daemon.go`, `lifecycle.go`
- `internal/deacon/manager.go`, `stale_hooks.go`
- `internal/events/events.go`
- `internal/lock/lock.go`
- `internal/mayor/manager.go`
- `internal/polecat/types.go`
- `internal/refinery/manager.go`, `types.go`
- `internal/registry/registry.go`
- `internal/session/identity.go`, `names.go`, `stale.go`, `town.go`
- `internal/terminal/backend.go`, `coop.go`
- `internal/witness/handlers.go`, `manager.go`, `types.go`
- `internal/workspace/find.go`

### 8c. Remove legacy bead format fallbacks

Scattered across the codebase, there are fallbacks for old bead formats:
- `internal/cmd/rig_helpers.go` — falls back to "legacy config beads" (lines 47, 69, 130)
- `internal/cmd/rig_dock.go` — handles "legacy rig" without identity bead
- `internal/cmd/agents.go` — handles "legacy format gt-witness-\<rig\>" (line 157)
- `internal/cmd/mq_list.go` — handles deprecated `issue_type` field

---

## Ledger

| Phase | Description | Source | Test |
|-------|-------------|--------|------|
| 1 | Generated & staging code | 24,800 | 0 |
| 2 | Disabled CI & legacy scripts | 3,582 | 0 |
| 3 | Formally deprecated commands | 7,467 | 1,599 |
| 4 | Local execution lifecycle | 12,043 | 4,498 |
| 5 | Local-mode branches in surviving commands | 3,900 | 2,300 |
| 6 | Web dashboard (tmux-based) | 3,400 | 2,000 |
| 7 | Doctor check consolidation | 4,100 | 2,100 |
| 8 | Mail legacy + scattered cleanup | 1,700 | 2,000 |
| **Total** | | **~61,000** | **~14,500** |

The test estimate of 14.5k is a conservative floor — many `*_test.go` files for deleted
commands could not be confirmed to exist. The actual test reduction likely reaches 18–25k
when accounting for integration tests that exercise local-mode paths across beads, config,
terminal, and other packages.
