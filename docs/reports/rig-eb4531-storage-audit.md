# Storage Layer Audit: SQLite vs Dolt Assumptions

**Date:** 2026-01-19
**Issue:** rig-eb4531
**Polecat:** furiosa
**Related:** rig-2b9509 (readonly error fix)

## Executive Summary

Audited the Gastown codebase for storage backend assumptions. Found **3 critical SQLite-specific assumptions** that bypass the beads CLI abstraction layer, **no explicit test coverage for dual-backend support**, and **significant architectural differences in lock handling** between SQLite and Dolt.

### Key Findings

1. **Direct SQLite CLI access** bypasses backend abstraction (2 files)
2. **No test coverage** for SQLite vs Dolt behavior differences
3. **Lock handling differs fundamentally** between backends
4. **Configuration assumes single backend** per beads database

## Architecture Overview

### Storage Abstraction Model

Gastown uses a layered approach:

```
┌─────────────────────────────────────┐
│  Gastown Go Code                    │
│  (internal/beads, internal/daemon)  │
└────────────┬────────────────────────┘
             │ mostly
             ▼
      ┌──────────────┐
      │  bd CLI      │ ◄─── Abstraction boundary
      └──────┬───────┘
             │
       ┌─────┴──────┐
       │            │
       ▼            ▼
   ┌────────┐  ┌───────┐
   │ SQLite │  │ Dolt  │
   └────────┘  └───────┘
```

**Problem:** Some Gastown components bypass the abstraction and directly invoke `sqlite3` CLI.

## SQLite-Specific Assumptions

### 1. Convoy Watcher (Critical)

**File:** `internal/daemon/convoy_watcher.go`

**Lines:** 156, 170, 196, 202

**Code:**
```go
dbPath := filepath.Join(townBeads, "beads.db")
queryCmd := exec.Command("sqlite3", "-json", dbPath, query)
```

**Impact:**
- Convoy completion detection **fails silently with Dolt backend**
- Queries `dependencies` and `issues` tables directly via `sqlite3` CLI
- No fallback to `bd` CLI or Dolt-compatible query method

**Affected Features:**
- `gt convoy status` - shows 0 tracked issues
- Daemon convoy auto-completion
- TUI convoy panels

**Evidence:**
```bash
$ ls -la /home/ubuntu/gastown9/gastown/mayor/rig/.beads/
drwxr-x---  3 ubuntu ubuntu    4096 Jan 19 19:40 dolt/      # Dolt backend active
-rw-rw-r--  1 ubuntu ubuntu  282624 Jan 19 19:43 beads.db   # SQLite also present
```

When Dolt is active (`metadata.json` has `"backend": "dolt"`), the SQLite `beads.db` file becomes stale. The convoy watcher continues querying stale SQLite data instead of current Dolt data.

### 2. Live Convoy Fetcher (Critical)

**File:** `internal/web/fetcher.go`

**Lines:** 155, 160

**Code:**
```go
dbPath := filepath.Join(f.townBeads, "beads.db")
queryCmd := exec.Command("sqlite3", "-json", dbPath,
    fmt.Sprintf(`SELECT depends_on_id, type FROM dependencies
                 WHERE issue_id = '%s' AND type = 'tracks'`, safeConvoyID))
```

**Impact:**
- Web convoy display shows stale data when Dolt is active
- Manual SQL injection protection (string escaping) instead of parameterized queries
- Same stale-data issue as convoy watcher

**Affected Features:**
- TUI feed convoy panels
- Any web-based convoy tracking features

### 3. Doctor Checks Assume SQLite Files

**File:** `internal/doctor/beads_check.go`, `internal/doctor/sqlite3_check.go`

**Lines:** beads_check.go:48-49, sqlite3_check.go:8-11

**Code:**
```go
issuesDB := filepath.Join(beadsDir, "issues.db")
```

**Impact:**
- `gt doctor` checks assume `issues.db` and `beads.db` exist
- Warns about "missing database" when using Dolt-native mode
- `sqlite3` CLI check treats missing sqlite3 as warning, not error

**Recommendation:** Doctor should detect backend from `metadata.json` and adjust checks accordingly.

## Dolt-Specific Assumptions

### 1. Auto-Import Disabled

**File:** `.beads/config.yaml` (rig beads)

**Setting:**
```yaml
no-auto-import: true
```

**Comment:** "For Dolt-native setup: Dolt is source of truth, JSONL is export-only"

**Impact:**
- SQLite mode expects bi-directional JSONL sync (import/export)
- Dolt mode treats JSONL as export-only
- Configuration change required when switching backends

### 2. Manual Commits Required

**Source:** `docs/dolt-setup-report.md:76-77`

**Quote:**
> Manual commits needed: The Dolt backend doesn't automatically commit changes.
> Use `dolt add . && dolt commit -m "message"` in the Dolt database directory
> to create version history entries.

**Impact:**
- SQLite writes are immediate and persistent
- Dolt writes require manual `dolt commit` to persist in version history
- Agents must change workflow when using Dolt backend

### 3. Daemon Compatibility Issues

**Source:** `docs/dolt-setup-report.md:78`

**Quote:**
> Some operations fail with "database is read only" when the daemon is involved.
> Use `--no-daemon` flag when encountering this issue.

**Impact:**
- `bd` daemon optimizations may not work with Dolt
- Gastown uses `--no-daemon` by default (internal/beads/beads.go:157) which mitigates this
- But explains the readonly errors mentioned in rig-2b9509

