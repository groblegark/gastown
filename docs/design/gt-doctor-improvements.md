# Design: gt doctor Single-Check Execution and Settings Auto-Fix

**Issue**: gt-0g7zw4.1
**Author**: polecat/furiosa
**Date**: 2026-02-02

## Summary

Research findings and recommendations for improving `gt doctor` capabilities:
1. Single-check execution via `--check=<name>` flag
2. Settings validation improvements (stdin piping already implemented)
3. Template staleness detection
4. Session notification for settings changes

## Current State

### gt doctor Architecture

The doctor framework (`internal/doctor/`) provides:

- **~60 checks** across 7 categories (Core, Infrastructure, Rig, Patrol, Config, Cleanup, Hooks)
- **Check interface**: `Name()`, `Description()`, `Run()`, `Fix()`, `CanFix()`
- **Base implementations**: `BaseCheck` (no auto-fix), `FixableCheck` (supports auto-fix)
- **Existing flags**:
  - `--fix`: Run auto-fix on fixable checks
  - `--rig <name>`: Run rig-specific checks only
  - `--verbose`: Show detailed output
  - `--slow [threshold]`: Highlight slow checks
  - `--restart-sessions`: Restart patrol sessions when fixing settings

### claude_settings_check.go

Location: `internal/doctor/claude_settings_check.go`

**Current validations:**
1. `enabledPlugins` exists
2. `hooks` section exists
3. SessionStart has `gt nudge deacon session-started`
4. Stop has `gt decision turn-check`
5. UserPromptSubmit has `bd decision check --inject`
6. UserPromptSubmit has `gt decision turn-clear`
7. **UserPromptSubmit turn-clear has stdin piping** (gt-te4okj - ALREADY IMPLEMENTED)
8. PostToolUse has `gt inject drain`
9. SessionStart has `gt mail check --inject` (autonomous roles only)

**Auto-fix capability:**
- Deletes stale/wrong-location settings files
- Recreates from embedded templates
- Creates symlinks for mayor settings
- Optionally restarts patrol sessions (with `--restart-sessions`)

## Recommendations

### 1. Single-Check Execution (`--check=<name>`)

**Problem**: Running full `gt doctor` takes 10+ seconds. For CI/hooks, we often need just one specific check.

**Proposed Implementation:**

```go
// cmd/doctor.go
var doctorCheck string

func init() {
    doctorCmd.Flags().StringVar(&doctorCheck, "check", "", "Run a specific check by name")
}

func runDoctor(cmd *cobra.Command, args []string) error {
    // ... existing setup ...

    if doctorCheck != "" {
        // Filter to just the requested check
        for _, check := range d.Checks() {
            if check.Name() == doctorCheck {
                filtered := doctor.NewDoctor()
                filtered.Register(check)
                d = filtered
                break
            }
        }
        if len(d.Checks()) == 0 {
            return fmt.Errorf("unknown check: %s", doctorCheck)
        }
    }

    // ... rest of execution ...
}
```

**Usage:**
```bash
gt doctor --check=claude-settings           # Run one check
gt doctor --check=claude-settings --fix     # Fix one check
gt doctor --check=daemon                    # Quick daemon health check
```

**Implementation beads:**
- `gt-xxxx1`: Add --check flag to gt doctor
- `gt-xxxx2`: Add --list-checks flag to show available check names

### 2. Template Staleness Detection

**Problem**: Settings files can become stale when templates are updated. Currently we only check for missing patterns, not overall freshness.

**Current gap**: No comparison of template version/hash vs installed file.

**Proposed Implementation:**

