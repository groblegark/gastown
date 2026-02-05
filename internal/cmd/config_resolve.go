// Package cmd provides CLI commands for the gt tool.
// This file implements gt config resolve, verify, and materialize subcommands.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/claude"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	resolveJSON  bool
	resolveTown  string
	resolveRig   string
	resolveRole  string
	resolveAgent string

	verifyFix bool

	materializeHooks bool
	materializeMCP   bool
	materializeScope string
)

var configResolveCmd = &cobra.Command{
	Use:   "resolve <category>",
	Short: "Show effective merged config for a scope",
	Long: `Resolve config beads for a category and scope, showing the effective
merged configuration.

This queries all config beads for the given category, filters by scope,
and merges them in specificity order (least specific to most specific).

Examples:
  gt config resolve claude-hooks --town=gt11 --rig=gastown --role=crew
  gt config resolve mcp --town=gt11 --rig=gastown
  gt config resolve claude-hooks --town=gt11 --rig=gastown --role=crew --agent=slack
  gt config resolve identity --town=gt11 --json`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigResolve,
}

var configVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify config beads match filesystem",
	Long: `Verify that seeded config beads exist and their metadata is consistent.

Checks that expected config beads are present in the beads database.
Use --fix to re-seed missing or outdated beads.

Examples:
  gt config verify
  gt config verify --fix`,
	RunE: runConfigVerify,
}

var configMaterializeCmd = &cobra.Command{
	Use:   "materialize",
	Short: "Write config beads to filesystem",
	Long: `Materialize config beads to filesystem files for debugging or manual use.

This resolves config beads for the given scope and writes the merged
result to the appropriate files (.claude/settings.json or .mcp.json).

Examples:
  gt config materialize --hooks --scope=gt11/gastown/crew/slack
  gt config materialize --mcp --scope=gt11/gastown/crew/slack`,
	RunE: runConfigMaterialize,
}

func runConfigResolve(cmd *cobra.Command, args []string) error {
	category := args[0]

	if !beads.ValidConfigCategories[category] {
		return fmt.Errorf("invalid category %q", category)
	}

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	issues, fields, err := bd.ListConfigBeadsForScope(category, resolveTown, resolveRig, resolveRole, resolveAgent)
	if err != nil {
		return fmt.Errorf("resolving config beads: %w", err)
	}

	if len(issues) == 0 {
		fmt.Printf("No config beads found for category=%s scope=[town=%s rig=%s role=%s agent=%s]\n",
			category, resolveTown, resolveRig, resolveRole, resolveAgent)
		return nil
	}

	// Collect metadata layers
	var layers []string
	for _, f := range fields {
		if f.Metadata != "" {
			layers = append(layers, f.Metadata)
		}
	}

	// For hook categories, use the merge function
	var merged interface{}
	if category == beads.ConfigCategoryClaudeHooks {
		mergedMap, mergeErr := claude.MergeHooksConfig(layers)
		if mergeErr != nil {
			return fmt.Errorf("merging config: %w", mergeErr)
		}
		merged = mergedMap
	} else {
		// For other categories, show layers as-is (simple override merge)
		result := make(map[string]interface{})
		for _, layer := range layers {
			var m map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(layer), &m); jsonErr != nil {
				continue
			}
			for k, v := range m {
				if v == nil {
					delete(result, k)
				} else {
					result[k] = v
				}
			}
		}
		merged = result
	}

	if resolveJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(merged)
	}

	// Text output: show layers then merged result
	fmt.Printf("%s\n\n", style.Bold.Render("Resolved Config: "+category))
	fmt.Printf("Scope: town=%s rig=%s role=%s agent=%s\n", resolveTown, resolveRig, resolveRole, resolveAgent)
	fmt.Printf("Layers: %d config beads matched\n\n", len(issues))

	for i, issue := range issues {
		slug := strings.TrimPrefix(issue.ID, "hq-cfg-")
		fmt.Printf("  %d. %s %s\n", i+1, slug, style.Dim.Render("["+fields[i].Rig+"]"))
	}

	mergedJSON, _ := json.MarshalIndent(merged, "", "  ")
	fmt.Printf("\n%s\n%s\n", style.Bold.Render("Effective Config:"), string(mergedJSON))

	return nil
}

