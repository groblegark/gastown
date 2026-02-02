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
		// Show scope indicator
		scope := getAdviceScope(advice)
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
// Uses `bd advice list --for=<agent-id>` which auto-subscribes to:
//   - global
//   - agent:<agent-id>
//   - rig:<rig-name>
//   - role:<role-type>
//
// Beads handles all filtering including rig-scoping internally, so we trust
// the returned results without additional filtering.
//
// Note: We still need a two-step fetch because `bd advice list --for --json`
// doesn't include labels (needed for scope display). We fetch IDs from --for,
// then look up full beads from `bd list -t advice --json` which includes labels.
func queryAdviceForAgent(agentID string) ([]AdviceBead, error) {
	// Step 1: Get filtered IDs using subscription model (beads handles all filtering)
	cmd := exec.Command("bd", "advice", "list", "--for="+agentID, "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd advice list --for=%s: %w", agentID, err)
	}

	// Handle empty result
	if len(output) == 0 || strings.TrimSpace(string(output)) == "[]" {
		return nil, nil
	}

	// Parse to get IDs
	var filteredBeads []AdviceBead
	if err := json.Unmarshal(output, &filteredBeads); err != nil {
		return nil, fmt.Errorf("parsing filtered advice: %w", err)
	}

	if len(filteredBeads) == 0 {
		return nil, nil
	}

	// Build ID set for lookup
	wantIDs := make(map[string]bool)
	for _, b := range filteredBeads {
		wantIDs[b.ID] = true
	}

	// Step 2: Fetch all advice with labels (for scope display)
	cmd2 := exec.Command("bd", "list", "-t", "advice", "--json", "--limit", "200")
	output2, err := cmd2.Output()
	if err != nil {
		// Fall back to filtered results without labels (scope will show as "Global")
		return filteredBeads, nil
	}

	var allBeads []AdviceBead
	if err := json.Unmarshal(output2, &allBeads); err != nil {
		return filteredBeads, nil
	}

	// Step 3: Return only matching beads (with labels for display)
	var result []AdviceBead
	for _, b := range allBeads {
		if wantIDs[b.ID] {
			result = append(result, b)
		}
	}

	return result, nil
}

// getAdviceScope returns a human-readable scope indicator for the advice.
func getAdviceScope(bead AdviceBead) string {
	for _, label := range bead.Labels {
		switch {
		case strings.HasPrefix(label, "agent:"):
			return "Agent"
		case strings.HasPrefix(label, "role:"):
			role := strings.TrimPrefix(label, "role:")
			// Capitalize first letter
			if len(role) > 0 {
				return strings.ToUpper(role[:1]) + role[1:]
			}
			return role
		case strings.HasPrefix(label, "rig:"):
			return strings.TrimPrefix(label, "rig:")
		}
	}
	return "Global"
}
