package cmd

import (
	"testing"
)

func TestPreShutdownCheckResult_NoProblems(t *testing.T) {
	result := &PreShutdownCheckResult{}
	if result.HasProblems() {
		t.Error("expected no problems for empty result")
	}
}

func TestPreShutdownCheckResult_UncommittedFiles(t *testing.T) {
	result := &PreShutdownCheckResult{
		UncommittedFiles: 3,
	}
	if !result.HasProblems() {
		t.Error("expected problems when uncommitted files > 0")
	}
}

func TestPreShutdownCheckResult_UnpushedCommits(t *testing.T) {
	result := &PreShutdownCheckResult{
		UnpushedCommits: 2,
	}
	if !result.HasProblems() {
		t.Error("expected problems when unpushed commits > 0")
	}
}

func TestPreShutdownCheckResult_StashCount(t *testing.T) {
	result := &PreShutdownCheckResult{
		StashCount: 1,
	}
	if !result.HasProblems() {
		t.Error("expected problems when stash count > 0")
	}
}

func TestPreShutdownCheckResult_OpenIssue(t *testing.T) {
	result := &PreShutdownCheckResult{
		OpenIssue: "gt-abc",
	}
	if !result.HasProblems() {
		t.Error("expected problems when open issue exists")
	}
}

func TestPreShutdownCheckResult_MultipleProblems(t *testing.T) {
	result := &PreShutdownCheckResult{
		UncommittedFiles: 5,
		UnpushedCommits:  1,
		OpenIssue:        "gt-xyz",
	}
	if !result.HasProblems() {
		t.Error("expected problems with multiple issues")
	}
}

func TestPreShutdownCheckResult_ZeroValues(t *testing.T) {
	// Explicitly zero values should not trigger problems
	result := &PreShutdownCheckResult{
		UncommittedFiles: 0,
		UnpushedCommits:  0,
		StashCount:       0,
		OpenIssue:        "",
	}
	if result.HasProblems() {
		t.Error("expected no problems with all zero values")
	}
}
