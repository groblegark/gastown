package cmd

// PreShutdownCheckResult holds the results of pre-shutdown verification.
type PreShutdownCheckResult struct {
	UncommittedFiles int
	UnpushedCommits  int
	StashCount       int
	OpenIssue        string // non-empty if polecat has an assigned issue that's not closed
}

// HasProblems returns true if any pre-shutdown check failed.
func (r *PreShutdownCheckResult) HasProblems() bool {
	return r.UncommittedFiles > 0 || r.UnpushedCommits > 0 || r.StashCount > 0 || r.OpenIssue != ""
}
