// Package configbeads provides functions for loading configuration from beads.
// This file adds agent preset config bead loading.
package configbeads

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// LoadAgentPresetsFromBeadsScope queries agent-preset config beads for the given scope
// and returns the parsed presets and role-agent mappings.
// Returns nil, nil, nil if no agent preset beads exist.
func LoadAgentPresetsFromBeadsScope(bd *beads.Beads, townName, rigName string) (map[string]*config.AgentPresetInfo, map[string]string, error) {
	_, fields, err := bd.ListConfigBeadsForScope(
		beads.ConfigCategoryAgentPreset, townName, rigName, "", "")
	if err != nil {
		return nil, nil, fmt.Errorf("listing agent preset beads: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil, nil
	}

	var layers []string
	for _, f := range fields {
		if f.Metadata != "" {
			layers = append(layers, f.Metadata)
		}
	}

	return config.LoadAgentPresetsFromBeads(layers)
}

// LoadAgentRegistry loads agent presets, trying beads first with filesystem fallback.
// Presets from beads are merged into the global agent registry.
// Falls back to loading from townRoot/settings/agents.json and rigPath/settings/agents.json.
func LoadAgentRegistry(townRoot, townName, rigPath, rigName string) error {
	bd := beads.New(townRoot)
	presets, _, err := LoadAgentPresetsFromBeadsScope(bd, townName, rigName)
	if err == nil && len(presets) > 0 {
		config.MergeAgentPresets(presets)
		return nil
	}

	// Fallback to filesystem
	if err := config.LoadAgentRegistry(config.DefaultAgentRegistryPath(townRoot)); err != nil {
		return err
	}
	if rigPath != "" {
		if err := config.LoadRigAgentRegistry(config.RigAgentRegistryPath(rigPath)); err != nil {
			return err
		}
	}
	return nil
}
