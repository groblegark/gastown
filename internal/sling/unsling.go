package sling

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/beads"
)

// Unsling removes work from an agent's hook and resets the bead status.
// This replaces the RPC server's exec.Command("gt", "unhook", ...) pattern
// with direct beads API calls.
func Unsling(opts UnslingOptions) (*UnslingResult, error) {
	if opts.BeadID == "" {
		return nil, fmt.Errorf("bead_id is required")
	}

	townRoot := opts.TownRoot
	client := beads.New(beads.GetTownBeadsPath(townRoot))

	// Get current bead info before unsling
	issue, err := client.Show(opts.BeadID)
	if err != nil {
		return nil, fmt.Errorf("bead not found: %w", err)
	}

	previousAgent := ""
	wasIncomplete := false
	if issue != nil {
		previousAgent = issue.Assignee
		wasIncomplete = issue.Status != "closed"
	}

	// Check if work is complete (warn if not, unless force)
	if wasIncomplete && !opts.Force {
		return nil, fmt.Errorf("hooked work %s is incomplete (%s), use force to unsling anyway",
			opts.BeadID, issue.Title)
	}

	// Clear the hook: set status back to "open" and remove assignee
	if issue.Status == beads.StatusHooked {
		openStatus := "open"
		emptyAssignee := ""
		if err := client.Update(opts.BeadID, beads.UpdateOptions{
			Status:   &openStatus,
			Assignee: &emptyAssignee,
		}); err != nil {
			// Non-fatal for the overall operation, but report it
			return nil, fmt.Errorf("updating bead status: %w", err)
		}
	}

	return &UnslingResult{
		BeadID:        opts.BeadID,
		PreviousAgent: previousAgent,
		WasIncomplete: wasIncomplete,
	}, nil
}
