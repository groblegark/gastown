// Package claude - config bead materialization for Claude Code settings.
//
// MaterializeSettings merges config bead metadata payloads into a settings.json
// file, implementing the "Everything Is Beads" merge strategy:
//   - Top-level keys: OVERRIDE (more specific wins)
//   - hooks.<hookType> arrays: APPEND (more specific adds)
//   - Explicit null: suppresses inherited value
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
