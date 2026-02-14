package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestPreflightReport_CleanGitRepo(t *testing.T) {
	// Create a real git repo
	dir := t.TempDir()
	setupTestGitRepo(t, dir)

	report := &PreflightReport{}
	checkGitState(dir, report)

	if len(report.Errors) != 0 {
		t.Errorf("expected no errors for clean git repo, got: %v", report.Errors)
	}
	if !report.GitClean {
		t.Error("expected git clean for fresh repo")
	}
	if report.CurrentBranch != "main" {
		t.Errorf("expected branch 'main', got %q", report.CurrentBranch)
	}
	if !report.OnMainBranch {
		t.Error("expected on main branch")
	}
}

func TestPreflightReport_DirtyGitRepo(t *testing.T) {
	dir := t.TempDir()
	setupTestGitRepo(t, dir)

	// Create uncommitted file
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted"), 0644); err != nil {
		t.Fatal(err)
	}

	report := &PreflightReport{}
	checkGitState(dir, report)

	if report.GitClean {
		t.Error("expected git NOT clean with uncommitted file")
	}
	if len(report.Warnings) == 0 {
		t.Error("expected warning about dirty git state")
	}
}

func TestPreflightReport_WrongBranch(t *testing.T) {
	dir := t.TempDir()
	setupTestGitRepo(t, dir)

	// Create and switch to feature branch
	execGit(t, dir, "checkout", "-b", "feature-branch")

	report := &PreflightReport{}
	checkGitState(dir, report)

	if report.OnMainBranch {
		t.Error("expected NOT on main branch")
	}
	if report.CurrentBranch != "feature-branch" {
		t.Errorf("expected branch 'feature-branch', got %q", report.CurrentBranch)
	}

	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "feature-branch") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected warning mentioning 'feature-branch'")
	}
}

func TestPreflightReport_JSONRoundTrip(t *testing.T) {
	report := &PreflightReport{
		GitClean:      true,
		OnMainBranch:  true,
		CurrentBranch: "main",
		StaleMailCount: 5,
		StuckWorkers:   []string{"hq-deacon"},
		RigHealth: []RigHealthStatus{
			{Name: "test", Healthy: true, HasWitness: true, HasRefinery: true, Polecats: 2},
		},
		Warnings: []string{"5 unread messages"},
		DryRun:   true,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded PreflightReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.CurrentBranch != "main" {
		t.Errorf("expected branch 'main', got %q", decoded.CurrentBranch)
	}
	if decoded.StaleMailCount != 5 {
		t.Errorf("expected 5 stale mail, got %d", decoded.StaleMailCount)
	}
	if len(decoded.StuckWorkers) != 1 || decoded.StuckWorkers[0] != "hq-deacon" {
		t.Errorf("stuck workers mismatch: %v", decoded.StuckWorkers)
	}
	if len(decoded.RigHealth) != 1 || decoded.RigHealth[0].Name != "test" {
		t.Errorf("rig health mismatch: %v", decoded.RigHealth)
	}
	if !decoded.DryRun {
		t.Error("expected dry-run flag preserved")
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

func TestRigHealthStatus_JSONRoundTrip(t *testing.T) {
	health := RigHealthStatus{
		Name:        "prod",
		Healthy:     false,
		HasWitness:  true,
		HasRefinery: false,
		Polecats:    0,
		Issues:      []string{"no refinery configured", "no polecats configured"},
	}

	data, err := json.Marshal(health)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RigHealthStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != "prod" {
		t.Errorf("expected name 'prod', got %q", decoded.Name)
	}
	if decoded.Healthy {
		t.Error("expected unhealthy")
	}
	if len(decoded.Issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(decoded.Issues))
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
		{"hq-gastown-witness", "gt-gastown-witness"},
		{"some-other-format", ""},
		{"gt-gastown-polecat-toast", ""},
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
		{"hq-polecat-test", true},
		{"hq-mayor", false},
		{"gt-gastown-witness", false},
		{"gt-gastown-refinery", false},
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

func TestCheckGitState_MasterBranch(t *testing.T) {
	dir := t.TempDir()

	// Init git repo with "master" default branch
	execGit(t, dir, "init", "-b", "master")
	execGit(t, dir, "config", "user.email", "test@test.com")
	execGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, dir, "add", ".")
	execGit(t, dir, "commit", "-m", "init")

	report := &PreflightReport{}
	checkGitState(dir, report)

	// "master" should also be treated as main branch
	if !report.OnMainBranch {
		t.Error("expected 'master' to be recognized as main branch")
	}
	if report.CurrentBranch != "master" {
		t.Errorf("expected branch 'master', got %q", report.CurrentBranch)
	}
}

func TestSyncBeads_MissingBd(t *testing.T) {
	// Override PATH to not include bd
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // empty dir, no bd binary

	report := &PreflightReport{}
	syncBeads(t.TempDir(), report)

	// Restore for other tests
	_ = os.Setenv("PATH", origPath)

	// Should warn, not error
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "bd not found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'bd not found' warning, got warnings: %v", report.Warnings)
	}
}

// setupTestGitRepo creates a minimal git repo at dir with an initial commit on 'main'.
func setupTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	execGit(t, dir, "init", "-b", "main")
	execGit(t, dir, "config", "user.email", "test@test.com")
	execGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, dir, "add", ".")
	execGit(t, dir, "commit", "-m", "initial commit")
}

// execGit runs a git command in the given directory.
func execGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
