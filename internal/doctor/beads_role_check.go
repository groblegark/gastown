package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/git"
)

// BeadsRoleCheck detects when git config beads.role is not set in rig workspaces.
// Without this setting, the bd CLI shows a warning:
// "beads.role not configured. Run 'bd init' to set."
//
// This warning is confusing and not actionable in Gas Town contexts where
// the role is always "maintainer". This check ensures beads.role=maintainer
// is set in all rig workspaces (mayor, refinery, crew, polecats).
//
// See: gt-vhsnvd
type BeadsRoleCheck struct {
	FixableCheck
}

// NewBeadsRoleCheck creates a new beads role check.
func NewBeadsRoleCheck() *BeadsRoleCheck {
	return &BeadsRoleCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "beads-role",
				CheckDescription: "Check beads.role is set to maintainer (prevents warning)",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if beads.role is set in rig workspaces.
func (c *BeadsRoleCheck) Run(ctx *CheckContext) *CheckResult {
	if ctx.RigName == "" {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rig context, skipping beads.role check",
		}
	}

	rigPath := ctx.RigPath()
	var missingPaths []string

	// Check mayor/rig
	mayorPath := filepath.Join(rigPath, "mayor", "rig")
	if _, err := os.Stat(mayorPath); err == nil {
		if !c.hasBeadsRole(mayorPath) {
			missingPaths = append(missingPaths, "mayor/rig")
		}
	}

	// Check refinery/rig
	refineryPath := filepath.Join(rigPath, "refinery", "rig")
	if _, err := os.Stat(refineryPath); err == nil {
		if !c.hasBeadsRole(refineryPath) {
			missingPaths = append(missingPaths, "refinery/rig")
		}
	}

	// Check crew members
	crewDir := filepath.Join(rigPath, "crew")
	if entries, err := os.ReadDir(crewDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "README.md" {
				crewPath := filepath.Join(crewDir, entry.Name())
				if _, err := os.Stat(filepath.Join(crewPath, ".git")); err == nil {
					if !c.hasBeadsRole(crewPath) {
						missingPaths = append(missingPaths, fmt.Sprintf("crew/%s", entry.Name()))
					}
				}
			}
		}
	}

	// Check polecats
	polecatsDir := filepath.Join(rigPath, "polecats")
	if entries, err := os.ReadDir(polecatsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				polecatPath := filepath.Join(polecatsDir, entry.Name(), ctx.RigName)
				if _, err := os.Stat(filepath.Join(polecatPath, ".git")); err == nil {
					if !c.hasBeadsRole(polecatPath) {
						missingPaths = append(missingPaths, fmt.Sprintf("polecats/%s/%s", entry.Name(), ctx.RigName))
					}
				}
			}
		}
	}

	if len(missingPaths) > 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("beads.role not set in %d workspace(s)", len(missingPaths)),
			Details: append([]string{
				"Without beads.role=maintainer, bd commands show a warning:",
				"  'beads.role not configured. Run 'bd init' to set.'",
				"Affected workspaces:",
			}, formatPaths(missingPaths)...),
			FixHint: "Run 'gt doctor --fix' to set beads.role=maintainer in all workspaces",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: "beads.role is set in all workspaces",
	}
}

// hasBeadsRole checks if beads.role is set in the git config.
func (c *BeadsRoleCheck) hasBeadsRole(repoPath string) bool {
	g := git.NewGit(repoPath)
	role, err := g.ConfigGet("beads.role")
	return err == nil && role != ""
}

// Fix sets beads.role=maintainer in all rig workspaces.
func (c *BeadsRoleCheck) Fix(ctx *CheckContext) error {
	if ctx.RigName == "" {
		return nil
	}

	rigPath := ctx.RigPath()

	// Fix mayor/rig
	mayorPath := filepath.Join(rigPath, "mayor", "rig")
	if _, err := os.Stat(mayorPath); err == nil {
		if err := c.setBeadsRole(mayorPath); err != nil {
			return fmt.Errorf("setting beads.role for mayor: %w", err)
		}
	}

	// Fix refinery/rig
	refineryPath := filepath.Join(rigPath, "refinery", "rig")
	if _, err := os.Stat(refineryPath); err == nil {
		if err := c.setBeadsRole(refineryPath); err != nil {
			return fmt.Errorf("setting beads.role for refinery: %w", err)
		}
	}

	// Fix crew members
	crewDir := filepath.Join(rigPath, "crew")
	if entries, err := os.ReadDir(crewDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "README.md" {
				crewPath := filepath.Join(crewDir, entry.Name())
				if _, err := os.Stat(filepath.Join(crewPath, ".git")); err == nil {
					if err := c.setBeadsRole(crewPath); err != nil {
						return fmt.Errorf("setting beads.role for crew/%s: %w", entry.Name(), err)
					}
				}
			}
		}
	}

	// Fix polecats
	polecatsDir := filepath.Join(rigPath, "polecats")
	if entries, err := os.ReadDir(polecatsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				polecatPath := filepath.Join(polecatsDir, entry.Name(), ctx.RigName)
				if _, err := os.Stat(filepath.Join(polecatPath, ".git")); err == nil {
					if err := c.setBeadsRole(polecatPath); err != nil {
						return fmt.Errorf("setting beads.role for polecats/%s: %w", entry.Name(), err)
					}
				}
			}
		}
	}

	return nil
}

// setBeadsRole sets beads.role=maintainer in the git config.
func (c *BeadsRoleCheck) setBeadsRole(repoPath string) error {
	g := git.NewGit(repoPath)
	return g.SetConfig("beads.role", "maintainer")
}

// formatPaths formats paths with bullet points for display.
func formatPaths(paths []string) []string {
	result := make([]string, len(paths))
	for i, p := range paths {
		result[i] = fmt.Sprintf("  - %s", p)
	}
	return result
}
