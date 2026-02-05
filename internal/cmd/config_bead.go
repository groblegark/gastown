// Package cmd provides CLI commands for the gt tool.
// This file implements the gt config bead subcommands for managing config beads.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	configBeadListJSON     bool
	configBeadListCategory string
	configBeadListScope    string
	configBeadShowJSON     bool
	configBeadCreateRig    string
	configBeadCreateRole   string
	configBeadCreateAgent  string
	configBeadUpdateMeta   string
	configBeadUpdateMerge  string
)

// ConfigBeadListItem represents a config bead in list output.
type ConfigBeadListItem struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Category string `json:"category"`
	Rig      string `json:"rig"`
	Title    string `json:"title"`
}

// ConfigBeadDetail represents detailed config bead output.
type ConfigBeadDetail struct {
	ID        string      `json:"id"`
	Slug      string      `json:"slug"`
	Category  string      `json:"category"`
	Rig       string      `json:"rig"`
	Labels    []string    `json:"labels,omitempty"`
	CreatedBy string      `json:"created_by,omitempty"`
	CreatedAt string      `json:"created_at,omitempty"`
	UpdatedBy string      `json:"updated_by,omitempty"`
	UpdatedAt string      `json:"updated_at,omitempty"`
	Metadata  interface{} `json:"metadata"`
}

var configBeadCmd = &cobra.Command{
	Use:   "bead",
	Short: "Manage config beads",
	Long: `Manage config beads directly (CRUD operations).

Config beads store configuration as beads, making beads the source of truth
and the filesystem a cache.

Commands:
  gt config bead list              List all config beads
  gt config bead show <slug>       Show a specific config bead
  gt config bead create <slug>     Create a config bead
  gt config bead update <slug>     Update a config bead
  gt config bead delete <slug>     Delete a config bead`,
	RunE: requireSubcommand,
}

var configBeadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List config beads",
	Long: `List all config beads or filter by category/scope.

Examples:
  gt config bead list                             # All config beads
  gt config bead list --category=claude-hooks      # Filter by category
  gt config bead list --scope=gt11/gastown         # Filter by scope
  gt config bead list --json                       # JSON output`,
	RunE: runConfigBeadList,
}

var configBeadShowCmd = &cobra.Command{
	Use:   "show <slug>",
	Short: "Show a specific config bead",
	Long: `Show the details of a config bead by its slug.

The slug is the identifier without the hq-cfg- prefix.

Examples:
  gt config bead show hooks-base
  gt config bead show town-gt11
  gt config bead show hooks-base --json`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigBeadShow,
}

var configBeadCreateCmd = &cobra.Command{
	Use:   "create <slug> --category=<category> --metadata=<json>",
	Short: "Create a config bead",
	Long: `Create a new config bead with the given slug and metadata.

Valid categories: identity, claude-hooks, mcp, rig-registry, agent-preset,
role-definition, slack-routing, accounts, daemon, messaging, escalation

Examples:
  gt config bead create hooks-custom --category=claude-hooks \
    --rig="gt11/gastown" --metadata='{"hooks":{}}'
  gt config bead create my-mcp --category=mcp --rig="*" \
    --metadata='{"mcpServers":{}}' --role=crew`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigBeadCreate,
}

var configBeadUpdateCmd = &cobra.Command{
	Use:   "update <slug>",
	Short: "Update a config bead",
	Long: `Update the metadata of an existing config bead.

Use --metadata to replace the entire metadata payload.
Use --merge-metadata to deep-merge into existing metadata.

Examples:
  gt config bead update hooks-base --metadata='{"editorMode":"vim"}'
  gt config bead update hooks-base --merge-metadata='{"hooks":{"PreCompact":[{"command":"gt prime"}]}}'`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigBeadUpdate,
}

var configBeadDeleteCmd = &cobra.Command{
	Use:   "delete <slug>",
	Short: "Delete a config bead",
	Long: `Permanently delete a config bead.

Examples:
  gt config bead delete hooks-custom`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigBeadDelete,
}

