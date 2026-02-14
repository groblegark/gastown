package cmd

import (
	"testing"
)

func TestPreflightReport_EmptyIsClean(t *testing.T) {
	report := &PreflightReport{
		GitClean:      true,
		OnMainBranch:  true,
		CurrentBranch: "main",
	}

	if len(report.Warnings) != 0 {
		t.Errorf("empty report should have no warnings, got %d", len(report.Warnings))
	}
	if len(report.Errors) != 0 {
		t.Errorf("empty report should have no errors, got %d", len(report.Errors))
	}
}

func TestPreflightReport_DirtyGit(t *testing.T) {
	report := &PreflightReport{}
	checkGitState(t.TempDir(), report)

	// TempDir is not a git repo, so we expect an error
	if len(report.Errors) == 0 {
		t.Error("expected error for non-git directory")
	}
}

func TestRigHealthStatus_AllPresent(t *testing.T) {
	health := RigHealthStatus{
		Name:        "test",
		HasWitness:  true,
		HasRefinery: true,
		Polecats:    3,
		Healthy:     true,
	}

	if !health.Healthy {
		t.Error("rig with all components should be healthy")
	}
	if len(health.Issues) != 0 {
		t.Errorf("healthy rig should have no issues, got %d", len(health.Issues))
	}
}

func TestRigHealthStatus_MissingWitness(t *testing.T) {
	health := RigHealthStatus{
		Name:        "test",
		HasWitness:  false,
		HasRefinery: true,
		Polecats:    3,
		Healthy:     false,
		Issues:      []string{"no witness configured"},
	}

	if health.Healthy {
		t.Error("rig without witness should not be healthy")
	}
}

func TestAgentBeadIDToSessionName(t *testing.T) {
	tests := []struct {
		beadID string
		want   string
	}{
		{"hq-mayor", "gt-mayor"},
		{"hq-deacon", "gt-deacon"},
		{"hq-boot", "gt-boot"},
		{"some-other-format", ""},
	}

	for _, tt := range tests {
		got := agentBeadIDToSessionName(tt.beadID)
		if got != tt.want {
			t.Errorf("agentBeadIDToSessionName(%q) = %q, want %q", tt.beadID, got, tt.want)
		}
	}
}

func TestIsPolecatSession(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gt-gastown-polecat-toast", true},
		{"hq-mayor", false},
		{"gt-gastown-witness", false},
	}

	for _, tt := range tests {
		got := isPolecatSession(tt.id)
		if got != tt.want {
			t.Errorf("isPolecatSession(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestBoolIcon(t *testing.T) {
	trueIcon := boolIcon(true)
	falseIcon := boolIcon(false)

	if trueIcon == falseIcon {
		t.Error("true and false icons should be different")
	}
	if trueIcon == "" || falseIcon == "" {
		t.Error("icons should not be empty")
	}
}

func TestCheckOrphanedProcesses_DryRun(t *testing.T) {
	report := &PreflightReport{}
	checkOrphanedProcesses(report, true)

	// In dry-run, no processes should be cleaned
	if report.OrphansCleaned != 0 {
		t.Errorf("dry-run should not clean any processes, got %d", report.OrphansCleaned)
	}
}
