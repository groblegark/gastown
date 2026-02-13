// Package configbeads provides functions for loading configuration from beads.
// This file handles rig registry config bead operations.
package configbeads

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// SeedRigRegistryBead creates a rig identity bead (type=rig) when a rig is added.
// Uses the same format as `gt rig register`: type=rig with gt:rig label and
// structured description containing repo, prefix, and state fields.
//
// Falls back to legacy config bead creation if rig bead creation fails,
// for backwards compatibility during migration.
func SeedRigRegistryBead(townRoot string, townName, rigName string, entry config.RigEntry) error {
	bd := beads.New(townRoot)

	// Check if a rig bead already exists (by title match via list).
	rigBeads, err := bd.ListRigBeads()
	if err == nil {
		for _, issue := range rigBeads {
			if issue.Title == rigName {
				return nil // Already seeded
			}
		}
	}

	// Build rig bead fields.
	prefix := ""
	if entry.BeadsConfig != nil {
		prefix = entry.BeadsConfig.Prefix
	}
	rigFields := &beads.RigFields{
		Repo:   entry.GitURL,
		Prefix: prefix,
		State:  "active",
	}

	// Use hq- prefix so it lives in the town beads, not a rig-specific DB.
	id := fmt.Sprintf("hq-%s-rig-%s", prefix, rigName)
	_, err = bd.CreateRigBead(id, rigName, rigFields)
	return err
}

// DeleteRigRegistryBead removes the rig identity bead when a rig is removed.
// Tries both new rig bead format and legacy config bead format.
func DeleteRigRegistryBead(townRoot, townName, rigName string) error {
	bd := beads.New(townRoot)

	// Try deleting rig identity bead (new format).
	rigBeads, err := bd.ListRigBeads()
	if err == nil {
		for _, issue := range rigBeads {
			if issue.Title == rigName {
				_ = bd.Close(issue.ID)
				return nil
			}
		}
	}

	// Fall back to legacy config bead deletion.
	slug := "rig-" + townName + "-" + rigName
	id := beads.ConfigBeadID(slug)
	existing, _, err := bd.GetConfigBead(id)
	if err != nil || existing == nil {
		return nil // Nothing to delete
	}
	return bd.DeleteConfigBead(id)
}

// SeedAccountBead creates a config bead for an account (excluding auth_token).
// The bead ID is hq-cfg-account-<handle>.
// Secrets (auth_token) are intentionally excluded from beads.
func SeedAccountBead(townRoot string, handle string, acct config.Account) error {
	bd := beads.New(townRoot)

	slug := "account-" + handle

	// Check if already exists
	existing, _, err := bd.GetConfigBeadBySlug(slug)
	if err == nil && existing != nil {
		return nil // Already seeded
	}

	metadata := map[string]interface{}{
		"handle":     handle,
		"config_dir": acct.ConfigDir,
		"created_at": time.Now().Format(time.RFC3339),
	}
	if acct.Email != "" {
		metadata["email"] = acct.Email
	}
	if acct.Description != "" {
		metadata["description"] = acct.Description
	}
	if acct.BaseURL != "" {
		metadata["base_url"] = acct.BaseURL
	}
	// NOTE: auth_token is intentionally excluded - secrets never go in beads

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling account metadata: %w", err)
	}

	fields := &beads.ConfigFields{
		Rig:      "*", // Global scope - accounts are cross-rig
		Category: beads.ConfigCategoryAccounts,
		Metadata: string(metadataJSON),
	}

	_, err = bd.CreateConfigBead(slug, fields, "", "")
	return err
}
