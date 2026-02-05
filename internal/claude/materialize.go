// Package claude provides config bead materialization for Claude Code settings.
//
// MaterializeSettings merges config bead metadata payloads into a settings.json
// file, implementing the "Everything Is Beads" merge strategy:
//   - Top-level keys: OVERRIDE (more specific wins)
//   - hooks.<hookType> arrays: APPEND (more specific adds)
//   - Explicit null: suppresses inherited value
//
// MaterializeMCPConfig merges MCP config bead metadata into .mcp.json:
//   - mcpServers map: MERGE (rig-specific servers added to global)
//   - Individual server config: OVERRIDE (rig overrides global for same server name)
//   - Null value: REMOVE (rig removes a global server)
package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MaterializeSettings merges config bead metadata payloads and writes settings.json.
// If metadataPayloads is empty, falls back to the embedded template for the role.
// Unlike EnsureSettings, this ALWAYS writes when config beads are provided,
// since config beads are the source of truth and the filesystem is a cache.
func MaterializeSettings(workDir, role string, metadataPayloads []string) error {
	if len(metadataPayloads) == 0 {
		// No config beads - fall back to embedded template
		return EnsureSettings(workDir, RoleTypeFor(role))
	}

	merged, err := MergeHooksConfig(metadataPayloads)
	if err != nil {
		// Merge failed - fall back to embedded template
		fmt.Printf("Warning: failed to merge config beads (%v), using embedded templates\n", err)
		return EnsureSettings(workDir, RoleTypeFor(role))
	}

	return WriteSettings(workDir, merged)
}

// WriteSettings writes a merged settings map to workDir/.claude/settings.json.
func WriteSettings(workDir string, settings map[string]interface{}) error {
	claudeDir := filepath.Join(workDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude directory: %w", err)
	}

	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, append(settingsJSON, '\n'), 0600); err != nil {
		return fmt.Errorf("writing settings.json: %w", err)
	}

	return nil
}

// MergeHooksConfig merges multiple config bead metadata JSON payloads.
// Payloads should be in specificity order (least specific first).
//
// Merge rules per the "Everything Is Beads" design:
//   - Top-level keys: OVERRIDE (more specific wins)
//   - hooks.<hookType> arrays: APPEND (more specific adds)
//   - Explicit null: suppresses inherited value
func MergeHooksConfig(payloads []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for _, payload := range payloads {
		if payload == "" {
			continue
		}

		var layer map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &layer); err != nil {
			return nil, fmt.Errorf("parsing config bead metadata: %w", err)
		}

		mergeSettingsLayer(result, layer)
	}

	return result, nil
}

// mergeSettingsLayer merges a single settings layer into the result.
func mergeSettingsLayer(result, layer map[string]interface{}) {
	for key, value := range layer {
		if value == nil {
			// Explicit null suppresses inherited value
			delete(result, key)
			continue
		}

		if key == "hooks" {
			mergeHooksField(result, value)
			continue
		}

		// Top-level key: OVERRIDE
		result[key] = value
	}
}

// mergeHooksField handles the special merge logic for the "hooks" field.
// Hook type arrays are APPENDed; null suppresses an inherited hook type.
func mergeHooksField(result map[string]interface{}, hooksValue interface{}) {
	newHooks, ok := hooksValue.(map[string]interface{})
	if !ok {
		result["hooks"] = hooksValue
		return
	}

	var existingHooks map[string]interface{}
	if raw, exists := result["hooks"]; exists {
		if eh, ok := raw.(map[string]interface{}); ok {
			existingHooks = eh
		}
	}
	if existingHooks == nil {
		existingHooks = make(map[string]interface{})
	}

	for hookType, hookValue := range newHooks {
		if hookValue == nil {
			// Explicit null suppresses this hook type
			delete(existingHooks, hookType)
			continue
		}

		newArray, ok := hookValue.([]interface{})
		if !ok {
			// Not an array - override entirely
			existingHooks[hookType] = hookValue
			continue
		}

		// APPEND to existing array
		if existing, exists := existingHooks[hookType]; exists {
			if existingArr, ok := existing.([]interface{}); ok {
				existingHooks[hookType] = append(existingArr, newArray...)
				continue
			}
		}
		existingHooks[hookType] = newArray
	}

	result["hooks"] = existingHooks
}

