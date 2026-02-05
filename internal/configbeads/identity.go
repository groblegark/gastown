// Package configbeads provides functions for loading configuration from beads.
// This package bridges internal/config and internal/beads to avoid circular imports.
package configbeads

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/workspace"
)

// LoadTownConfigFromBeads loads the TownConfig from a config bead.
// Returns nil, nil if the bead does not exist.
func LoadTownConfigFromBeads(bd *beads.Beads, townName string) (*config.TownConfig, error) {
	slug := "town-" + townName
	issue, fields, err := bd.GetConfigBeadBySlug(slug)
	if err != nil {
		return nil, fmt.Errorf("getting config bead: %w", err)
	}
	if issue == nil || fields == nil {
		return nil, nil
	}

	var tc config.TownConfig
	if err := json.Unmarshal([]byte(fields.Metadata), &tc); err != nil {
		return nil, fmt.Errorf("parsing config bead metadata: %w", err)
	}

	return &tc, nil
}

// LoadTownIdentity loads town identity, trying beads first with filesystem fallback.
func LoadTownIdentity(townRoot string) (*config.TownConfig, error) {
	// Derive town name from filesystem config first (needed for bead lookup)
	fsPath := filepath.Join(townRoot, workspace.PrimaryMarker)
	fsTc, fsErr := config.LoadTownConfig(fsPath)

	// If we can read the filesystem config, try beads with that town name
	if fsErr == nil && fsTc != nil {
		bd := beads.New(townRoot)
		tc, err := LoadTownConfigFromBeads(bd, fsTc.Name)
		if err == nil && tc != nil {
			return tc, nil
		}
		// Bead lookup failed or not found - fall back to filesystem
	}

	// Fallback to filesystem
	if fsErr != nil {
		return nil, fsErr
	}
	return fsTc, nil
}
