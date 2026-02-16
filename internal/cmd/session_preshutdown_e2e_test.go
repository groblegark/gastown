package cmd

import (
	"testing"
)

// Extended E2E tests for session pre-shutdown checks (beads-tiyd)

func TestPreShutdownCheckResult_CombinationMatrix(t *testing.T) {
	tests := []struct {
		name         string
		uncommitted  int
		unpushed     int
		stashes      int
		issue        string
		wantProblems bool
	}{
		{"all_clean", 0, 0, 0, "", false},
		{"uncommitted_only", 3, 0, 0, "", true},
		{"unpushed_only", 0, 2, 0, "", true},
		{"stash_only", 0, 0, 1, "", true},
		{"issue_only", 0, 0, 0, "gt-abc", true},
		{"uncommitted_and_unpushed", 3, 2, 0, "", true},
		{"uncommitted_and_stash", 3, 0, 1, "", true},
		{"uncommitted_and_issue", 3, 0, 0, "gt-abc", true},
		{"unpushed_and_stash", 0, 2, 1, "", true},
		{"unpushed_and_issue", 0, 2, 0, "gt-abc", true},
		{"stash_and_issue", 0, 0, 1, "gt-abc", true},
		{"all_problems", 5, 3, 2, "gt-xyz", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &PreShutdownCheckResult{
				UncommittedFiles: tt.uncommitted,
				UnpushedCommits:  tt.unpushed,
				StashCount:       tt.stashes,
				OpenIssue:        tt.issue,
			}

			if result.HasProblems() != tt.wantProblems {
				t.Errorf("HasProblems() = %v, want %v", result.HasProblems(), tt.wantProblems)
			}
		})
	}
}

func TestPreShutdownCheckResult_HighCounts(t *testing.T) {
	result := &PreShutdownCheckResult{
		UncommittedFiles: 100,
		UnpushedCommits:  50,
		StashCount:       10,
	}

	if !result.HasProblems() {
		t.Error("expected problems with high counts")
	}
}

func TestPreShutdownCheckResult_SingleFileUncommitted(t *testing.T) {
	result := &PreShutdownCheckResult{
		UncommittedFiles: 1,
	}

	if !result.HasProblems() {
		t.Error("expected problems with 1 uncommitted file")
	}
}

func TestPreShutdownCheckResult_SingleUnpushedCommit(t *testing.T) {
	result := &PreShutdownCheckResult{
		UnpushedCommits: 1,
	}

	if !result.HasProblems() {
		t.Error("expected problems with 1 unpushed commit")
	}
}

func TestPreShutdownCheckResult_IssueIDFormats(t *testing.T) {
	issueIDs := []string{"gt-abc", "beads-xyz", "hq-12345", "bd-test"}
	for _, id := range issueIDs {
		t.Run(id, func(t *testing.T) {
			result := &PreShutdownCheckResult{
				OpenIssue: id,
			}
			if !result.HasProblems() {
				t.Errorf("expected problems with issue %q", id)
			}
		})
	}
}

func TestPreShutdownCheckResult_EmptyIssueNoProblem(t *testing.T) {
	result := &PreShutdownCheckResult{
		OpenIssue: "",
	}
	if result.HasProblems() {
		t.Error("expected no problems with empty issue")
	}
}
