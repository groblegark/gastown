package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/witness"
	"github.com/steveyegge/gastown/internal/workspace"
)

// mol gc flags
var (
	molGCDryRun      bool
	molGCJSON        bool
	molGCGracePeriod string
	molGCScope       string
)

var moleculeGCCmd = &cobra.Command{
	Use:   "gc [rig]",
	Short: "Garbage-collect orphaned polecat molecules",
	Long: `Find and close orphaned mol-polecat-work molecules and their step children.

When a polecat crashes or times out, its mol-polecat-work molecule and all
child step beads remain open forever. This command detects orphaned molecules
(those with no active polecat working on them) and closes them.

A molecule is considered orphaned when:
  - It is still open
  - It has children (step beads)
  - No active polecat agent bead has it hooked
  - It was created more than --grace-period ago (default: 1h)

Use --scope=town to scan town-level beads (hq- prefix) instead of rig-level.
This catches orphaned molecules dispatched by the mayor or cross-rig workflows.

Examples:
  gt mol gc gastown                     # Close orphaned molecules in gastown rig
  gt mol gc gastown --dry-run           # Preview what would be closed
  gt mol gc gastown --scope town        # Scan town-level (hq-) beads
  gt mol gc gastown --grace-period 30m  # Shorter grace period`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeGC,
}

func init() {
	moleculeGCCmd.Flags().BoolVar(&molGCDryRun, "dry-run", false, "Preview what would be closed without closing")
	moleculeGCCmd.Flags().BoolVar(&molGCJSON, "json", false, "Output as JSON")
	moleculeGCCmd.Flags().StringVar(&molGCGracePeriod, "grace-period", "1h", "Grace period before considering a molecule orphaned (e.g., 30m, 2h)")
	moleculeGCCmd.Flags().StringVar(&molGCScope, "scope", "rig", "Scope to scan: 'rig' for rig-level beads, 'town' for town-level (hq-) beads")

	moleculeCmd.AddCommand(moleculeGCCmd)
}

// MolGCResult represents the result of a molecule GC run (for JSON output).
type MolGCResult struct {
	DryRun       bool                `json:"dry_run"`
	RigName      string              `json:"rig_name"`
	Scope        string              `json:"scope"`
	OrphansFound int                 `json:"orphans_found"`
	BeadsClosed  int                 `json:"beads_closed"`
	Orphans      []MolGCOrphanEntry  `json:"orphans,omitempty"`
}

// MolGCOrphanEntry represents a single orphaned molecule in the GC result.
type MolGCOrphanEntry struct {
	MoleculeID string `json:"molecule_id"`
	Title      string `json:"title"`
	CreatedAt  string `json:"created_at"`
	Children   int    `json:"children"`
	Closed     bool   `json:"closed"`
}

func runMoleculeGC(cmd *cobra.Command, args []string) error {
	rigName := args[0]

	// Validate scope
	if molGCScope != "rig" && molGCScope != "town" {
		return fmt.Errorf("invalid scope %q: must be 'rig' or 'town'", molGCScope)
	}

	// Parse grace period
	gracePeriod, err := time.ParseDuration(molGCGracePeriod)
	if err != nil {
		return fmt.Errorf("invalid grace period %q: %w", molGCGracePeriod, err)
	}

	// Find workspace
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}
	townRoot, err := workspace.Find(cwd)
	if err != nil || townRoot == "" {
		return fmt.Errorf("not in a Gas Town workspace")
	}

	// Find orphaned molecules at the specified scope
	orphans, err := witness.FindOrphanedMoleculesWithScope(townRoot, rigName, gracePeriod, molGCScope)
	if err != nil {
		return fmt.Errorf("finding orphaned molecules: %w", err)
	}

	if len(orphans) == 0 {
		if molGCJSON {
			result := MolGCResult{
				DryRun:       molGCDryRun,
				RigName:      rigName,
				Scope:        molGCScope,
				OrphansFound: 0,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		scopeLabel := rigName
		if molGCScope == "town" {
			scopeLabel = rigName + " (town-level)"
		}
		fmt.Printf("%s No orphaned molecules found in %s\n", style.Dim.Render("○"), scopeLabel)
		return nil
	}

	result := MolGCResult{
		DryRun:       molGCDryRun,
		RigName:      rigName,
		Scope:        molGCScope,
		OrphansFound: len(orphans),
		Orphans:      make([]MolGCOrphanEntry, 0, len(orphans)),
	}

	if molGCDryRun {
		if !molGCJSON {
			fmt.Printf("%s [DRY RUN] Found %d orphaned molecule(s) in %s:\n",
				style.Bold.Render("🧹"), len(orphans), rigName)
		}
		for _, mol := range orphans {
			entry := MolGCOrphanEntry{
				MoleculeID: mol.MoleculeID,
				Title:      mol.Title,
				CreatedAt:  mol.CreatedAt,
				Children:   mol.Children,
				Closed:     false,
			}
			result.Orphans = append(result.Orphans, entry)
			if !molGCJSON {
				fmt.Printf("  %s (%d children, created %s)\n",
					mol.MoleculeID, mol.Children, mol.CreatedAt)
			}
		}
	} else {
		totalClosed := 0
		if !molGCJSON {
			fmt.Printf("Closing %d orphaned molecule(s) in %s...\n", len(orphans), rigName)
		}
		for _, mol := range orphans {
			closed, err := witness.CloseOrphanedMoleculeWithScope(townRoot, rigName, mol, molGCScope)
			entry := MolGCOrphanEntry{
				MoleculeID: mol.MoleculeID,
				Title:      mol.Title,
				CreatedAt:  mol.CreatedAt,
				Children:   mol.Children,
				Closed:     err == nil,
			}
			result.Orphans = append(result.Orphans, entry)
			if err != nil {
				if !molGCJSON {
					fmt.Fprintf(os.Stderr, "  Warning: failed to close %s: %v\n", mol.MoleculeID, err)
				}
			} else {
				totalClosed += closed
				if !molGCJSON {
					fmt.Printf("  %s Closed %s (%d beads)\n",
						style.Success.Render("✓"), mol.MoleculeID, closed)
				}
			}
		}
		result.BeadsClosed = totalClosed

		if !molGCJSON {
			fmt.Printf("%s Closed %d orphaned beads across %d molecule(s)\n",
				style.Success.Render("✓"), totalClosed, len(orphans))
		}
	}

	if molGCJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	return nil
}