func runConfigBeadList(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	var issues []*beads.Issue
	if configBeadListCategory != "" {
		issues, err = bd.ListConfigBeadsByCategory(configBeadListCategory)
	} else {
		issues, err = bd.ListConfigBeads()
	}
	if err != nil {
		return fmt.Errorf("listing config beads: %w", err)
	}

	// Filter by scope if requested
	if configBeadListScope != "" {
		var filtered []*beads.Issue
		for _, issue := range issues {
			fields := beads.ParseConfigFields(issue.Description)
			if fields.Rig == configBeadListScope || fields.Rig == "*" {
				filtered = append(filtered, issue)
			}
		}
		issues = filtered
	}

	// Build list items
	items := make([]ConfigBeadListItem, 0, len(issues))
	for _, issue := range issues {
		fields := beads.ParseConfigFields(issue.Description)
		slug := strings.TrimPrefix(issue.ID, "hq-cfg-")
		items = append(items, ConfigBeadListItem{
			ID:       issue.ID,
			Slug:     slug,
			Category: fields.Category,
			Rig:      fields.Rig,
			Title:    issue.Title,
		})
	}

	// Sort by category then slug
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Slug < items[j].Slug
	})

	if configBeadListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	if len(items) == 0 {
		fmt.Println("No config beads found.")
		return nil
	}

	// Group by category for display
	currentCat := ""
	for _, item := range items {
		if item.Category != currentCat {
			if currentCat != "" {
				fmt.Println()
			}
			currentCat = item.Category
			fmt.Printf("%s\n", style.Bold.Render(currentCat))
		}
		rigLabel := style.Dim.Render("[" + item.Rig + "]")
		fmt.Printf("  %-30s %s %s\n", item.Slug, rigLabel, item.ID)
	}

	fmt.Printf("\n%d config beads total\n", len(items))
	return nil
}

func runConfigBeadShow(cmd *cobra.Command, args []string) error {
	slug := args[0]

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	issue, fields, err := bd.GetConfigBeadBySlug(slug)
	if err != nil {
		return fmt.Errorf("getting config bead: %w", err)
	}
	if issue == nil {
		return fmt.Errorf("config bead '%s' not found (id: %s)", slug, beads.ConfigBeadID(slug))
	}

	// Parse metadata as generic JSON for pretty-printing
	var metadata interface{}
	if fields.Metadata != "" {
		if jsonErr := json.Unmarshal([]byte(fields.Metadata), &metadata); jsonErr != nil {
			metadata = fields.Metadata // Use as raw string if not valid JSON
		}
	}

	detail := ConfigBeadDetail{
		ID:        issue.ID,
		Slug:      slug,
		Category:  fields.Category,
		Rig:       fields.Rig,
		Labels:    issue.Labels,
		CreatedBy: fields.CreatedBy,
		CreatedAt: fields.CreatedAt,
		UpdatedBy: fields.UpdatedBy,
		UpdatedAt: fields.UpdatedAt,
		Metadata:  metadata,
	}

	if configBeadShowJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(detail)
	}

	// Text output
	fmt.Printf("%s\n\n", style.Bold.Render("Config Bead: "+slug))
	fmt.Printf("ID:       %s\n", detail.ID)
	fmt.Printf("Category: %s\n", detail.Category)
	fmt.Printf("Scope:    %s\n", detail.Rig)
	if len(detail.Labels) > 0 {
		fmt.Printf("Labels:   %s\n", strings.Join(detail.Labels, ", "))
	}
	if detail.CreatedBy != "" {
		fmt.Printf("Created:  %s by %s\n", detail.CreatedAt, detail.CreatedBy)
	}
	if detail.UpdatedBy != "" {
		fmt.Printf("Updated:  %s by %s\n", detail.UpdatedAt, detail.UpdatedBy)
	}

	if metadata != nil {
		metaJSON, _ := json.MarshalIndent(metadata, "", "  ")
		fmt.Printf("\n%s\n%s\n", style.Bold.Render("Metadata:"), string(metaJSON))
	}

	return nil
}

func runConfigBeadCreate(cmd *cobra.Command, args []string) error {
	slug := args[0]

	// Read metadata from flag or stdin
	metadataStr, _ := cmd.Flags().GetString("metadata")
	if metadataStr == "" {
		return fmt.Errorf("--metadata flag is required (must be valid JSON)")
	}

	category, _ := cmd.Flags().GetString("category")
	if category == "" {
		return fmt.Errorf("--category flag is required")
	}
	if !beads.ValidConfigCategories[category] {
		validCats := make([]string, 0, len(beads.ValidConfigCategories))
		for k := range beads.ValidConfigCategories {
			validCats = append(validCats, k)
		}
		sort.Strings(validCats)
		return fmt.Errorf("invalid category %q; valid categories: %s", category, strings.Join(validCats, ", "))
	}

	// Validate metadata is valid JSON
	if !json.Valid([]byte(metadataStr)) {
		return fmt.Errorf("--metadata must be valid JSON")
	}

	rig := configBeadCreateRig
	if rig == "" {
		rig = "*"
	}

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)

	fields := &beads.ConfigFields{
		Rig:      rig,
		Category: category,
		Metadata: metadataStr,
	}

	issue, err := bd.CreateConfigBead(slug, fields, configBeadCreateRole, configBeadCreateAgent)
	if err != nil {
		return fmt.Errorf("creating config bead: %w", err)
	}

	fmt.Printf("%s Created config bead: %s (id: %s)\n", style.Success.Render("✓"), slug, issue.ID)
	return nil
}

