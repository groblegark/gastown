package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/registry"
)

// discoverSessionNames discovers all agent sessions via the SessionRegistry
// and returns their TmuxSession names. This is the K8s-native replacement for
// listing sessions.
//
// townRoot is optional — if empty, the function tries workspace.FindFromCwd().
// Returns an empty slice (not error) if discovery fails, matching the
// best-effort semantics of the session listing callers.
func discoverSessionNames(townRoot string) []string {
	agents := collectAllAgentBeads(townRoot)
	if len(agents) == 0 {
		return nil
	}

	lister := &mapAgentLister{agents: agents}
	reg := registry.New(lister, nil)
	ctx := context.Background()
	sessions, err := reg.DiscoverAll(ctx, registry.DiscoverOpts{})
	if err != nil {
		return nil
	}

	var names []string
	for _, s := range sessions {
		if s.TmuxSession != "" {
			names = append(names, s.TmuxSession)
		}
	}
	return names
}

// discoverSessionNamesForRig discovers agent sessions for a specific rig.
func discoverSessionNamesForRig(townRoot, rigName string) []string {
	agents := collectAllAgentBeads(townRoot)
	if len(agents) == 0 {
		return nil
	}

	lister := &mapAgentLister{agents: agents}
	reg := registry.New(lister, nil)
	ctx := context.Background()
	sessions, err := reg.DiscoverRig(ctx, rigName, registry.DiscoverOpts{})
	if err != nil {
		return nil
	}

	var names []string
	for _, s := range sessions {
		if s.TmuxSession != "" {
			names = append(names, s.TmuxSession)
		}
	}
	return names
}

// collectAllAgentBeads gathers agent beads from town + all rig beads instances.
func collectAllAgentBeads(townRoot string) map[string]*beads.Issue {
	allAgents := make(map[string]*beads.Issue)

	if townRoot == "" {
		return allAgents
	}

	// Town-level agent beads
	townBeadsPath := beads.GetTownBeadsPath(townRoot)
	if agents, err := beads.New(townBeadsPath).ListAgentBeads(); err == nil {
		for id, issue := range agents {
			allAgents[id] = issue
		}
	}

	// Rig-level agent beads
	for _, rigName := range discoverRigs(townRoot) {
		// Standard rig beads path
		rigBeadsPath := filepath.Join(townRoot, rigName, "mayor", "rig")
		if agents, err := beads.New(rigBeadsPath).ListAgentBeads(); err == nil {
			for id, issue := range agents {
				allAgents[id] = issue
			}
		}
		// Also check rig root path
		rigRootPath := filepath.Join(townRoot, rigName)
		if _, err := os.Stat(filepath.Join(rigRootPath, ".beads")); err == nil {
			if agents, err := beads.New(rigRootPath).ListAgentBeads(); err == nil {
				for id, issue := range agents {
					allAgents[id] = issue
				}
			}
		}
	}

	return allAgents
}

// discoverRigs finds all rigs in the town.
func discoverRigs(townRoot string) []string {
	var rigs []string

	// Try rigs.json first
	if rigsConfig, err := loadRigsConfigBeadsFirst(townRoot); err == nil {
		for name := range rigsConfig.Rigs {
			rigs = append(rigs, name)
		}
		return rigs
	}

	// Fallback: scan directory for rig-like directories
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Skip known non-rig directories
		if name == "mayor" || name == "daemon" || name == "deacon" ||
			name == ".git" || name == "docs" || name[0] == '.' {
			continue
		}

		dirPath := filepath.Join(townRoot, name)

		// Check for .beads directory (indicates a rig)
		beadsPath := filepath.Join(dirPath, ".beads")
		if _, err := os.Stat(beadsPath); err == nil {
			rigs = append(rigs, name)
			continue
		}

		// Check for polecats directory (indicates a rig)
		polecatsPath := filepath.Join(dirPath, "polecats")
		if _, err := os.Stat(polecatsPath); err == nil {
			rigs = append(rigs, name)
		}
	}

	return rigs
}
