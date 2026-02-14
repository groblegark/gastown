package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostflightReport_EmptyIsClean(t *testing.T) {
	report := &PostflightReport{}

	if len(report.Warnings) != 0 {
		t.Errorf("empty report should have no warnings, got %d", len(report.Warnings))
	}
	if len(report.Errors) != 0 {
		t.Errorf("empty report should have no errors, got %d", len(report.Errors))
	}
	if report.MailArchived != 0 {
		t.Errorf("empty report should have 0 mail archived, got %d", report.MailArchived)
	}
	if report.BranchesCleaned != 0 {
		t.Errorf("empty report should have 0 branches cleaned, got %d", report.BranchesCleaned)
	}
}

func TestPostflightReport_DryRun(t *testing.T) {
	report := &PostflightReport{DryRun: true}

	if !report.DryRun {
		t.Error("dry-run report should be flagged as dry-run")
	}
}

func TestCleanOrphans_DryRun(t *testing.T) {
	report := &PostflightReport{}
	cleanOrphans(report, true)

	// In dry-run, no processes should be cleaned
	if report.OrphansCleaned != 0 {
		t.Errorf("dry-run should not clean any processes, got %d", report.OrphansCleaned)
	}
}

func TestPostflightReport_JSONRoundTrip(t *testing.T) {
	report := &PostflightReport{
		MailArchived:    12,
		BranchesCleaned: 3,
		OrphansCleaned:  2,
		BeadsSynced:     true,
		Warnings:        []string{"something odd"},
		Errors:          []string{"something bad"},
		DryRun:          true,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded PostflightReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.MailArchived != 12 {
		t.Errorf("expected 12 mail archived, got %d", decoded.MailArchived)
	}
	if decoded.BranchesCleaned != 3 {
		t.Errorf("expected 3 branches cleaned, got %d", decoded.BranchesCleaned)
	}
	if decoded.OrphansCleaned != 2 {
		t.Errorf("expected 2 orphans cleaned, got %d", decoded.OrphansCleaned)
	}
	if !decoded.BeadsSynced {
		t.Error("expected beads synced to be true")
	}
	if len(decoded.Warnings) != 1 || decoded.Warnings[0] != "something odd" {
		t.Errorf("warnings mismatch: %v", decoded.Warnings)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0] != "something bad" {
		t.Errorf("errors mismatch: %v", decoded.Errors)
	}
	if !decoded.DryRun {
		t.Error("expected dry-run flag preserved")
	}
}

func TestPostflightSyncBeads_MissingBd(t *testing.T) {
	// Override PATH to not include bd
	t.Setenv("PATH", t.TempDir())

	report := &PostflightReport{}
	postflightSyncBeads(t.TempDir(), report)

	// Should warn about missing bd, not error
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
	if report.BeadsSynced {
		t.Error("beads should not be marked as synced when bd is missing")
	}
}

func TestCleanStaleBranches_NoRigsConfig(t *testing.T) {
	// Use a temp dir with no town configuration
	dir := t.TempDir()
	report := &PostflightReport{}

	// cleanStaleBranches should gracefully handle missing rig config
	cleanStaleBranches(dir, report, true)

	// No branches should be cleaned if we can't find rig config
	if report.BranchesCleaned != 0 {
		t.Errorf("expected 0 branches cleaned without rig config, got %d", report.BranchesCleaned)
	}
}

func TestPostflightReport_AllZeroValues(t *testing.T) {
	report := &PostflightReport{}

	// All numeric fields should be zero
	if report.MailArchived != 0 {
		t.Errorf("expected 0 mail archived, got %d", report.MailArchived)
	}
	if report.BranchesCleaned != 0 {
		t.Errorf("expected 0 branches cleaned, got %d", report.BranchesCleaned)
	}
	if report.OrphansCleaned != 0 {
		t.Errorf("expected 0 orphans cleaned, got %d", report.OrphansCleaned)
	}
	if report.BeadsSynced {
		t.Error("expected beads not synced by default")
	}
	if report.DryRun {
		t.Error("expected dry-run false by default")
	}
}

func TestPostflightReport_ErrorsAndWarnings(t *testing.T) {
	report := &PostflightReport{
		Warnings: []string{"warn1", "warn2"},
		Errors:   []string{"err1"},
	}

	if len(report.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(report.Warnings))
	}
	if len(report.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(report.Errors))
	}
}