func runConfigVerify(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	issues, err := bd.ListConfigBeads()
	if err != nil {
		return fmt.Errorf("listing config beads: %w", err)
	}

	// Check each config bead for validity
	var problems []string
	valid := 0
	for _, issue := range issues {
		fields := beads.ParseConfigFields(issue.Description)

		// Validate category
		if !beads.ValidConfigCategories[fields.Category] {
			problems = append(problems, fmt.Sprintf("%s: invalid category %q", issue.ID, fields.Category))
			continue
		}

		// Validate metadata is valid JSON
		if fields.Metadata == "" {
			problems = append(problems, fmt.Sprintf("%s: empty metadata", issue.ID))
			continue
		}
		if !json.Valid([]byte(fields.Metadata)) {
			problems = append(problems, fmt.Sprintf("%s: invalid JSON metadata", issue.ID))
			continue
		}

		// Validate required labels
		if !beads.HasLabel(issue, "gt:config") {
			problems = append(problems, fmt.Sprintf("%s: missing gt:config label", issue.ID))
			continue
		}
		if !beads.HasLabel(issue, "config:"+fields.Category) {
			problems = append(problems, fmt.Sprintf("%s: missing config:%s label", issue.ID, fields.Category))
			continue
		}

		valid++
	}

	// Check for expected beads from seed categories
	expectedCategories := []string{
		beads.ConfigCategoryClaudeHooks,
		beads.ConfigCategoryMCP,
		beads.ConfigCategoryIdentity,
	}
	for _, cat := range expectedCategories {
		catIssues, catErr := bd.ListConfigBeadsByCategory(cat)
		if catErr != nil {
			problems = append(problems, fmt.Sprintf("category %s: error listing: %v", cat, catErr))
			continue
		}
		if len(catIssues) == 0 {
			problems = append(problems, fmt.Sprintf("category %s: no beads found (run 'gt config seed' to create)", cat))
		}
	}

	if len(problems) == 0 {
		fmt.Printf("%s All %d config beads valid\n", style.Success.Render("✓"), valid)
		return nil
	}

	fmt.Printf("%s Found %d problems:\n\n", style.Warning.Render("!"), len(problems))
	for _, p := range problems {
		fmt.Printf("  - %s\n", p)
	}

	if verifyFix {
		fmt.Printf("\nRunning seed to fix missing beads...\n")
		seedForce = true
		return runConfigSeed(cmd, nil)
	}

	fmt.Printf("\nRun 'gt config verify --fix' to re-seed missing beads\n")
	return nil
}

func runConfigMaterialize(cmd *cobra.Command, args []string) error {
	if !materializeHooks && !materializeMCP {
		return fmt.Errorf("specify --hooks and/or --mcp to materialize")
	}

	// Parse scope: town/rig/role/agent
	town, rig, role, agent := parseMaterializeScope(materializeScope)

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	// Determine work directory - use cwd
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	if materializeHooks {
		layers, err := bd.ResolveConfigMetadata(beads.ConfigCategoryClaudeHooks, town, rig, role, agent)
		if err != nil {
			return fmt.Errorf("resolving hook config: %w", err)
		}

		if err := claude.MaterializeSettings(workDir, role, layers); err != nil {
			return fmt.Errorf("materializing hooks: %w", err)
		}

		settingsPath := filepath.Join(workDir, ".claude", "settings.json")
		fmt.Printf("%s Materialized hooks to %s\n", style.Success.Render("✓"), settingsPath)
	}

	if materializeMCP {
		layers, err := bd.ResolveConfigMetadata(beads.ConfigCategoryMCP, town, rig, role, agent)
		if err != nil {
			return fmt.Errorf("resolving MCP config: %w", err)
		}

		if err := claude.MaterializeMCPConfig(workDir, layers); err != nil {
			return fmt.Errorf("materializing MCP config: %w", err)
		}

		mcpPath := filepath.Join(workDir, ".mcp.json")
		fmt.Printf("%s Materialized MCP config to %s\n", style.Success.Render("✓"), mcpPath)
	}

	return nil
}

// parseMaterializeScope parses a scope string like "gt11/gastown/crew/slack"
// into individual components.
func parseMaterializeScope(scope string) (town, rig, role, agent string) {
	if scope == "" {
		return "", "", "", ""
	}

	parts := strings.Split(scope, "/")
	if len(parts) >= 1 {
		town = parts[0]
	}
	if len(parts) >= 2 {
		rig = parts[1]
	}
	if len(parts) >= 3 {
		role = parts[2]
	}
	if len(parts) >= 4 {
		agent = parts[3]
	}
	return
}

func init() {
	// Resolve flags
	configResolveCmd.Flags().BoolVar(&resolveJSON, "json", false, "Output as JSON")
	configResolveCmd.Flags().StringVar(&resolveTown, "town", "", "Town scope (e.g., gt11)")
	configResolveCmd.Flags().StringVar(&resolveRig, "rig", "", "Rig scope (e.g., gastown)")
	configResolveCmd.Flags().StringVar(&resolveRole, "role", "", "Role scope (e.g., crew, polecat)")
	configResolveCmd.Flags().StringVar(&resolveAgent, "agent", "", "Agent scope (e.g., slack)")

	// Verify flags
	configVerifyCmd.Flags().BoolVar(&verifyFix, "fix", false, "Re-seed missing or outdated beads")

	// Materialize flags
	configMaterializeCmd.Flags().BoolVar(&materializeHooks, "hooks", false, "Materialize Claude hooks/settings")
	configMaterializeCmd.Flags().BoolVar(&materializeMCP, "mcp", false, "Materialize MCP config")
	configMaterializeCmd.Flags().StringVar(&materializeScope, "scope", "", "Scope: town/rig/role/agent")

	configCmd.AddCommand(configResolveCmd)
	configCmd.AddCommand(configVerifyCmd)
	configCmd.AddCommand(configMaterializeCmd)
}