```go
// internal/claude/settings.go

// TemplateVersion returns a hash of the embedded template content.
// Used by gt doctor to detect stale installed settings.
func TemplateVersion(roleType RoleType) string {
    var templateName string
    switch roleType {
    case Autonomous:
        templateName = "config/settings-autonomous.json"
    default:
        templateName = "config/settings-interactive.json"
    }
    content, _ := configFS.ReadFile(templateName)
    return fmt.Sprintf("%x", sha256.Sum256(content))[:8]
}

// internal/doctor/claude_settings_check.go

func (c *ClaudeSettingsCheck) checkSettingsFreshness(path, agentType string) bool {
    // Read installed settings
    installed, err := os.ReadFile(path)
    if err != nil {
        return false
    }
    installedHash := fmt.Sprintf("%x", sha256.Sum256(installed))[:8]

    // Compare to template
    templateHash := claude.TemplateVersion(claude.RoleTypeFor(agentType))
    return installedHash == templateHash
}
```

**Implementation beads:**
- `gt-xxxx3`: Add template hash comparison to claude_settings_check
- `gt-xxxx4`: Add --force flag to --fix to update even if patterns match

### 3. Settings Refresh Command

**Problem**: When settings change, running agents have the old settings loaded. No way to push updates without restarting sessions.

**Current behavior**: `gt doctor --fix --restart-sessions` kills sessions to pick up new settings.

**Alternative approaches:**

| Approach | Pros | Cons |
|----------|------|------|
| **A: Session restart** (current) | Simple, guaranteed fresh | Loses agent context |
| **B: Hot reload via signal** | No context loss | Claude Code doesn't support SIGHUP |
| **C: Inject notification** | Agent can choose when to restart | Complex, race conditions |
| **D: gt settings refresh** | Explicit command, user controls timing | Requires agent cooperation |

**Recommendation**: Option D - `gt settings refresh`

```bash
# Update settings and inject "settings updated" message
gt settings refresh [--rig <name>]
```

This would:
1. Run claude_settings_check for the workspace/rig
2. If stale, update settings from templates
3. Inject a system message to running sessions: "Settings updated. Run `gt handoff` to pick up new configuration."

**Implementation beads:**
- `gt-xxxx5`: Add gt settings refresh command
- `gt-xxxx6`: Add inject message when settings are updated

### 4. Additional Hook Validations

**Current patterns validated** (lines 386-422 of claude_settings_check.go):
- `gt nudge deacon session-started` in SessionStart
- `gt decision turn-check` in Stop
- `bd decision check --inject` in UserPromptSubmit
- `gt decision turn-clear` in UserPromptSubmit (with stdin piping)
- `gt inject drain` in PostToolUse
- `gt mail check --inject` in SessionStart (autonomous only)

**Potential additional validations:**

1. **PATH setup**: All hooks should have `export PATH="$HOME/.local/bin:$HOME/go/bin:$PATH"`
2. **PreCompact hook**: Should have `gt prime --hook`
3. **PreToolUse guards**: PR workflow guards for `gh pr create`, `git checkout -b`, `git switch -c`
4. **Error handling**: Commands should have `|| true` where failure is acceptable

**Implementation beads:**
- `gt-xxxx7`: Validate PATH export in all hooks
- `gt-xxxx8`: Validate PreCompact has gt prime --hook
- `gt-xxxx9`: Validate PreToolUse PR workflow guards

## Implementation Priority

| Priority | Item | Effort | Impact |
|----------|------|--------|--------|
| P1 | Single-check execution | Low | High - enables fast CI checks |
| P2 | Template staleness detection | Medium | Medium - catches drift |
| P3 | Additional hook validations | Low | Low - already mostly covered |
| P4 | Settings refresh command | Medium | Low - workaround exists |

## Files to Modify

1. `internal/cmd/doctor.go` - Add --check flag
2. `internal/doctor/claude_settings_check.go` - Add template hash comparison
3. `internal/claude/settings.go` - Add TemplateVersion function
4. `internal/cmd/settings.go` (new) - Add settings refresh command

## Testing Strategy

1. **Unit tests**: `internal/doctor/claude_settings_check_test.go` already exists
2. **Integration test**: Verify --check flag runs single check
3. **Manual validation**: Run against real workspace with stale settings
