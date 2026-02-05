// Package configbeads provides functions for loading configuration from beads.
// This file implements role definition loading from config beads with TOML fallback.
package configbeads

import (
	"encoding/json"
	"fmt"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// LoadRoleDefinitionFromBeads loads a role definition from config beads.
// It queries config beads matching the "role-definition" category for the given scope,
// merging layers in specificity order (global → town → rig → role → agent).
// Returns nil, nil if no role definition beads exist for this role.
func LoadRoleDefinitionFromBeads(bd *beads.Beads, town, rig, roleName string) (*config.RoleDefinition, error) {
	if !config.IsValidRoleName(roleName) {
		return nil, fmt.Errorf("unknown role %q - valid roles: %v", roleName, config.AllRoles())
	}

	// Query config beads for this role's category, scoped by town/rig.
	// The role parameter here refers to the role label on the bead (scope filtering),
	// not an arbitrary role. For role-definition beads, we don't filter by role label
	// since each bead already encodes the role in its slug.
	issues, fields, err := bd.ListConfigBeadsForScope(
		beads.ConfigCategoryRoleDefinition, town, rig, "", "")
	if err != nil {
		return nil, fmt.Errorf("listing role definition beads: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}

	// Filter to only beads for the requested role.
	// Each bead's metadata contains a "role" field we can check.
	var matchingLayers []string
	for i, f := range fields {
		if f.Metadata == "" {
			continue
		}

		// Quick check: does this bead's metadata match our role?
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(f.Metadata), &meta); err != nil {
			continue
		}

		beadRole, _ := meta["role"].(string)
		if beadRole != "" && beadRole != roleName {
			continue
		}

		// Also check the bead ID/title for role slug match.
		// Global beads: hq-cfg-role-<roleName>
		// Town beads: hq-cfg-role-<roleName>-<town>
		// Rig beads: hq-cfg-role-<roleName>-<town>-<rig>
		expectedSlugPrefix := "role-" + roleName
		beadID := issues[i].ID
		expectedIDPrefix := beads.ConfigBeadID(expectedSlugPrefix)
		if beadRole == "" && beadID != "" && len(beadID) >= len(expectedIDPrefix) {
			if beadID[:len(expectedIDPrefix)] != expectedIDPrefix {
				continue
			}
		}

		matchingLayers = append(matchingLayers, f.Metadata)
	}

	if len(matchingLayers) == 0 {
		return nil, nil
	}

	// Merge metadata layers (least specific → most specific)
	merged := make(map[string]interface{})
	for _, layer := range matchingLayers {
		var layerMap map[string]interface{}
		if err := json.Unmarshal([]byte(layer), &layerMap); err != nil {
			continue
		}
		deepMergeRoleConfig(merged, layerMap)
	}

	// Marshal merged config and unmarshal into typed struct
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged role config: %w", err)
	}

	var def config.RoleDefinition
	if err := json.Unmarshal(mergedJSON, &def); err != nil {
		return nil, fmt.Errorf("parsing merged role config: %w", err)
	}

	return &def, nil
}

// LoadRoleDefinition loads a role definition, trying beads first with TOML fallback.
// This is the preferred entry point for loading role definitions.
func LoadRoleDefinition(bd *beads.Beads, townRoot, townName, rigPath, rigName, roleName string) (*config.RoleDefinition, error) {
	// Try beads first
	def, err := LoadRoleDefinitionFromBeads(bd, townName, rigName, roleName)
	if err == nil && def != nil {
		return def, nil
	}

	// Fall back to TOML-based loading
	return config.LoadRoleDefinition(townRoot, rigPath, roleName)
}

// deepMergeRoleConfig merges src into dst recursively.
// For nested maps, values are merged recursively.
// For all other types, src overwrites dst.
func deepMergeRoleConfig(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		dstVal, exists := dst[key]
		if !exists {
			dst[key] = srcVal
			continue
		}

		srcMap, srcIsMap := srcVal.(map[string]interface{})
		dstMap, dstIsMap := dstVal.(map[string]interface{})
		if srcIsMap && dstIsMap {
			deepMergeRoleConfig(dstMap, srcMap)
		} else {
			dst[key] = srcVal
		}
	}
}
