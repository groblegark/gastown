// Package cmd provides CLI commands for the gt tool.
// This file implements the gt config seed command for creating config beads
// from existing embedded template files.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/claude"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	seedDryRun   bool
	seedHooks    bool
	seedMCP      bool
	seedRigs     bool
	seedForce    bool
)

var configSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed config beads from embedded templates",
	Long: `Create config beads from the embedded Claude settings and MCP templates.

This is a one-time migration command that reads the existing embedded config
templates (settings-autonomous.json, settings-interactive.json, mcp.json)
and creates corresponding config beads in the beads database.

By default, seeds all config types. Use flags to seed specific types:

  gt config seed              # Seed all config types
  gt config seed --hooks      # Only Claude hooks
  gt config seed --mcp        # Only MCP config
  gt config seed --rigs       # Only rig registry
  gt config seed --dry-run    # Show what would be created
  gt config seed --force      # Overwrite existing beads`,
	RunE: runConfigSeed,
}

func init() {
	configSeedCmd.Flags().BoolVar(&seedDryRun, "dry-run", false, "Show what would be created without creating")
	configSeedCmd.Flags().BoolVar(&seedHooks, "hooks", false, "Only seed Claude hook config beads")
	configSeedCmd.Flags().BoolVar(&seedMCP, "mcp", false, "Only seed MCP config beads")
	configSeedCmd.Flags().BoolVar(&seedRigs, "rigs", false, "Only seed rig registry config beads")
	configSeedCmd.Flags().BoolVar(&seedForce, "force", false, "Overwrite existing config beads")

	configCmd.AddCommand(configSeedCmd)
}

func runConfigSeed(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	// If no specific flags, seed everything
	seedAll := !seedHooks && !seedMCP && !seedRigs

	var created, skipped, updated int

	if seedAll || seedHooks {
		c, s, u, err := seedHookBeads(bd)
		if err != nil {
			return fmt.Errorf("seeding hook beads: %w", err)
		}
		created += c
		skipped += s
		updated += u
	}

	if seedAll || seedMCP {
		c, s, u, err := seedMCPBeads(bd)
		if err != nil {
			return fmt.Errorf("seeding MCP beads: %w", err)
		}
		created += c
		skipped += s
		updated += u
	}

	if seedAll || seedRigs {
		c, s, u, err := seedRigRegistryBeads(bd, townRoot)
		if err != nil {
			return fmt.Errorf("seeding rig registry beads: %w", err)
		}
		created += c
		skipped += s
		updated += u
	}

	// Summary
	fmt.Println()
	if seedDryRun {
		fmt.Printf("%s Dry run complete: would create %d, would skip %d, would update %d\n",
			style.Info.Render("ℹ"), created, skipped, updated)
	} else {
		fmt.Printf("%s Seed complete: created %d, skipped %d, updated %d\n",
			style.Success.Render("✓"), created, skipped, updated)
	}

	return nil
}