## Lock Handling Differences

### SQLite Locking

**Mechanism:** SQLite uses file-based locks on the database file itself

**Behavior:**
- Multiple readers allowed
- Single writer blocks all readers
- Lock is held for duration of transaction
- Lock released immediately when transaction completes

**Evidence:**
- Standard SQLite locking documented in SQLite docs
- No special lock handling in Gastown code

### Dolt Locking

**Mechanism:** Dolt uses Git-style lock files in `.dolt/noms/LOCK`

**Source:** rig-2b9509 description

**Quote:**
> The 'database is read only' error occurs when multiple processes try to access
> the Dolt database simultaneously. Dolt uses lock files (`.dolt/noms/LOCK`) for
> write access, similar to Git.

**Behavior:**
- Lock files prevent concurrent writes
- When lock can't be acquired, `bd` falls back to **readonly mode**
- Readonly mode causes "cannot update manifest: database is read only" error
- Lock persists until process exits (not transaction-scoped)

**Evidence:**
```bash
$ ls -la /home/ubuntu/gastown9/gastown/mayor/rig/.beads/dolt/beads/.dolt/noms/
# LOCK file present when bd process is running
```

**Critical Difference:**
- **SQLite:** Lock acquisition failure → wait/retry
- **Dolt:** Lock acquisition failure → open readonly → error on write attempt

**Missing:** No retry logic in Gastown or `bd` CLI for Dolt lock contention

## Test Coverage Analysis

### Current State

**Finding:** No backend-specific tests found

**Search Results:**
```bash
$ find . -name "*_test.go" -exec grep -l "sqlite\|dolt" {} \;
# No results

$ grep -r "TestSqlite\|TestDolt\|test.*backend" --include="*_test.go" .
# No results
```

**Conclusion:** Tests assume SQLite is the only backend. No dual-backend testing.

### Missing Test Coverage

1. **Backend-agnostic integration tests**
   - Should run against both SQLite and Dolt
   - Current tests only run against SQLite (implicit assumption)

2. **Lock contention tests**
   - No tests for concurrent access behavior
   - No tests for readonly fallback scenario (Dolt)

3. **Convoy watcher tests**
   - No tests verifying convoy completion with Dolt backend
   - Convoy watcher code path never exercised with Dolt

4. **Backend switching tests**
   - No tests for SQLite → Dolt migration
   - No tests for config changes when switching backends

### Test Recommendations

**Create:**
```
internal/beads/backend_test.go
internal/daemon/convoy_watcher_dolt_test.go
internal/web/fetcher_backend_test.go
```

**Test matrix:**
- Create issues (both backends)
- List/show issues (both backends)
- Dependencies (both backends)
- Convoy tracking (both backends)
- Concurrent access (lock contention)
- Readonly fallback (Dolt-specific)

## Recommendations

### Immediate (P0)

1. **Fix convoy watcher SQLite dependency**
   - Replace direct `sqlite3` CLI calls with `bd` CLI commands
   - Example: Replace SQL query with `bd list --json` + filtering
   - File: `internal/daemon/convoy_watcher.go`

2. **Fix live convoy fetcher**
   - Same fix as convoy watcher
   - File: `internal/web/fetcher.go`

3. **Document backend switching**
   - Create `docs/storage-backends.md` with migration guide
   - Document config changes (`no-auto-import`, daemon compatibility)

### Short-term (P1)

4. **Add backend detection to doctor**
   - Read `metadata.json` to detect backend
   - Skip SQLite-specific checks when Dolt is active
   - File: `internal/doctor/beads_check.go`

5. **Add basic dual-backend tests**
   - Start with `internal/beads/backend_test.go`
   - Test create/list/show/update against both backends
   - Use table-driven tests with backend as parameter

### Long-term (P2)

6. **Abstract database queries**
   - Create `internal/beads/query.go` with backend-agnostic query methods
   - Migrate convoy watcher and fetcher to use abstraction
   - Consider using `bd` CLI exclusively (eliminate direct database access)

7. **Add Dolt lock retry logic**
   - Detect readonly errors
   - Retry with backoff when lock contention detected
   - File: `internal/beads/beads.go` (error handling)

8. **Comprehensive backend test suite**
   - Full integration tests for both backends
   - Lock contention simulation
   - Backend switching/migration tests

## Related Issues

- **rig-2b9509** - "database is read only" error from concurrent Dolt access
  - Root cause: Dolt lock contention, no retry logic
  - Related to findings in Lock Handling Differences section

- **rig-297d2b** - gt- prefix routing conflict
  - Shows confusion between SQLite and Dolt databases in routing layer
  - Symptom of dual-backend complexity

## Conclusion

Gastown's storage layer has **leaky abstractions**:
- The `bd` CLI is supposed to abstract backend differences
- But 2 critical code paths bypass this and directly use `sqlite3` CLI
- No tests verify dual-backend compatibility
- Lock handling differences are not documented or handled

**Immediate risk:** Convoy features are broken when Dolt backend is active, but fail silently (showing stale SQLite data instead of errors).

**Long-term risk:** As more features are added, developers may continue adding SQLite-specific code without realizing it breaks Dolt compatibility.

**Recommended approach:** Eliminate direct database access, route all queries through `bd` CLI, add backend-agnostic tests.
