// Package beads provides config bead management for the "Everything Is Beads" system.
// Config beads store all gastown configuration as beads, making beads the source
// of truth and the filesystem a cache. See docs/design/everything-is-beads.md.
package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConfigFields holds structured fields for config beads.
// These are stored as "key: value" lines in the description.
type ConfigFields struct {
	Rig       string // Scope: "*" (global), "gt11" (town), "gt11/gastown" (rig)
	Category  string // Config category (e.g., "claude-hooks", "mcp")
	Metadata  string // The config payload as compact JSON
	CreatedBy string // Who created this config bead
	CreatedAt string // ISO 8601 timestamp
	UpdatedBy string // Who last updated this config bead
	UpdatedAt string // ISO 8601 timestamp of last update
}

// Config category constants matching the design spec.
const (
	ConfigCategoryIdentity       = "identity"
	ConfigCategoryClaudeHooks    = "claude-hooks"
	ConfigCategoryMCP            = "mcp"
	ConfigCategoryRigRegistry    = "rig-registry"
	ConfigCategoryAgentPreset    = "agent-preset"
	ConfigCategoryRoleDefinition = "role-definition"
	ConfigCategorySlackRouting   = "slack-routing"
	ConfigCategoryAccounts       = "accounts"
	ConfigCategoryDaemon         = "daemon"
	ConfigCategoryMessaging      = "messaging"
	ConfigCategoryEscalation     = "escalation"
)

// ValidConfigCategories maps valid config category names to true.
var ValidConfigCategories = map[string]bool{
	ConfigCategoryIdentity:       true,
	ConfigCategoryClaudeHooks:    true,
	ConfigCategoryMCP:            true,
	ConfigCategoryRigRegistry:    true,
	ConfigCategoryAgentPreset:    true,
	ConfigCategoryRoleDefinition: true,
	ConfigCategorySlackRouting:   true,
	ConfigCategoryAccounts:       true,
	ConfigCategoryDaemon:         true,
	ConfigCategoryMessaging:      true,
	ConfigCategoryEscalation:     true,
}

// ConfigBeadID returns the bead ID for a config slug.
// Format: hq-cfg-<slug> — all config beads use HQ prefix for cross-town visibility.
func ConfigBeadID(slug string) string {
	return "hq-cfg-" + slug
}

// ConfigScopeLabels builds the label set for a config bead from its scope fields.
// The rig field determines the base scope, with optional role and agent refinements.
//
// Scope hierarchy:
//
//	"*"              → scope:global
//	"gt11"           → town:gt11
//	"gt11/gastown"   → town:gt11, rig:gastown
//
// Adding role/agent:
//
//	role="crew"      → adds role:crew
//	agent="slack"    → adds agent:slack
func ConfigScopeLabels(rig, role, agent string) []string {
	var labels []string

	if rig == "*" {
		labels = append(labels, "scope:global")
	} else if strings.Contains(rig, "/") {
		// Rig-scoped: "gt11/gastown" → town:gt11, rig:gastown
		parts := strings.SplitN(rig, "/", 2)
		labels = append(labels, "town:"+parts[0])
		labels = append(labels, "rig:"+parts[1])
	} else if rig != "" {
		// Town-scoped: "gt11" → town:gt11
		labels = append(labels, "town:"+rig)
	}

	if role != "" {
		labels = append(labels, "role:"+role)
	}
	if agent != "" {
		labels = append(labels, "agent:"+agent)
	}

	return labels
}

// FormatConfigDescription creates a description string from config fields.
// The metadata JSON is stored as a compact single-line value.
func FormatConfigDescription(title string, fields *ConfigFields) string {
	if fields == nil {
		return title
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")

	if fields.Rig != "" {
		lines = append(lines, fmt.Sprintf("rig: %s", fields.Rig))
	} else {
		lines = append(lines, "rig: *")
	}

	if fields.Category != "" {
		lines = append(lines, fmt.Sprintf("category: %s", fields.Category))
	}

	if fields.CreatedBy != "" {
		lines = append(lines, fmt.Sprintf("created_by: %s", fields.CreatedBy))
	}
	if fields.CreatedAt != "" {
		lines = append(lines, fmt.Sprintf("created_at: %s", fields.CreatedAt))
	}
	if fields.UpdatedBy != "" {
		lines = append(lines, fmt.Sprintf("updated_by: %s", fields.UpdatedBy))
	}
	if fields.UpdatedAt != "" {
		lines = append(lines, fmt.Sprintf("updated_at: %s", fields.UpdatedAt))
	}

	// Metadata is stored last as compact JSON on a single line
	if fields.Metadata != "" {
		lines = append(lines, fmt.Sprintf("metadata: %s", fields.Metadata))
	}

	return strings.Join(lines, "\n")
}

// ParseConfigFields extracts config fields from an issue's description.
func ParseConfigFields(description string) *ConfigFields {
	fields := &ConfigFields{
		Rig: "*", // Default to global scope
	}

	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])
		if value == "null" || value == "" {
			value = ""
		}

		switch strings.ToLower(key) {
		case "rig":
			if value != "" {
				fields.Rig = value
			}
		case "category":
			fields.Category = value
		case "metadata":
			fields.Metadata = value
		case "created_by":
			fields.CreatedBy = value
		case "created_at":
			fields.CreatedAt = value
		case "updated_by":
			fields.UpdatedBy = value
		case "updated_at":
			fields.UpdatedAt = value
		}
	}

	return fields
}

