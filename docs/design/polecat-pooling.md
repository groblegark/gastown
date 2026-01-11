# Polecat Pooling and Capacity Management Design

**Issue**: hq-0esz
**Author**: angharad (polecat)
**Date**: 2026-01-11
**Status**: Draft

## 1. Current Behavior Analysis

### 1.1 How gt sling Works Today

When `gt sling <bead> <rig>` is invoked with a rig target:

1. **`SpawnPolecatForSling`** is called (polecat_spawn.go:47)
2. **`polecatMgr.AllocateName()`** gets a fresh name from the name pool
3. **`polecatMgr.AddWithOptions()`** creates the polecat worktree and agent bead
4. If not `--naked`, a tmux session is created with Claude running
5. The work bead is hooked atomically at spawn time

**Key observations:**
- **Always spawns fresh**: No check for idle polecats to reuse
- **No capacity limits**: No max polecat check before spawning
- **No queue**: If resources are exhausted (e.g., disk space), spawn fails immediately
- **Cleanup is reactive**: Witness can clean up stale polecats, but there's no proactive limit

### 1.2 Polecat State Model

States are derived from beads, not explicit state files:

| Condition | State | Meaning |
|-----------|-------|---------|
| Has hooked work (hook_bead set) | Working | Actively processing a bead |
| No hooked work | Done | Ready for cleanup |
| tmux session exists | Running | Agent process is active |
| No tmux session | Terminated | Session ended (may still have work) |

The "idle" state (running but no work) is not currently tracked.

### 1.3 Name Pool System

Polecats use a themed name pool (manager.go:438):
- Themes: mad-max, minerals, wasteland (50 names each)
- Names are allocated sequentially and released on removal
- Released names can be reallocated (but to new polecats, not reused)

## 2. Prior Art Research

### 2.1 Kubernetes Pod Autoscaling

**Model**: Horizontal Pod Autoscaler (HPA)
```yaml
spec:
  minReplicas: 1
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        targetAverageUtilization: 50
```

**Lessons**:
- Explicit min/max bounds prevent runaway scaling
- Metric-based scaling (CPU, memory, custom metrics)
- Scale-down delay prevents thrashing

### 2.2 Database Connection Pooling (HikariCP, pgBouncer)

**Model**: Pre-warmed connections, lazy creation up to max
```
pool:
  minimum-idle: 5
  maximum-pool-size: 20
  idle-timeout: 600000  # 10 minutes
  connection-timeout: 30000  # fail fast if pool exhausted
```

**Lessons**:
- Idle timeout releases unused resources
- Connection reuse avoids creation overhead
- Pool exhaustion handling: block/timeout vs reject

### 2.3 Process Pools (Python multiprocessing, Go worker pools)

**Model**: Fixed or bounded pool of workers processing a queue
```python
with ProcessPoolExecutor(max_workers=4) as executor:
    futures = [executor.submit(work, item) for item in items]
```

**Lessons**:
- Queue decouples submission from execution
- Workers can be reused across tasks
- Graceful degradation under load

### 2.4 CI/CD Runners (GitHub Actions, GitLab)

**Model**: Self-hosted runners with concurrency limits
```yaml
# GitHub Actions
jobs:
  build:
    runs-on: self-hosted
    concurrency:
      group: my-group
      cancel-in-progress: false
```

**Lessons**:
- Job queuing when runners are busy
- Runner affinity (specific labels/capabilities)
- Automatic cleanup of stale runners

## 3. Design Decisions

### 3.1 Core Question: Spawn Fresh vs Reuse Idle?

**Option A: Always Spawn Fresh (Current)**
- Pros: Simple, clean worktree, no context pollution
- Cons: Resource accumulation, creation overhead, name pool exhaustion

**Option B: Prefer Reuse Idle**
- Pros: Resource efficiency, faster dispatch
- Cons: Potential context pollution, more complex state machine

**Option C: Configurable Strategy (Recommended)**
- Default: Prefer reuse with fresh fallback
- Config option to force fresh for specific workloads

### 3.2 Capacity Model

Three levels of limits:

| Level | Scope | Default | Rationale |
|-------|-------|---------|-----------|
| `max_per_rig` | Single rig | 10 | Prevent one rig from consuming all resources |
| `max_total` | Town-wide | 50 | Hard ceiling for resource protection |
| `max_concurrent` | Active sessions | 20 | Limit simultaneous Claude instances |

### 3.3 Idle Detection

A polecat is **idle** when:
1. tmux session exists (running)
2. Claude process is at prompt (not generating)
3. No work on hook (hook_bead is empty)
4. Last activity > idle_detection_delay (e.g., 30s)

**Implementation**: Witness patrol can mark polecats as idle in their agent bead.

### 3.4 Queue Strategy

When capacity is reached, incoming sling requests can:

| Strategy | Behavior | Use Case |
|----------|----------|----------|
| `reject` | Fail immediately with error | Interactive use, fail-fast |
| `queue` | Add to pending queue, process when capacity frees | Batch workloads |
| `evict_oldest_idle` | Remove oldest idle polecat, spawn fresh | Auto-cleanup |

