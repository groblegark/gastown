# Gas Town Validation Report

**Date:** 2026-01-18
**Rig:** gastown
**Validator:** furiosa
**Molecule:** gt-wisp-d8gt7 (mol-setup-validate)

## Overall Status: UNHEALTHY

Errors remain after auto-fix attempts. Manual intervention required.

---

## 1. Upstream Sync Status

| Repository | Behind | Ahead | Status |
|------------|--------|-------|--------|
| gastown (steveyegge → groblegark) | 0 | 222 | Up to date |
| beads (steveyegge → groblegark) | 0 | 11 | Up to date |

Both repositories are fully synced with steveyegge upstream.

---

## 2. gt doctor --fix Summary

**Result:** 47 passed, 7 warnings, 2 failed

### Failures (require manual attention)

1. **agent-beads-exist**: 14 agent beads missing
   - Fix attempted but failed due to validation error
   - Error: `invalid agent ID: agent role "witness" requires rig: <prefix>-<rig>-witness`
   - Missing: sw-witness, sw-refinery, sw-crew-ubuntu, gt-gastown-crew-*, loc-local-crew-*, sd-*, spa-*

2. **priming**: 2 AGENTS.md files exceed bootstrap size
   - fhc/AGENTS.md: 142 lines (should be <20)
   - gastown/AGENTS.md: 96 lines (should be <20)

### Warnings

| Issue | Count | Notes |
|-------|-------|-------|
| stale-binary | 1 | Binary built from different commit |
| sqlite3-available | 1 | sqlite3 CLI not installed |
| patrol-not-stuck | 3 | Stuck patrol wisps >1h |
| session-hooks | 22 | Bare 'gt prime' in settings.json |
| runtime-gitignore | 14 | Missing .runtime/ pattern |
| orphan-processes | 22 | Runtime processes outside tmux |
| persistent-role-branches | 2 | Not on main branch |

---

## 3. bd doctor --fix Summary

**Result:** 52 passed, 11 warnings, 2 failed

Note: Interactive fix was not applied (requires manual confirmation).

### Failures (require manual attention)

1. **Sync Divergence**: Database and JSONL out of sync
   - Database import time newer than JSONL
   - Uncommitted .beads/ changes (3 files)

2. **Sync Branch Config**: Currently on sync branch 'master'
   - Cannot create worktree for already-checked-out branch

### Warnings

| Issue | Count | Notes |
|-------|-------|-------|
| DB-JSONL Sync | 1 | Count mismatch: 8290 vs 902 |
| Stale Molecules | 306 | Complete but unclosed |
| Duplicate Issues | 1534 | In 76 groups |
| Orphaned Dependencies | 105 | Orphaned references |
| Large Database | 1 | 6312 closed (threshold: 5000) |
| JSONL Files | 1 | Multiple files found |
| Gitignore | 1 | Missing merge artifact patterns |
| Git Working Tree | 1 | Uncommitted changes |
| Claude Plugin | 1 | Not installed |
| Claude Integration | 1 | Not configured |
| Test Pollution | 1 | Potential test issues |

---

## 4. Recommended Manual Actions

### High Priority

1. **Fix agent bead creation** - The validation error for spa-witness indicates the ID format is incorrect. Investigate and correct the agent bead ID naming convention.

2. **Run bd export** - Export database to JSONL to resolve sync divergence:
   ```bash
   cd /home/ubuntu/pihealth && bd export
   ```

3. **Switch off sync branch** - Before running bd sync:
   ```bash
   cd /home/ubuntu/pihealth && git checkout main
   ```

### Medium Priority

4. **Clean up stale molecules** - 306 complete-but-unclosed:
   ```bash
   bd mol stale
   bd close <id>  # for each
   ```

5. **Install sqlite3** - Required for convoy features:
   ```bash
   sudo apt install sqlite3
   ```

6. **Rebuild gt binary** - Currently stale:
   ```bash
   cd /home/ubuntu/pihealth/submodules/gastown && go build -o ~/go/bin/gt ./cmd/gt
   ```

### Low Priority

7. **Review duplicate issues** - 1534 duplicates in 76 groups
8. **Cleanup large database** - Consider pruning old closed issues
9. **Update session hooks** - 22 hooks need --hook flag or session-start.sh
10. **Add .runtime/ to gitignores** - 14 locations missing pattern

---

## 5. Tools Rebuilt

| Tool | Version | Build Commit |
|------|---------|--------------|
| gt | 0.4.0 | b53ed27b1f79 |
| bd | 0.48.0 | fbfb2cbf4edd |

---

## Summary

The validation found infrastructure in a degraded state with several issues requiring manual attention. The most critical are:

1. Agent bead creation failures due to ID validation errors
2. Beads database/JSONL sync divergence
3. 306 stale molecules needing cleanup

Both upstream repositories are fully synced with steveyegge origins.