// seedHookBeads creates config beads for Claude hook settings.
// It reads the embedded templates, diffs them to find shared vs role-specific
// settings, and creates:
//   - hq-cfg-hooks-base: shared settings (PreToolUse, PreCompact, PostToolUse, UserPromptSubmit)
//   - hq-cfg-hooks-polecat: polecat-specific overrides (SessionStart with mail check, Stop with --soft)
//   - hq-cfg-hooks-crew: crew-specific overrides (SessionStart without mail check, Stop without --soft)
func seedHookBeads(bd *beads.Beads) (created, skipped, updated int, err error) {
	// Read embedded templates
	autoContent, err := claude.TemplateContent(claude.Autonomous)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reading autonomous template: %w", err)
	}
	interContent, err := claude.TemplateContent(claude.Interactive)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reading interactive template: %w", err)
	}

	// Parse both templates
	var autoSettings map[string]interface{}
	var interSettings map[string]interface{}
	if err := json.Unmarshal(autoContent, &autoSettings); err != nil {
		return 0, 0, 0, fmt.Errorf("parsing autonomous template: %w", err)
	}
	if err := json.Unmarshal(interContent, &interSettings); err != nil {
		return 0, 0, 0, fmt.Errorf("parsing interactive template: %w", err)
	}

	// Extract hooks from both
	autoHooks := extractHooksMap(autoSettings)
	interHooks := extractHooksMap(interSettings)

	// Identify shared vs different hooks
	baseHooks := make(map[string]interface{})
	autoOnlyHooks := make(map[string]interface{})
	interOnlyHooks := make(map[string]interface{})

	// All hook names from both templates
	allHookNames := make(map[string]bool)
	for k := range autoHooks {
		allHookNames[k] = true
	}
	for k := range interHooks {
		allHookNames[k] = true
	}

	for name := range allHookNames {
		autoJSON, _ := json.Marshal(autoHooks[name])
		interJSON, _ := json.Marshal(interHooks[name])

		if string(autoJSON) == string(interJSON) {
			// Shared between both roles
			baseHooks[name] = autoHooks[name]
		} else {
			// Different between roles
			if autoHooks[name] != nil {
				autoOnlyHooks[name] = autoHooks[name]
			}
			if interHooks[name] != nil {
				interOnlyHooks[name] = interHooks[name]
			}
		}
	}

	// Build base settings (non-hook fields + shared hooks)
	baseSettings := make(map[string]interface{})
	for k, v := range autoSettings {
		if k != "hooks" {
			baseSettings[k] = v
		}
	}
	if len(baseHooks) > 0 {
		baseSettings["hooks"] = baseHooks
	}

	// Create base config bead
	c, s, u, err := createOrSkipConfigBead(bd, "hooks-base", beads.ConfigCategoryClaudeHooks,
		"*", "", "", baseSettings, "Base Claude hooks shared by all roles")
	if err != nil {
		return 0, 0, 0, err
	}
	created += c
	skipped += s
	updated += u

	// Create polecat-specific bead (autonomous roles)
	if len(autoOnlyHooks) > 0 {
		polecatOverride := map[string]interface{}{
			"hooks": autoOnlyHooks,
		}
		c, s, u, err = createOrSkipConfigBead(bd, "hooks-polecat", beads.ConfigCategoryClaudeHooks,
			"*", "polecat", "", polecatOverride, "Polecat-specific hook overrides")
		if err != nil {
			return created, skipped, updated, err
		}
		created += c
		skipped += s
		updated += u
	}

	// Create crew-specific bead (interactive roles)
	if len(interOnlyHooks) > 0 {
		crewOverride := map[string]interface{}{
			"hooks": interOnlyHooks,
		}
		c, s, u, err = createOrSkipConfigBead(bd, "hooks-crew", beads.ConfigCategoryClaudeHooks,
			"*", "crew", "", crewOverride, "Crew-specific hook overrides")
		if err != nil {
			return created, skipped, updated, err
		}
		created += c
		skipped += s
		updated += u
	}

	return created, skipped, updated, nil
}

// seedMCPBeads creates config beads for MCP server configuration.
func seedMCPBeads(bd *beads.Beads) (created, skipped, updated int, err error) {
	// Read embedded MCP template using the claude package's embed FS
	mcpContent, err := claude.MCPTemplateContent()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reading MCP template: %w", err)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(mcpContent, &mcpConfig); err != nil {
		return 0, 0, 0, fmt.Errorf("parsing MCP template: %w", err)
	}

	return createOrSkipConfigBead(bd, "mcp-global", beads.ConfigCategoryMCP,
		"*", "", "", mcpConfig, "Global MCP server configuration")
}

// createOrSkipConfigBead creates a config bead or skips if it already exists.
// Returns (created, skipped, updated) counts.
func createOrSkipConfigBead(bd *beads.Beads, slug, category, rig, role, agent string,
	metadata interface{}, description string) (created, skipped, updated int, err error) {

	id := beads.ConfigBeadID(slug)

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("marshaling metadata for %s: %w", slug, err)
	}

	// Check if bead already exists
	existing, _, getErr := bd.GetConfigBead(id)
	if getErr != nil {
		return 0, 0, 0, fmt.Errorf("checking existing bead %s: %w", id, getErr)
	}

	action := "Created"
	if existing != nil {
		if seedForce {
			// Update existing bead
			action = "Updated"
			if seedDryRun {
				fmt.Printf("  %s Would update %s (%s)\n", style.Warning.Render("~"), id, description)
				return 0, 0, 1, nil
			}
			err = bd.UpdateConfigMetadata(id, string(metadataJSON))
			if err != nil {
				return 0, 0, 0, fmt.Errorf("updating %s: %w", id, err)
			}
			fmt.Printf("  %s %s %s (%s)\n", style.Success.Render("✓"), action, id, description)
			return 0, 0, 1, nil
		}
		// Skip existing
		fmt.Printf("  - Skipped %s (already exists)\n", id)
		return 0, 1, 0, nil
	}

	if seedDryRun {
		fmt.Printf("  %s Would create %s (%s)\n", style.Info.Render("+"), id, description)
		return 1, 0, 0, nil
	}

	// Create the bead
	fields := &beads.ConfigFields{
		Rig:      rig,
		Category: category,
		Metadata: string(metadataJSON),
	}

	_, err = bd.CreateConfigBead(slug, fields, role, agent)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("creating %s: %w", id, err)
	}

	fmt.Printf("  %s %s %s (%s)\n", style.Success.Render("✓"), action, id, description)
	return 1, 0, 0, nil
}