**Default**: `queue` with configurable timeout.

### 3.5 Cleanup Policy

Polecats should be cleaned up when:
1. **Idle timeout exceeded**: No work for `idle_timeout` duration
2. **Max age exceeded**: Polecat older than `max_age`
3. **Explicit command**: `gt polecat remove` or `gt cleanup`
4. **Capacity pressure**: Soft eviction when approaching limits

## 4. Configuration Schema

### 4.1 Town-Level Configuration (town.yaml)

```yaml
# town.yaml
polecats:
  # Capacity limits
  max_total: 50          # Maximum polecats across all rigs
  max_concurrent: 20     # Maximum simultaneously running sessions

  # Lifecycle
  idle_timeout: 30m      # Remove idle polecats after this duration
  max_age: 24h           # Maximum polecat lifetime (0 = unlimited)

  # Dispatch strategy
  reuse_strategy: prefer_idle  # prefer_idle | always_fresh | always_reuse

  # Queue behavior
  queue_when_full: true      # Queue requests when at capacity (vs reject)
  queue_timeout: 5m          # Max time to wait in queue before failing
  queue_max_size: 100        # Maximum queue depth
```

### 4.2 Rig-Level Configuration (rig.json)

```json
{
  "name": "gastown",
  "path": "/path/to/rig",
  "polecats": {
    "max_per_rig": 10,
    "reuse_strategy": "always_fresh",
    "name_theme": "mad-max"
  }
}
```

### 4.3 Configuration Precedence

1. Command-line flags (`--fresh`, `--reuse`)
2. Rig-level config
3. Town-level config
4. Built-in defaults

## 5. Implementation Plan

### Phase 1: Capacity Limits (Foundation)

**Changes:**
1. Add `PolecatCapacityConfig` struct to `internal/config/types.go`
2. Add capacity checking to `SpawnPolecatForSling`:
   ```go
   func SpawnPolecatForSling(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
       // NEW: Check capacity before spawning
       if err := checkPolecatCapacity(townRoot, rigName); err != nil {
           return nil, err
       }
       // ... existing spawn logic
   }
   ```
3. Implement `countActivePolecats(townRoot)` helper
4. Add `--force-spawn` flag to bypass capacity checks

**Files to modify:**
- `internal/config/types.go` - Add config structs
- `internal/config/loader.go` - Load polecat config
- `internal/cmd/polecat_spawn.go` - Add capacity checks
- `internal/cmd/sling.go` - Respect capacity errors

### Phase 2: Idle Detection & Reuse

**Changes:**
1. Add `PolecatState` with `idle` status to agent beads
2. Witness patrol marks polecats as idle when:
   - Hook is empty
   - Session exists but Claude is at prompt
3. Add `findIdlePolecat(rigName)` to `polecat.Manager`
4. Modify `SpawnPolecatForSling` to check for idle first:
   ```go
   if opts.ReuseStrategy != AlwaysFresh {
       if idle := polecatMgr.FindIdle(); idle != nil {
           return reusePolecatForSling(idle, opts)
       }
   }
   // Fall through to spawn fresh
   ```

**Files to modify:**
- `internal/polecat/types.go` - Add idle state
- `internal/polecat/manager.go` - Add FindIdle()
- `internal/witness/patrol.go` - Mark idle polecats
- `internal/cmd/polecat_spawn.go` - Reuse logic

### Phase 3: Queue System

**Changes:**
1. Add `PolecatQueue` struct for pending sling requests
2. Persist queue to `mayor/polecat-queue.json`
3. Deacon processes queue on patrol:
   - Check capacity
   - If available, dispatch oldest request
   - If not, leave in queue
4. Add `gt queue` command for visibility

**Files to create:**
- `internal/polecat/queue.go` - Queue implementation

**Files to modify:**
- `internal/cmd/sling.go` - Queue when at capacity
- `internal/deacon/patrol.go` - Process queue

### Phase 4: Auto-Cleanup

**Changes:**
1. Witness patrol identifies cleanup candidates:
   - Idle timeout exceeded
   - Max age exceeded
2. Automatic removal with safety checks:
   - Clean git state
   - No uncommitted work
3. Add `gt cleanup` command for manual cleanup

**Files to modify:**
- `internal/witness/patrol.go` - Cleanup logic
- `internal/polecat/manager.go` - Add CleanupIdlePolecats()

## 6. Migration & Compatibility

### 6.1 Defaults

- All new features are opt-in via configuration
- Without config, behavior matches current (spawn fresh, no limits)
- Existing polecats are unaffected

### 6.2 Breaking Changes

None expected. New flags and config are additive.

### 6.3 Deprecations

- `gt polecat spawn` may be deprecated in favor of `gt sling --fresh`

## 7. Open Questions

1. **Queue persistence**: JSON file vs beads database?
2. **Distributed coordination**: How to handle multi-machine scenarios?
3. **Metrics**: Should we emit capacity/queue metrics for monitoring?
4. **Affinity**: Should work prefer polecats that previously worked on same bead?

## 8. Related Issues

- **hq-emog**: gt sling should reuse idle polecats
- **hq-kkkm**: gt cleanup/ps command
