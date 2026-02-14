package cmd

import (
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
