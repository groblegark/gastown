package terminal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/bdcmd"
)

// ResolveBackend returns the appropriate Backend for the given agent.
// All agents are Coop-backed in the K8s-only architecture.
//
// The agentID follows the standard format: "rig/polecat" or "rig/crew/name".
// Backend detection checks the agent bead for a "backend" field set by
// the K8s pod manager or Coop sidecar deployment.
func ResolveBackend(agentID string) Backend {
	// Try the given agentID first, then hq-prefixed form for town-level
	// shortnames (mayor -> hq-mayor, deacon -> hq-deacon, etc.).
	candidates := []string{agentID}
	if !strings.Contains(agentID, "/") && !strings.Contains(agentID, "-") {
		candidates = append(candidates, "hq-"+agentID)
	}

	for _, id := range candidates {
		// Check if agent has Coop backend metadata
		coopCfg, err := resolveCoopConfig(id)
		if err == nil && coopCfg != nil {
			b := NewCoopBackend(coopCfg.CoopConfig)
			b.AddSession("claude", coopCfg.baseURL)
			return b
		}
	}

	// Default: return a Coop backend with no sessions configured.
	// Callers should check for errors when invoking methods.
	return NewCoopBackend(CoopConfig{})
}

// resolveCoopConfig checks agent bead metadata for Coop sidecar configuration.
// Returns nil if the agent doesn't use Coop.
func resolveCoopConfig(agentID string) (*coopResolvedConfig, error) {
	notes, err := getAgentNotes(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent bead lookup failed: %w", err)
	}
	return parseCoopConfig(notes)
}

// coopResolvedConfig holds Coop connection info parsed from bead metadata.
type coopResolvedConfig struct {
	CoopConfig
	baseURL string
}

// parseCoopConfig parses Coop config from bd show output.
// Returns nil if the output doesn't indicate a Coop agent.
func parseCoopConfig(output string) (*coopResolvedConfig, error) {
	outStr := strings.TrimSpace(output)
	if outStr == "" || !strings.Contains(outStr, "coop") {
		return nil, nil // Not a Coop agent
	}

	cfg := &coopResolvedConfig{}
	for _, line := range strings.Split(outStr, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "coop_url":
			cfg.baseURL = val
		case "coop_token":
			cfg.Token = val
		}
	}

	if cfg.baseURL == "" {
		return nil, fmt.Errorf("Coop agent missing coop_url in bead metadata")
	}

	return cfg, nil
}

// getAgentNotes fetches the notes field from an agent bead via bd show --json.
// Backend metadata (backend, coop_url, etc.) is stored in the notes
// field as key: value pairs, one per line.
func getAgentNotes(agentID string) (string, error) {
	cmd := bdcmd.Command("show", agentID, "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bd show failed: %w", err)
	}

	// bd show --json returns an array of issues
	var issues []struct {
		Notes string `json:"notes"`
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return "", fmt.Errorf("failed to parse bd show output: %w", err)
	}
	if len(issues) == 0 {
		return "", fmt.Errorf("agent bead %q not found", agentID)
	}
	return issues[0].Notes, nil
}
