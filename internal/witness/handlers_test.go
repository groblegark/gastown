package witness

import (
	"testing"
	"time"
)

func TestIsCrewPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		rigName string
		want    bool
	}{
		{
			name:    "old format crew",
			path:    "gastown/decision",
			rigName: "gastown",
			want:    true,
		},
		{
			name:    "witness is not crew",
			path:    "gastown/witness",
			rigName: "gastown",
			want:    false,
		},
		{
			name:    "refinery is not crew",
			path:    "gastown/refinery",
			rigName: "gastown",
			want:    false,
		},
		{
			name:    "polecats is not crew",
			path:    "gastown/polecats",
			rigName: "gastown",
			want:    false,
		},
		{
			name:    "different rig",
			path:    "beads/decision",
			rigName: "gastown",
			want:    false,
		},
		{
			name:    "new format not matched by this func",
			path:    "gastown/crew/decision",
			rigName: "gastown",
			want:    false, // 3 parts, handled differently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCrewPath(tt.path, tt.rigName)
			if got != tt.want {
				t.Errorf("isCrewPath(%q, %q) = %v, want %v", tt.path, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestCrewSessionName(t *testing.T) {
	tests := []struct {
		name        string
		requestedBy string
		rigName     string
		want        string
	}{
		{
			name:        "new format crew",
			requestedBy: "gastown/crew/decision",
			rigName:     "gastown",
			want:        "gt-gastown-crew-decision",
		},
		{
			name:        "old format crew",
			requestedBy: "gastown/decision",
			rigName:     "gastown",
			want:        "gt-gastown-crew-decision",
		},
		{
			name:        "witness returns empty",
			requestedBy: "gastown/witness",
			rigName:     "gastown",
			want:        "",
		},
		{
			name:        "refinery returns empty",
			requestedBy: "gastown/refinery",
			rigName:     "gastown",
			want:        "",
		},
		{
			name:        "polecats returns empty",
			requestedBy: "gastown/polecats",
			rigName:     "gastown",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crewSessionName(tt.requestedBy, tt.rigName)
			if got != tt.want {
				t.Errorf("crewSessionName(%q, %q) = %q, want %q", tt.requestedBy, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestOrphanedMoleculeStruct(t *testing.T) {
	// Verify OrphanedMolecule struct can be created and populated
	mol := &OrphanedMolecule{
		MoleculeID: "gt-abc123",
		Title:      "mol-polecat-work for bd-xyz",
		CreatedAt:  "2026-02-19T01:00:00Z",
		Children:   10,
	}

	if mol.MoleculeID != "gt-abc123" {
		t.Errorf("MoleculeID = %q, want %q", mol.MoleculeID, "gt-abc123")
	}
	if mol.Children != 10 {
		t.Errorf("Children = %d, want %d", mol.Children, 10)
	}
}

func TestGracePeriodFiltering(t *testing.T) {
	// Test that the grace period logic correctly identifies old vs new molecules.
	// This tests the time parsing and comparison logic used in FindOrphanedMolecules.
	gracePeriod := 1 * time.Hour
	now := time.Now()

	tests := []struct {
		name      string
		createdAt string
		wantStale bool // true = past grace period (should be GC'd)
	}{
		{
			name:      "created 2 hours ago is stale",
			createdAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
			wantStale: true,
		},
		{
			name:      "created 30 minutes ago is fresh",
			createdAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
			wantStale: false,
		},
		{
			name:      "created 1 second ago is fresh",
			createdAt: now.Add(-1 * time.Second).Format(time.RFC3339),
			wantStale: false,
		},
		{
			name:      "created just past grace period boundary is stale",
			createdAt: now.Add(-gracePeriod - time.Minute).Format(time.RFC3339),
			wantStale: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created, err := time.Parse(time.RFC3339, tt.createdAt)
			if err != nil {
				t.Fatalf("failed to parse time: %v", err)
			}
			isStale := now.Sub(created) > gracePeriod
			if isStale != tt.wantStale {
				t.Errorf("isStale = %v, want %v (age: %v, grace: %v)",
					isStale, tt.wantStale, now.Sub(created), gracePeriod)
			}
		})
	}
}
