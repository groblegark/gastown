package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/bdcmd"
	"github.com/steveyegge/gastown/internal/config"
)

// discoverTownRoot finds the Gas Town root directory.
// Checks GT_ROOT env var first, then walks up from cwd.
func discoverTownRoot() string {
	if root := os.Getenv("GT_ROOT"); root != "" {
		return root
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "mayor", "town.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolveRigAgent tries to resolve a rig/name agent by constructing the
// proper bead ID using the rig's configured prefix from rigs.json.
// Returns the resolved coop config, or nil if not found.
func resolveRigAgent(rigName, agentName string) *coopResolvedConfig {
	townRoot := discoverTownRoot()
	if townRoot == "" {
		return nil
	}
	prefix := config.GetRigPrefix(townRoot, rigName)

	// Try as polecat first, then crew.
	// Inline the bead ID construction to avoid importing the beads package
	// (which would create an import cycle: terminal → beads → runtime → terminal).
	candidates := []string{
		agentBeadID(prefix, rigName, "polecat", agentName),
		agentBeadID(prefix, rigName, "crew", agentName),
	}
	for _, beadID := range candidates {
		cfg, err := resolveCoopConfig(beadID)
		if err == nil && cfg != nil {
			return cfg
		}
	}
	return nil
}

// agentBeadID constructs an agent bead ID from prefix, rig, role, and name.
// Mirrors beads.AgentBeadIDWithPrefix logic to avoid import cycles.
// When prefix == rig, the rig component is omitted to avoid duplication
// (e.g., "fhc-polecat-name" instead of "fhc-fhc-polecat-name").
func agentBeadID(prefix, rig, role, name string) string {
	if prefix == rig {
		if name == "" {
			return prefix + "-" + role
		}
		return prefix + "-" + role + "-" + name
	}
	if name == "" {
		return prefix + "-" + rig + "-" + role
	}
	return prefix + "-" + rig + "-" + role + "-" + name
}

// ResolveBackend returns the appropriate Backend for the given agent.
// All agents are Coop-backed in the K8s-only architecture.
//
// The agentID follows the standard format: "rig/polecat" or "rig/crew/name".
// Backend detection checks the agent bead for a "backend" field set by
// the K8s pod manager or Coop sidecar deployment.
//
// Bare names (e.g., "nux") are resolved by searching all agent beads for
// matching polecat or crew suffixes.
func ResolveBackend(agentID string) Backend {
	// Try the given agentID first, then hq-prefixed form for town-level
	// shortnames (mayor -> hq-mayor, deacon -> hq-deacon, etc.).
	candidates := []string{agentID}
	isBare := !strings.Contains(agentID, "/") && !strings.Contains(agentID, "-")
	if isBare {
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

	// For rig/name paths, construct the bead ID using the rig's configured prefix.
	if strings.Contains(agentID, "/") {
		parts := strings.Split(agentID, "/")
		var rigName, agentName string
		switch len(parts) {
		case 2:
			rigName, agentName = parts[0], parts[1]
		case 3:
			rigName, agentName = parts[0], parts[2]
		}
		if rigName != "" && agentName != "" {
			if cfg := resolveRigAgent(rigName, agentName); cfg != nil {
				b := NewCoopBackend(cfg.CoopConfig)
				b.AddSession("claude", cfg.baseURL)
				return b
			}
		}
	}

	// For bare names, search agent beads by name suffix.
	if isBare {
		if beadID := findAgentBeadByName(agentID); beadID != "" {
			coopCfg, err := resolveCoopConfig(beadID)
			if err == nil && coopCfg != nil {
				b := NewCoopBackend(coopCfg.CoopConfig)
				b.AddSession("claude", coopCfg.baseURL)
				return b
			}
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
	baseURL   string
	podName   string
	namespace string
}

// AgentPodInfo contains K8s pod metadata for an agent.
type AgentPodInfo struct {
	PodName   string
	Namespace string
	CoopURL   string
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
		case "pod_name":
			cfg.podName = val
		case "pod_namespace":
			cfg.namespace = val
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

// ResolveAgentPodInfo looks up an agent's K8s pod metadata from its bead notes.
// The address can be a shortname ("mayor"), bead ID ("hq-mayor"), path
// ("gastown/polecats/furiosa"), or bare name ("nux"). Returns pod_name and
// pod_namespace from the bead's notes field, which are written by the
// controller's status reporter.
//
// Bare names are resolved by searching all agent beads for matching
// polecat or crew suffixes (e.g., "nux" → "gt-gastown-polecat-nux").
func ResolveAgentPodInfo(address string) (*AgentPodInfo, error) {
	// Build candidate bead IDs to try.
	candidates := []string{address}
	isBare := false

	// Try parsing as an address to get bead ID format.
	// Import cycle avoidance: we parse the address format inline.
	switch address {
	case "mayor":
		candidates = []string{"hq-mayor"}
	case "deacon":
		candidates = []string{"hq-deacon"}
	case "boot":
		candidates = []string{"hq-boot"}
	default:
		if strings.Contains(address, "/") {
			// Path format — construct bead ID using the rig's configured prefix.
			parts := strings.Split(address, "/")
			townRoot := discoverTownRoot()
			prefix := "gt" // fallback
			if townRoot != "" && len(parts) >= 1 {
				prefix = config.GetRigPrefix(townRoot, parts[0])
			}
			switch len(parts) {
			case 2:
				// rig/name — could be a role (witness) or polecat/crew name
				candidates = append(candidates,
					agentBeadID(prefix, parts[0], parts[1], ""),     // as role singleton
					agentBeadID(prefix, parts[0], "polecat", parts[1]), // as polecat
					agentBeadID(prefix, parts[0], "crew", parts[1]),   // as crew
				)
			case 3:
				// rig/type/name → prefix-rig-type-name
				role := parts[1]
				if role == "polecats" {
					role = "polecat"
				}
				candidates = append(candidates, agentBeadID(prefix, parts[0], role, parts[2]))
			}
		} else if !strings.Contains(address, "-") {
			// Bare name (no slashes, no hyphens) — will search agent beads below.
			isBare = true
		}
	}

	for _, id := range candidates {
		cfg, err := resolveCoopConfig(id)
		if err != nil || cfg == nil {
			continue
		}
		if cfg.podName != "" {
			return &AgentPodInfo{
				PodName:   cfg.podName,
				Namespace: cfg.namespace,
				CoopURL:   cfg.baseURL,
			}, nil
		}
	}

	// For bare names, search all agent beads for matching polecat/crew suffix.
	if isBare {
		if beadID := findAgentBeadByName(address); beadID != "" {
			cfg, err := resolveCoopConfig(beadID)
			if err == nil && cfg != nil && cfg.podName != "" {
				return &AgentPodInfo{
					PodName:   cfg.podName,
					Namespace: cfg.namespace,
					CoopURL:   cfg.baseURL,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no pod metadata found for agent %q", address)
}

// findAgentBeadByName searches all agent beads for one matching a bare name.
// It looks for bead IDs ending in "-polecat-<name>" or "-crew-<name>".
// Returns the matching bead ID, or "" if no match is found.
// If multiple agents match (e.g., same name in different rigs), returns the first.
func findAgentBeadByName(name string) string {
	cmd := bdcmd.Command("list", "--label=gt:agent", "--json")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	var issues []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return ""
	}

	polecatSuffix := "-polecat-" + name
	crewSuffix := "-crew-" + name

	for _, issue := range issues {
		if strings.HasSuffix(issue.ID, polecatSuffix) || strings.HasSuffix(issue.ID, crewSuffix) {
			return issue.ID
		}
	}

	return ""
}
