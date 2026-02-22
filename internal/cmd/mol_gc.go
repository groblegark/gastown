package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
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

func runMoleculeGC(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("molecule GC removed — witness package deleted; use K8s controller for orphan cleanup")
}