// ConfigScope holds scope information for config bead resolution.
// Used to determine which config beads apply at spawn time.
type ConfigScope struct {
	Town  string // Town name (e.g., "gt11")
	Rig   string // Rig name (e.g., "gastown")
	Role  string // Role name (e.g., "polecat", "crew")
	Agent string // Agent name (e.g., "slit", "slack")
}

// ScopeFromEnv reads scope information from Gas Town environment variables.
func ScopeFromEnv() ConfigScope {
	townRoot := os.Getenv("GT_TOWN_ROOT")
	town := ""
	if townRoot != "" {
		town = filepath.Base(townRoot)
	}

	rig := os.Getenv("GT_RIG")
	role := os.Getenv("GT_ROLE")

	agent := ""
	switch role {
	case "polecat":
		agent = os.Getenv("GT_POLECAT")
	case "crew":
		agent = os.Getenv("GT_CREW")
	}

	return ConfigScope{
		Town:  town,
		Rig:   rig,
		Role:  role,
		Agent: agent,
	}
}

// MaterializeMCPConfig generates .mcp.json from pre-resolved config bead metadata.
// metadataLayers contains raw JSON strings from config beads, ordered from
// least-specific to most-specific (as returned by beads.ListConfigBeadsForScope).
//
// If metadataLayers is empty, falls back to the embedded mcp.json template.
// The caller is responsible for querying beads and extracting metadata payloads.
func MaterializeMCPConfig(workDir string, metadataLayers []string) error {
	if len(metadataLayers) == 0 {
		return EnsureMCPConfig(workDir)
	}

	merged := make(map[string]interface{})
	for _, metadata := range metadataLayers {
		if metadata == "" {
			continue
		}

		var layer map[string]interface{}
		if err := json.Unmarshal([]byte(metadata), &layer); err != nil {
			continue // Skip invalid JSON
		}

		MergeMCPConfig(merged, layer)
	}

	if len(merged) == 0 {
		return EnsureMCPConfig(workDir)
	}

	mcpJSON, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling MCP config: %w", err)
	}

	mcpPath := filepath.Join(workDir, ".mcp.json")
	if err := os.WriteFile(mcpPath, append(mcpJSON, '\n'), 0644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	return nil
}

// MergeMCPConfig merges an MCP config layer into the base config.
//
// MCP merge strategy:
//   - mcpServers map: MERGE (rig-specific servers added to global)
//   - Individual server config: OVERRIDE (rig overrides global for same server name)
//   - Null value: REMOVE (rig removes a global server)
func MergeMCPConfig(base, layer map[string]interface{}) {
	for key, layerVal := range layer {
		if key == "mcpServers" {
			mergeServers(base, layerVal)
			continue
		}

		// For non-mcpServers keys, simple override
		if layerVal == nil {
			delete(base, key)
		} else {
			base[key] = layerVal
		}
	}
}

// mergeServers merges the mcpServers map from a layer into the base config.
func mergeServers(base map[string]interface{}, layerServers interface{}) {
	servers, ok := layerServers.(map[string]interface{})
	if !ok {
		return
	}

	baseServers, ok := base["mcpServers"].(map[string]interface{})
	if !ok {
		baseServers = make(map[string]interface{})
		base["mcpServers"] = baseServers
	}

	for name, config := range servers {
		if config == nil {
			// Null value: REMOVE server
			delete(baseServers, name)
		} else {
			// OVERRIDE: replace server config entirely
			baseServers[name] = config
		}
	}
}