// compactJSON compacts a JSON string by removing unnecessary whitespace.
// Returns the original string if it's not valid JSON.
func compactJSON(s string) string {
	var buf json.RawMessage
	if err := json.Unmarshal([]byte(s), &buf); err != nil {
		return s
	}
	compacted, err := json.Marshal(buf)
	if err != nil {
		return s
	}
	return string(compacted)
}

// validateConfigFields checks that required fields are present and valid.
func validateConfigFields(fields *ConfigFields) error {
	if fields == nil {
		return fmt.Errorf("config fields are required")
	}
	if fields.Rig == "" {
		return fmt.Errorf("rig field is required (use \"*\" for global scope)")
	}
	if fields.Category == "" {
		return fmt.Errorf("category field is required")
	}
	if !ValidConfigCategories[fields.Category] {
		return fmt.Errorf("invalid config category %q", fields.Category)
	}
	if fields.Metadata == "" {
		return fmt.Errorf("metadata field is required (must be non-empty JSON)")
	}
	// Validate metadata is valid JSON
	if !json.Valid([]byte(fields.Metadata)) {
		return fmt.Errorf("metadata must be valid JSON")
	}
	return nil
}

// CreateConfigBead creates a config bead with the given slug and fields.
// The ID format is: hq-cfg-<slug> (e.g., hq-cfg-hooks-base, hq-cfg-town-gt11).
// Config beads are town-level entities (hq- prefix) for cross-town visibility.
//
// Required fields: Rig, Category, Metadata.
// Optional role and agent parameters add role:/agent: scope labels.
func (b *Beads) CreateConfigBead(slug string, fields *ConfigFields, role, agent string) (*Issue, error) {
	if err := validateConfigFields(fields); err != nil {
		return nil, fmt.Errorf("invalid config bead: %w", err)
	}

	id := ConfigBeadID(slug)
	title := fields.Category + ": " + slug

	// Ensure metadata is compact JSON
	fields.Metadata = compactJSON(fields.Metadata)

	// Populate timestamps
	if fields.CreatedAt == "" {
		fields.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if fields.CreatedBy == "" {
		fields.CreatedBy = b.getActor()
	}

	description := FormatConfigDescription(title, fields)

	// Build labels: gt:config + config:<category> + scope labels
	labels := []string{"gt:config", "config:" + fields.Category}
	labels = append(labels, ConfigScopeLabels(fields.Rig, role, agent)...)

	args := []string{"create", "--json",
		"--id=" + id,
		"--title=" + title,
		"--description=" + description,
		"--type=config",
		"--labels=" + strings.Join(labels, ","),
		"--force", // HQ prefix needs --force for cross-prefix creation
	}

	if actor := b.getActor(); actor != "" {
		args = append(args, "--actor="+actor)
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var issue Issue
	if err := json.Unmarshal(out, &issue); err != nil {
		return nil, fmt.Errorf("parsing bd create output: %w", err)
	}

	return &issue, nil
}

// GetConfigBead retrieves a config bead by its full ID.
// Returns nil, nil, nil if not found.
func (b *Beads) GetConfigBead(id string) (*Issue, *ConfigFields, error) {
	issue, err := b.Show(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	if !HasLabel(issue, "gt:config") {
		return nil, nil, fmt.Errorf("bead %s is not a config bead (missing gt:config label)", id)
	}

	fields := ParseConfigFields(issue.Description)
	return issue, fields, nil
}

// GetConfigBeadBySlug retrieves a config bead by its slug.
// This is a convenience wrapper around GetConfigBead using the standard ID format.
func (b *Beads) GetConfigBeadBySlug(slug string) (*Issue, *ConfigFields, error) {
	return b.GetConfigBead(ConfigBeadID(slug))
}

// UpdateConfigMetadata updates the metadata payload of a config bead.
// The new metadata must be valid JSON.
func (b *Beads) UpdateConfigMetadata(id string, metadata string) error {
	if !json.Valid([]byte(metadata)) {
		return fmt.Errorf("metadata must be valid JSON")
	}

	issue, fields, err := b.GetConfigBead(id)
	if err != nil {
		return err
	}
	if issue == nil {
		return ErrNotFound
	}

	fields.Metadata = compactJSON(metadata)
	fields.UpdatedBy = b.getActor()
	fields.UpdatedAt = time.Now().Format(time.RFC3339)

	description := FormatConfigDescription(issue.Title, fields)
	return b.Update(id, UpdateOptions{Description: &description})
}

// UpdateConfigFields updates all fields of a config bead.
// This replaces the description entirely with the new fields.
func (b *Beads) UpdateConfigFields(id string, fields *ConfigFields) error {
	if err := validateConfigFields(fields); err != nil {
		return fmt.Errorf("invalid config bead: %w", err)
	}

	issue, err := b.Show(id)
	if err != nil {
		return err
	}

	fields.Metadata = compactJSON(fields.Metadata)
	fields.UpdatedBy = b.getActor()
	fields.UpdatedAt = time.Now().Format(time.RFC3339)

	description := FormatConfigDescription(issue.Title, fields)
	return b.Update(id, UpdateOptions{Description: &description})
}

// DeleteConfigBead permanently deletes a config bead.
func (b *Beads) DeleteConfigBead(id string) error {
	_, err := b.run("delete", id, "--hard", "--force")
	return err
}

// ListConfigBeads returns all config beads.
func (b *Beads) ListConfigBeads() ([]*Issue, error) {
	out, err := b.run("list", "--label=gt:config", "--json")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd list output: %w", err)
	}

	return issues, nil
}

// ListConfigBeadsByCategory returns config beads matching a specific category.
// The category is used as a label filter: config:<category>.
func (b *Beads) ListConfigBeadsByCategory(category string) ([]*Issue, error) {
	if !ValidConfigCategories[category] {
		return nil, fmt.Errorf("invalid config category %q", category)
	}

	out, err := b.run("list", "--label=config:"+category, "--json")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd list output: %w", err)
	}

	return issues, nil
}

// ListConfigBeadsForScope returns config beads applicable to a given scope.
// It queries by category label and then filters results by scope applicability.
// A bead applies to a scope if:
//   - It has scope:global (applies to everything)
//   - Its town label matches the requested town
//   - Its rig label matches the requested rig (within the matching town)
//
// Results are returned in specificity order (least specific first) for merge.
func (b *Beads) ListConfigBeadsForScope(category, town, rig, role, agent string) ([]*Issue, []*ConfigFields, error) {
	issues, err := b.ListConfigBeadsByCategory(category)
	if err != nil {
		return nil, nil, err
	}

	type scored struct {
		issue *Issue
		fields *ConfigFields
		score  int
	}

	var matches []scored
	for _, issue := range issues {
		fields := ParseConfigFields(issue.Description)

		score := configScopeScore(issue, town, rig, role, agent)
		if score < 0 {
			continue // Not applicable to this scope
		}

		matches = append(matches, scored{issue: issue, fields: fields, score: score})
	}

	// Sort by specificity (ascending) for correct merge order
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[i].score > matches[j].score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	resultIssues := make([]*Issue, len(matches))
	resultFields := make([]*ConfigFields, len(matches))
	for i, m := range matches {
		resultIssues[i] = m.issue
		resultFields[i] = m.fields
	}

	return resultIssues, resultFields, nil
}

// configScopeScore calculates specificity score for a config bead against a target scope.
// Returns -1 if the bead does not apply to the given scope.
// Higher scores indicate more specific matches (used for merge ordering).
//
// Score levels:
//
//	0: scope:global (no additional qualifiers)
//	1: scope:global + role match
//	2: town match + rig match
//	3: town match + rig match + role match
//	4: town match + rig match + agent match
func configScopeScore(issue *Issue, town, rig, role, agent string) int {
	isGlobal := HasLabel(issue, "scope:global")
	townMatch := town != "" && HasLabel(issue, "town:"+town)
	rigMatch := rig != "" && HasLabel(issue, "rig:"+rig)
	roleMatch := role != "" && HasLabel(issue, "role:"+role)
	agentMatch := agent != "" && HasLabel(issue, "agent:"+agent)

	// Check if the bead has a rig label at all (even if it doesn't match ours).
	// A bead scoped to a specific rig should NOT match a different rig.
	beadHasRigLabel := hasLabelPrefix(issue, "rig:")

	// Agent-specific match (most specific)
	if townMatch && rigMatch && agentMatch {
		return 4
	}

	// Rig + role match
	if townMatch && rigMatch && roleMatch {
		return 3
	}

	// Rig match (town-scoped)
	if townMatch && rigMatch {
		return 2
	}

	// Global + role match
	if isGlobal && roleMatch {
		return 1
	}

	// Global match (least specific)
	if isGlobal {
		return 0
	}

	// Town-only match (between global and rig) — but only if the bead
	// is not scoped to a specific rig that doesn't match ours.
	if townMatch && !beadHasRigLabel {
		return 1
	}

	return -1 // Does not match this scope
}

// ResolveConfigMetadata queries config beads for a category and scope, returning
// the raw metadata JSON strings in merge order (least-specific to most-specific).
// This is designed for callers that need to pass metadata to materialization
// functions without importing both beads and the target package.
//
// Returns nil (not error) if no config beads are found.
func (b *Beads) ResolveConfigMetadata(category, town, rig, role, agent string) ([]string, error) {
	_, fields, err := b.ListConfigBeadsForScope(category, town, rig, role, agent)
	if err != nil {
		return nil, err
	}

	var layers []string
	for _, f := range fields {
		if f.Metadata != "" {
			layers = append(layers, f.Metadata)
		}
	}

	return layers, nil
}

// hasLabelPrefix checks if an issue has any label starting with the given prefix.
func hasLabelPrefix(issue *Issue, prefix string) bool {
	for _, l := range issue.Labels {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
