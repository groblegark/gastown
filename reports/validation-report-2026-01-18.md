# Gastown Validation Report
**Date:** 2026-01-18
**Polecat:** furiosa
**Overall Status:** DEGRADED

## Summary

| Component | Passed | Warnings | Failed |
|-----------|--------|----------|--------|
| gt doctor | 47 | 7 | 2 |
| bd doctor | 52 | 11 | 2 |

## Upstream Sync Status

| Repository | Behind Upstream | Ahead of Upstream | Status |
|------------|-----------------|-------------------|--------|
| gastown | 0 | 222 | ✓ Up to date |
| beads | 0 | 11 | ✓ Up to date |

## Tools Rebuilt

- gt version 0.4.0
- bd version 0.48.0

## gt doctor Results

### Failed (2)
1. **agent-beads-exist** - 13 agent beads missing
   - sd-witness, sd-refinery, sd-crew-ubuntu
   - spa-witness, spa-refinery, spa-crew-ubuntu
   - sw-witness, sw-refinery, sw-crew-ubuntu
   - gt-gastown-crew-code_review, gt-gastown-crew-commit_auditor, gt-gastown-crew-upstream_sync, gt-gastown-crew-validator
   - Fix failed: ID validation error for spa-witness

2. **priming** - 2 AGENTS.md files exceed bootstrap size
   - fhc: 142 lines (should be <20)
   - gastown: 96 lines (should be <20)

### Warnings (7)
1. stale-binary - Binary built from older commit
2. sqlite3-available - sqlite3 CLI not installed
3. patrol-not-stuck - 3 stuck patrols (>1h)
4. session-hooks - 22 hook issues (bare 'gt prime' usage)
5. runtime-gitignore - 13 locations missing .runtime pattern
6. orphan-processes - 25 Claude processes outside tmux
7. persistent-role-branches - 2 roles not on main branch

## bd doctor Results

### Failed (2)
1. **Sync Divergence** - DB import time newer than JSONL
2. **Sync Branch Config** - Currently on sync branch 'master'

### Warnings (11)
1. Multiple JSONL files (issues.jsonl, routes.jsonl)
2. DB-JSONL count mismatch (8271 vs 894)
3. Outdated .beads/.gitignore
4. Uncommitted changes present
5. Claude Plugin not installed
6. Claude Integration not configured
7. 306 stale molecules
8. 104 orphaned dependencies
9. 1534 duplicate issues in 76 groups
10. Test pollution (1 issue)
11. Large database (6296 closed issues)

## Recommendations

### High Priority
1. Fix agent-beads-exist: Review ID validation rules for witness beads
2. Fix sync divergence: Run `bd export` or `bd sync` to reconcile
3. Switch off sync branch: `git checkout main`

### Medium Priority
1. Install sqlite3: `apt install sqlite3`
2. Review stuck patrols: fhc, local rigs
3. Clean up 306 stale molecules: `bd mol stale`
4. Address duplicate issues: `bd duplicates`

### Low Priority
1. Rebuild gt/bd to fix stale-binary warning
2. Update session hooks to use `gt prime --hook`
3. Add .runtime/ to crew gitignore files
4. Review orphan Claude processes