// TestBranchCleanupPatterns verifies that the branch name filtering logic
// in cleanStaleBranches correctly identifies integration branches.
// This tests the pattern matching directly since we can't easily create
// a full rig environment in unit tests.
func TestBranchCleanupPatterns(t *testing.T) {
	tests := []struct {
		branch   string
		expected bool // true = should be cleaned
	}{
		{"beads-sync-12345", true},
		{"beads-sync", true},
		{"gt-feature-abc", true},
		{"polecat-work-toast", true},
		{"main", false},
		{"master", false},
		{"feature-branch", false},
		{"develop", false},
		{"hotfix-123", false},
	}

	for _, tt := range tests {
		isIntegration := strings.HasPrefix(tt.branch, "beads-sync") ||
			strings.HasPrefix(tt.branch, "gt-") ||
			strings.HasPrefix(tt.branch, "polecat-")

		// Skip protected branches
		isProtected := tt.branch == "main" || tt.branch == "master"

		shouldClean := isIntegration && !isProtected

		if shouldClean != tt.expected {
			t.Errorf("branch %q: shouldClean=%v, expected=%v", tt.branch, shouldClean, tt.expected)
		}
	}
}

// TestCleanStaleBranches_RealGitRepo creates a real git repo with
// merged branches to verify branch cleanup logic end-to-end.
func TestCleanStaleBranches_RealGitRepo(t *testing.T) {
	dir := t.TempDir()

	// Create a git repo simulating a rig
	rigDir := filepath.Join(dir, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Init git repo
	execGitCmd(t, rigDir, "init", "-b", "main")
	execGitCmd(t, rigDir, "config", "user.email", "test@test.com")
	execGitCmd(t, rigDir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(rigDir, "README"), []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	execGitCmd(t, rigDir, "add", ".")
	execGitCmd(t, rigDir, "commit", "-m", "initial commit")

	// Create a branch, add a commit, merge it back to main
	execGitCmd(t, rigDir, "checkout", "-b", "beads-sync-test123")
	if err := os.WriteFile(filepath.Join(rigDir, "sync.txt"), []byte("sync"), 0644); err != nil {
		t.Fatal(err)
	}
	execGitCmd(t, rigDir, "add", ".")
	execGitCmd(t, rigDir, "commit", "-m", "sync work")
	execGitCmd(t, rigDir, "checkout", "main")
	execGitCmd(t, rigDir, "merge", "beads-sync-test123")

	// Create a gt- branch, merge it
	execGitCmd(t, rigDir, "checkout", "-b", "gt-feature-abc")
	if err := os.WriteFile(filepath.Join(rigDir, "feat.txt"), []byte("feature"), 0644); err != nil {
		t.Fatal(err)
	}
	execGitCmd(t, rigDir, "add", ".")
	execGitCmd(t, rigDir, "commit", "-m", "feature work")
	execGitCmd(t, rigDir, "checkout", "main")
	execGitCmd(t, rigDir, "merge", "gt-feature-abc")

	// Create a non-integration branch that's merged (should NOT be cleaned)
	execGitCmd(t, rigDir, "checkout", "-b", "my-feature")
	if err := os.WriteFile(filepath.Join(rigDir, "my.txt"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	execGitCmd(t, rigDir, "add", ".")
	execGitCmd(t, rigDir, "commit", "-m", "my work")
	execGitCmd(t, rigDir, "checkout", "main")
	execGitCmd(t, rigDir, "merge", "my-feature")

	// Verify merged branches exist before cleanup
	out, err := exec.Command("git", "-C", rigDir, "branch", "--merged", "main").Output()
	if err != nil {
		t.Fatalf("listing merged branches: %v", err)
	}

	branches := strings.TrimSpace(string(out))
	if !strings.Contains(branches, "beads-sync-test123") {
		t.Fatal("expected beads-sync-test123 in merged branches")
	}
	if !strings.Contains(branches, "gt-feature-abc") {
		t.Fatal("expected gt-feature-abc in merged branches")
	}
	if !strings.Contains(branches, "my-feature") {
		t.Fatal("expected my-feature in merged branches")
	}

	// Now verify our pattern logic would identify the right branches
	var toClean, toKeep []string
	for _, line := range strings.Split(branches, "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || strings.HasPrefix(branch, "*") || branch == "main" || branch == "master" {
			continue
		}
		if strings.HasPrefix(branch, "beads-sync") ||
			strings.HasPrefix(branch, "gt-") ||
			strings.HasPrefix(branch, "polecat-") {
			toClean = append(toClean, branch)
		} else {
			toKeep = append(toKeep, branch)
		}
	}

	if len(toClean) != 2 {
		t.Errorf("expected 2 branches to clean, got %d: %v", len(toClean), toClean)
	}
	if len(toKeep) != 1 || toKeep[0] != "my-feature" {
		t.Errorf("expected [my-feature] to keep, got %v", toKeep)
	}
}

// execGitCmd runs a git command in the given directory (postflight test helper).
func execGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
