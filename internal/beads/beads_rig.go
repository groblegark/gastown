// Package beads provides rig identity bead management.
package beads

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RigFields contains the fields specific to rig identity beads.
type RigFields struct {
	Repo   string // Git URL for the rig's repository
	Prefix string // Beads prefix for this rig (e.g., "gt", "bd")
	State  string // Operational state: active, archived, maintenance
}

// FormatRigDescription formats the description field for a rig identity bead.
func FormatRigDescription(name string, fields *RigFields) string {
	if fields == nil {
		return ""
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Rig identity bead for %s.", name))
	lines = append(lines, "")

	if fields.Repo != "" {
		lines = append(lines, fmt.Sprintf("repo: %s", fields.Repo))
	}
	if fields.Prefix != "" {
		lines = append(lines, fmt.Sprintf("prefix: %s", fields.Prefix))
	}
	if fields.State != "" {
		lines = append(lines, fmt.Sprintf("state: %s", fields.State))
	}

	return strings.Join(lines, "\n")
}

// ParseRigFields extracts rig fields from an issue's description.
func ParseRigFields(description string) *RigFields {
	fields := &RigFields{}

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
		case "repo":
			fields.Repo = value
		case "prefix":
			fields.Prefix = value
		case "state":
			fields.State = value
		}
	}

	return fields
}

// CreateRigBead creates a rig identity bead for tracking rig metadata.
// The ID format is: <prefix>-rig-<name> (e.g., gt-rig-gastown)
// Use RigBeadID() helper to generate correct IDs.
// The created_by field is populated from BD_ACTOR env var for provenance tracking.
//
// Labels set on the bead (for controller consumption):
//   - gt:rig            — identifies this as a rig bead
//   - prefix:<X>        — beads issue prefix (e.g., "gt", "bd")
//   - git_url:<url>     — git repository URL
//   - default_branch:<br> — default branch (if known)
//   - state:<state>     — operational state (active, archived, maintenance)
func (b *Beads) CreateRigBead(id, title string, fields *RigFields) (*Issue, error) {
	description := FormatRigDescription(title, fields)

	args := []string{"create", "--json",
		"--id=" + id,
		"--title=" + title,
		"--description=" + description,
		"--labels=gt:rig",
	}

	// Add structured labels for controller consumption.
	if fields != nil {
		if fields.Prefix != "" {
			args = append(args, "--labels=prefix:"+fields.Prefix)
		}
		if fields.Repo != "" {
			args = append(args, "--labels=git_url:"+fields.Repo)
		}
		if fields.State != "" {
			args = append(args, "--labels=state:"+fields.State)
		}
	}

	if NeedsForceForID(id) {
		args = append(args, "--force")
	}

	// Default actor from BD_ACTOR env var for provenance tracking
	// Uses getActor() to respect isolated mode (tests)
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

// RigBeadIDWithPrefix generates a rig identity bead ID using the specified prefix.
// Format: <prefix>-rig-<name> (e.g., gt-rig-gastown)
func RigBeadIDWithPrefix(prefix, name string) string {
	return fmt.Sprintf("%s-rig-%s", prefix, name)
}

// RigBeadID generates a rig identity bead ID using "gt" prefix.
// For non-gastown rigs, use RigBeadIDWithPrefix with the rig's configured prefix.
func RigBeadID(name string) string {
	return RigBeadIDWithPrefix("gt", name)
}

// ListRigBeads returns all rig identity beads (type=rig, label=gt:rig).
func (b *Beads) ListRigBeads() ([]*Issue, error) {
	out, err := b.run("list", "--type=rig", "--label=gt:rig", "--json")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd list output: %w", err)
	}
	return issues, nil
}
