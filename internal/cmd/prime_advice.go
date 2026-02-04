package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// AdviceBead represents an advice issue from beads.
type AdviceBead struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Labels      []string `json:"labels,omitempty"`
}

// outputAdviceContext queries and outputs advice applicable to this agent.
// Delegates all subscription matching to beads via `bd advice list --for=<agent-id>`.
// The beads CLI implements the subscription model where agents auto-subscribe to:
//   - global
//   - agent:<their-id>
//   - rig:<their-rig>
//   - role:<their-role>
//
// Beads handles all filtering including rig-scoping (ensures rig:X advice only
// shows to agents in rig X). See docs/design/advice-subscription-model-v2.md.
func outputAdviceContext(ctx RoleInfo) {
	// Build agent identity for subscription matching
	agentID := buildAgentID(ctx)
	if agentID == "" {
		explain(false, "Advice: could not build agent ID")
		return
	}

	// Query advice using beads subscription model
	adviceBeads, err := queryAdviceForAgent(agentID)
	if err != nil {
		// Silently skip if bd isn't available or query fails
		explain(false, fmt.Sprintf("Advice query failed: %v", err))
		return
	}

	if len(adviceBeads) == 0 {
		return
	}

	explain(true, fmt.Sprintf("Advice: %d beads matched subscriptions for %s", len(adviceBeads), agentID))

	// Output advice section
	fmt.Println()
	fmt.Println("## 📝 Agent Advice")
	fmt.Println()
	for _, advice := range adviceBeads {
		// Show scope indicator (pass current role to prefer matching role label)
		scope := getAdviceScope(advice, ctx.Role)
		fmt.Printf("**[%s]** %s\n", scope, advice.Title)
		if advice.Description != "" {
			// Indent description for readability
			lines := strings.Split(advice.Description, "\n")
			for _, line := range lines {
				fmt.Printf("  %s\n", line)
			}
		}
		fmt.Println()
	}
}

// buildAgentID constructs the full agent identifier from role context.
// Format: <rig>/<role-type>/<name> e.g., "gastown/polecats/alpha" or "gastown/crew/decision_notify"
// Town-level roles (Mayor, Deacon) return simple identifiers without rig prefix.
func buildAgentID(ctx RoleInfo) string {
	switch ctx.Role {
	case RoleMayor:
		return "mayor"
	case RoleDeacon:
		return "deacon"
	case RolePolecat:
		if ctx.Rig != "" && ctx.Polecat != "" {
			return fmt.Sprintf("%s/polecats/%s", ctx.Rig, ctx.Polecat)
		}
	case RoleCrew:
		// Note: Crew name is also stored in ctx.Polecat field
		if ctx.Rig != "" && ctx.Polecat != "" {
			return fmt.Sprintf("%s/crew/%s", ctx.Rig, ctx.Polecat)
		}
	case RoleWitness:
		if ctx.Rig != "" {
			return fmt.Sprintf("%s/witness", ctx.Rig)
		}
	case RoleRefinery:
		if ctx.Rig != "" {
			return fmt.Sprintf("%s/refinery", ctx.Rig)
		}
	}

	return ""
}

// queryAdviceForAgent fetches advice beads matching the agent's subscriptions.
// Uses `bd advice list --for=<agent-id> --json` which:
//   - Auto-subscribes to: global, agent:<id>, rig:<rig>, role:<role>
//   - Handles rig-scoping (rig:X advice only matches agents subscribed to rig:X)
//
// Note: bd advice list --json doesn't include labels (beads bug), so we fetch
// labels separately using bd show to get proper scope display.
func queryAdviceForAgent(agentID string) ([]AdviceBead, error) {
	cmd := exec.Command("bd", "advice", "list", "--for="+agentID, "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd advice list --for=%s: %w", agentID, err)
	}

	// Handle empty result
	if len(output) == 0 || strings.TrimSpace(string(output)) == "[]" {
		return nil, nil
	}

	var beads []AdviceBead
	if err := json.Unmarshal(output, &beads); err != nil {
		return nil, fmt.Errorf("parsing advice: %w", err)
	}

	// Fetch labels for all beads in a single batch call
	// This works around bd advice list not including labels in JSON output
	if len(beads) > 0 {
		if err := fetchLabelsForBeads(beads); err != nil {
			// Log but don't fail - labels are nice-to-have for display
			explain(false, fmt.Sprintf("Advice: failed to fetch labels: %v", err))
		}
	}

	return beads, nil
}

// fetchLabelsForBeads fetches labels for all beads using bd show --json.
// Modifies beads in place to add their labels.
func fetchLabelsForBeads(beads []AdviceBead) error {
	if len(beads) == 0 {
		return nil
	}

	// Build args for bd show: bd show <id1> <id2> ... --json
	args := make([]string, 0, len(beads)+2)
	args = append(args, "show")
	for _, bead := range beads {
		args = append(args, bead.ID)
	}
	args = append(args, "--json")

	cmd := exec.Command("bd", args...)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("bd show: %w", err)
	}

	// Parse response - bd show returns array of issues with labels
	var fullBeads []AdviceBead
	if err := json.Unmarshal(output, &fullBeads); err != nil {
		return fmt.Errorf("parsing bd show response: %w", err)
	}

	// Build map of ID -> labels for quick lookup
	labelsMap := make(map[string][]string)
	for _, fb := range fullBeads {
		labelsMap[fb.ID] = fb.Labels
	}

	// Update original beads with their labels
	for i := range beads {
		if labels, ok := labelsMap[beads[i].ID]; ok {
			beads[i].Labels = labels
		}
	}

	return nil
}

// getAdviceScope returns a human-readable scope indicator for the advice.
// When advice has multiple role labels (e.g., role:crew AND role:polecat),
// prefer showing the role that matches the current agent's role.
func getAdviceScope(bead AdviceBead, currentRole Role) string {
	// First pass: check for agent-specific label (highest priority)
	for _, label := range bead.Labels {
		if strings.HasPrefix(label, "agent:") {
			return "Agent"
		}
	}

	// Second pass: look for role labels
	// If one matches the current agent's role, prefer it
	var firstRoleLabel *string // Use pointer to distinguish "not found" from "empty string"
	currentRoleStr := string(currentRole)
	for _, label := range bead.Labels {
		if strings.HasPrefix(label, "role:") {
			role := strings.TrimPrefix(label, "role:")
			// If this role matches the current agent's role, use it immediately
			if strings.EqualFold(role, currentRoleStr) {
				if len(role) > 0 {
					return strings.ToUpper(role[:1]) + role[1:]
				}
				return role
			}
			// Track first role label as fallback
			if firstRoleLabel == nil {
				firstRoleLabel = &role
			}
		}
	}

	// Use first role label if found (none matched current role)
	if firstRoleLabel != nil {
		role := *firstRoleLabel
		if len(role) > 0 {
			return strings.ToUpper(role[:1]) + role[1:]
		}
		return role
	}

	// Third pass: check for rig labels
	for _, label := range bead.Labels {
		if strings.HasPrefix(label, "rig:") {
			return strings.TrimPrefix(label, "rig:")
		}
	}

	return "Global"
}
