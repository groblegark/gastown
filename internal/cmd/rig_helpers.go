package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/workspace"
)

// getRig finds the town root and retrieves the specified rig.
// This is the common boilerplate extracted from get*Manager functions.
// Returns the town root path and rig instance.
//
// Resolution order:
//  1. Rig identity beads (type=rig, gt:rig label)
//  2. Legacy config beads (type=config, config:rig-registry)
//  3. Filesystem fallback (mayor/rigs.json)
func getRig(rigName string) (string, *rig.Rig, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return "", nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	rigsConfig, err := loadRigsConfigBeadsFirst(townRoot)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(rigName)
	if err != nil {
		return "", nil, fmt.Errorf("rig '%s' not found", rigName)
	}

	return townRoot, r, nil
}

// loadRigsConfigBeadsFirst loads rig registry from rig beads (type=rig)
// first, then falls back to legacy config beads (type=config,
// config:rig-registry), then to the filesystem rigs.json.
func loadRigsConfigBeadsFirst(townRoot string) (*config.RigsConfig, error) {
	bd := beads.New(townRoot)

	// Load town name for bead ID parsing.
	townName := ""
	townCfg, err := config.LoadTownConfig(constants.MayorTownPath(townRoot))
	if err == nil {
		townName = townCfg.Name
	}

	// 1. Try rig identity beads (type=rig, label=gt:rig).
	// These are created by `gt rig register` and the updated SeedRigRegistryBead.
	entries := rigBeadsToEntries(bd, townName)
	if len(entries) > 0 {
		cfg, err := config.LoadRigsConfigWithFallback(entries, townName, townRoot)
		if err == nil && cfg != nil && len(cfg.Rigs) > 0 {
			return cfg, nil
		}
	}

	// 2. Fall back to legacy config beads (type=config, config:rig-registry).
	var configEntries []config.RigBeadEntry
	issues, err := bd.ListConfigBeadsByCategory(beads.ConfigCategoryRigRegistry)
	if err == nil && len(issues) > 0 {
		configEntries = make([]config.RigBeadEntry, 0, len(issues))
		for _, issue := range issues {
			fields := beads.ParseConfigFields(issue.Description)
			configEntries = append(configEntries, config.RigBeadEntry{
				BeadID:   issue.ID,
				Metadata: fields.Metadata,
			})
		}
	}

	return config.LoadRigsConfigWithFallback(configEntries, townName, townRoot)
}

// rigBeadsToEntries converts rig identity beads into RigBeadEntry format
// compatible with LoadRigsConfigFromBeads. Extracts git_url and prefix from
// the rig bead's description (via ParseRigFields) and labels, then marshals
// them into the metadata JSON that LoadRigsConfigFromBeads expects.
func rigBeadsToEntries(bd *beads.Beads, townName string) []config.RigBeadEntry {
	issues, err := bd.ListRigBeads()
	if err != nil || len(issues) == 0 {
		return nil
	}

	entries := make([]config.RigBeadEntry, 0, len(issues))
	for _, issue := range issues {
		fields := beads.ParseRigFields(issue.Description)
		if fields.Repo == "" {
			// Try labels as fallback (gt rig register stores git_url as label).
			for _, label := range issue.Labels {
				if strings.HasPrefix(label, "git_url:") {
					fields.Repo = strings.TrimPrefix(label, "git_url:")
				}
				if strings.HasPrefix(label, "prefix:") {
					fields.Prefix = strings.TrimPrefix(label, "prefix:")
				}
			}
		}
		if fields.Repo == "" {
			continue // Skip beads without a repo URL
		}

		// Build metadata JSON matching what LoadRigsConfigFromBeads expects.
		metadata := map[string]interface{}{
			"git_url":  fields.Repo,
			"added_at": time.Now().Format(time.RFC3339),
		}
		if fields.Prefix != "" {
			metadata["beads"] = map[string]interface{}{
				"prefix": fields.Prefix,
			}
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			continue
		}

		// Synthesize a config-style bead ID so extractRigNameFromBeadID works.
		// The rig bead title is just the rig name (e.g., "coop").
		syntheticID := fmt.Sprintf("hq-cfg-rig-%s-%s", townName, issue.Title)
		entries = append(entries, config.RigBeadEntry{
			BeadID:   syntheticID,
			Metadata: string(metadataJSON),
		})
	}

	return entries
}