// seedRigRegistryBeads creates config beads for each rig in the registry.
// For each rig, it creates:
//   - hq-cfg-rig-<town>-<rigName>: from rigs.json entry (registry metadata)
//   - hq-cfg-rigcfg-<town>-<rigName>: from rig/config.json (rig identity)
func seedRigRegistryBeads(bd *beads.Beads, townRoot string) (created, skipped, updated int, err error) {
	// Load town config to get town name
	townCfg, err := config.LoadTownConfig(filepath.Join(townRoot, "mayor", "town.json"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("loading town config: %w", err)
	}
	townName := townCfg.Name

	// Load rigs registry
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsCfg, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("loading rigs config: %w", err)
	}

	for rigName, entry := range rigsCfg.Rigs {
		rigScope := townName + "/" + rigName

		// 1. Create rig registry bead from rigs.json entry
		slug := "rig-" + townName + "-" + rigName
		metadata := map[string]interface{}{
			"git_url":   entry.GitURL,
			"local_repo": entry.LocalRepo,
			"added_at":  entry.AddedAt,
		}
		if entry.BeadsConfig != nil {
			metadata["beads"] = map[string]interface{}{
				"repo":   entry.BeadsConfig.Repo,
				"prefix": entry.BeadsConfig.Prefix,
			}
		}

		desc := fmt.Sprintf("Rig registry entry for %s", rigName)
		c, s, u, seedErr := createOrSkipConfigBead(bd, slug, beads.ConfigCategoryRigRegistry,
			rigScope, "", "", metadata, desc)
		if seedErr != nil {
			return created, skipped, updated, fmt.Errorf("seeding rig %s: %w", rigName, seedErr)
		}
		created += c
		skipped += s
		updated += u

		// 2. Create per-rig config bead from rig/config.json (if it exists)
		rigConfigPath := filepath.Join(townRoot, rigName, "config.json")
		if _, statErr := os.Stat(rigConfigPath); statErr == nil {
			rigCfg, loadErr := config.LoadRigConfig(rigConfigPath)
			if loadErr != nil {
				fmt.Printf("  %s Skipping rigcfg for %s: %v\n", style.Warning.Render("!"), rigName, loadErr)
				continue
			}

			rigcfgSlug := "rigcfg-" + townName + "-" + rigName
			rigcfgMetadata := map[string]interface{}{
				"type":    rigCfg.Type,
				"name":    rigCfg.Name,
				"git_url": rigCfg.GitURL,
			}
			if rigCfg.LocalRepo != "" {
				rigcfgMetadata["local_repo"] = rigCfg.LocalRepo
			}
			if rigCfg.Beads != nil {
				rigcfgMetadata["beads"] = map[string]interface{}{
					"prefix": rigCfg.Beads.Prefix,
				}
			}

			rigcfgDesc := fmt.Sprintf("Rig config for %s", rigName)
			c, s, u, seedErr = createOrSkipConfigBead(bd, rigcfgSlug, beads.ConfigCategoryRigRegistry,
				rigScope, "", "", rigcfgMetadata, rigcfgDesc)
			if seedErr != nil {
				return created, skipped, updated, fmt.Errorf("seeding rigcfg %s: %w", rigName, seedErr)
			}
			created += c
			skipped += s
			updated += u
		}
	}

	return created, skipped, updated, nil
}

// extractHooksMap extracts the "hooks" key from a settings map.
func extractHooksMap(settings map[string]interface{}) map[string]interface{} {
	hooks, ok := settings["hooks"]
	if !ok {
		return nil
	}
	hooksMap, ok := hooks.(map[string]interface{})
	if !ok {
		return nil
	}
	return hooksMap
}