func runConfigBeadUpdate(cmd *cobra.Command, args []string) error {
	slug := args[0]

	if configBeadUpdateMeta == "" && configBeadUpdateMerge == "" {
		return fmt.Errorf("specify --metadata or --merge-metadata to update")
	}

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)
	id := beads.ConfigBeadID(slug)

	if configBeadUpdateMeta != "" {
		// Full metadata replacement
		if !json.Valid([]byte(configBeadUpdateMeta)) {
			return fmt.Errorf("--metadata must be valid JSON")
		}
		if err := bd.UpdateConfigMetadata(id, configBeadUpdateMeta); err != nil {
			return fmt.Errorf("updating config bead: %w", err)
		}
		fmt.Printf("%s Updated config bead: %s\n", style.Success.Render("✓"), slug)
		return nil
	}

	if configBeadUpdateMerge != "" {
		// Merge metadata
		if !json.Valid([]byte(configBeadUpdateMerge)) {
			return fmt.Errorf("--merge-metadata must be valid JSON")
		}

		// Get existing metadata
		issue, fields, err := bd.GetConfigBead(id)
		if err != nil {
			return fmt.Errorf("getting config bead: %w", err)
		}
		if issue == nil {
			return fmt.Errorf("config bead '%s' not found", slug)
		}

		// Parse existing metadata
		var existing map[string]interface{}
		if fields.Metadata != "" {
			if jsonErr := json.Unmarshal([]byte(fields.Metadata), &existing); jsonErr != nil {
				return fmt.Errorf("parsing existing metadata: %w", jsonErr)
			}
		} else {
			existing = make(map[string]interface{})
		}

		// Parse merge payload
		var merge map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(configBeadUpdateMerge), &merge); jsonErr != nil {
			return fmt.Errorf("parsing merge metadata: %w", jsonErr)
		}

		// Deep merge
		deepMerge(existing, merge)

		// Write back
		merged, _ := json.Marshal(existing)
		if err := bd.UpdateConfigMetadata(id, string(merged)); err != nil {
			return fmt.Errorf("updating config bead: %w", err)
		}
		fmt.Printf("%s Merged metadata into config bead: %s\n", style.Success.Render("✓"), slug)
		return nil
	}

	return nil // unreachable
}

func runConfigBeadDelete(cmd *cobra.Command, args []string) error {
	slug := args[0]

	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	bd := beads.New(townRoot)
	id := beads.ConfigBeadID(slug)

	// Verify it exists first
	issue, _, err := bd.GetConfigBead(id)
	if err != nil {
		return fmt.Errorf("checking config bead: %w", err)
	}
	if issue == nil {
		return fmt.Errorf("config bead '%s' not found (id: %s)", slug, id)
	}

	if err := bd.DeleteConfigBead(id); err != nil {
		return fmt.Errorf("deleting config bead: %w", err)
	}

	fmt.Printf("%s Deleted config bead: %s (id: %s)\n", style.Success.Render("✓"), slug, id)
	return nil
}

// deepMerge recursively merges src into dst.
func deepMerge(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if srcVal == nil {
			delete(dst, key)
			continue
		}
		if srcMap, ok := srcVal.(map[string]interface{}); ok {
			if dstMap, ok := dst[key].(map[string]interface{}); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}

func init() {
	// List flags
	configBeadListCmd.Flags().BoolVar(&configBeadListJSON, "json", false, "Output as JSON")
	configBeadListCmd.Flags().StringVar(&configBeadListCategory, "category", "", "Filter by category")
	configBeadListCmd.Flags().StringVar(&configBeadListScope, "scope", "", "Filter by scope (e.g., gt11/gastown)")

	// Show flags
	configBeadShowCmd.Flags().BoolVar(&configBeadShowJSON, "json", false, "Output as JSON")

	// Create flags
	configBeadCreateCmd.Flags().String("category", "", "Config category (required)")
	configBeadCreateCmd.Flags().String("metadata", "", "Config payload as JSON (required)")
	configBeadCreateCmd.Flags().StringVar(&configBeadCreateRig, "rig", "*", "Scope: \"*\" (global), \"town\", or \"town/rig\"")
	configBeadCreateCmd.Flags().StringVar(&configBeadCreateRole, "role", "", "Role scope (e.g., crew, polecat)")
	configBeadCreateCmd.Flags().StringVar(&configBeadCreateAgent, "agent", "", "Agent scope (e.g., slack)")

	// Update flags
	configBeadUpdateCmd.Flags().StringVar(&configBeadUpdateMeta, "metadata", "", "Replace metadata entirely (JSON)")
	configBeadUpdateCmd.Flags().StringVar(&configBeadUpdateMerge, "merge-metadata", "", "Deep-merge into existing metadata (JSON)")

	// Register subcommands
	configBeadCmd.AddCommand(configBeadListCmd)
	configBeadCmd.AddCommand(configBeadShowCmd)
	configBeadCmd.AddCommand(configBeadCreateCmd)
	configBeadCmd.AddCommand(configBeadUpdateCmd)
	configBeadCmd.AddCommand(configBeadDeleteCmd)

	configCmd.AddCommand(configBeadCmd)
}
