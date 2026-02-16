package cmd

import (
	"encoding/json"
	"testing"
	"time"
)

// Tests for gt worker status commands (beads-oipg)

func TestWorkerStatusReport_FullRoundtrip(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "beads-xyz",
		Status:     "progress",
		Progress:   75,
		Reason:     "building",
		Message:    "compiling module A",
		ReportedAt: time.Date(2026, 2, 15, 14, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed WorkerStatusReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.IssueID != report.IssueID {
		t.Errorf("IssueID = %q, want %q", parsed.IssueID, report.IssueID)
	}
	if parsed.Status != report.Status {
		t.Errorf("Status = %q, want %q", parsed.Status, report.Status)
	}
	if parsed.Progress != report.Progress {
		t.Errorf("Progress = %d, want %d", parsed.Progress, report.Progress)
	}
	if parsed.Reason != report.Reason {
		t.Errorf("Reason = %q, want %q", parsed.Reason, report.Reason)
	}
	if parsed.Message != report.Message {
		t.Errorf("Message = %q, want %q", parsed.Message, report.Message)
	}
}

func TestWorkerStatusReport_JSONFieldNames(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "gt-test",
		Status:     "started",
		Progress:   50,
		Reason:     "reason",
		Message:    "msg",
		ReportedAt: time.Now(),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	expectedFields := []string{"issue_id", "status", "progress", "reason", "message", "reported_at"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected JSON field %q to be present", field)
		}
	}
}

func TestStatusLabel_Comprehensive(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		progress int
		reason   string
		want     string
	}{
		{"started_no_progress", "started", 0, "", "work started"},
		{"started_with_progress", "started", 50, "", "work started"},
		{"progress_zero", "progress", 0, "", "0% complete"},
		{"progress_half", "progress", 50, "", "50% complete"},
		{"progress_full", "progress", 100, "", "100% complete"},
		{"progress_with_reason", "progress", 75, "building", "75% complete"},
		{"blocked_with_reason", "blocked", 0, "waiting for review", "waiting for review"},
		{"blocked_empty_reason", "blocked", 0, "", ""},
		{"completed_full", "completed", 100, "", "done"},
		{"completed_zero", "completed", 0, "", "done"},
		{"failed_with_reason", "failed", 0, "compilation error", "compilation error"},
		{"failed_empty_reason", "failed", 0, "", ""},
		{"unknown_status", "unknown", 0, "", "unknown"},
		{"empty_status", "", 0, "", ""},
		{"custom_status", "custom", 0, "", "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusLabel(tt.status, tt.progress, tt.reason)
			if got != tt.want {
				t.Errorf("statusLabel(%q, %d, %q) = %q, want %q",
					tt.status, tt.progress, tt.reason, got, tt.want)
			}
		})
	}
}

func TestWorkerStatusReport_BlockedWithReason(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "gt-blocked",
		Status:     "blocked",
		Reason:     "waiting for API key",
		ReportedAt: time.Now(),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if reason, ok := raw["reason"]; !ok {
		t.Error("expected reason to be present for blocked status")
	} else if reason != "waiting for API key" {
		t.Errorf("reason = %q, want %q", reason, "waiting for API key")
	}

	if _, ok := raw["progress"]; ok {
		t.Error("expected progress to be omitted when 0")
	}
}

func TestWorkerStatusReport_FailedWithReason(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "gt-failed",
		Status:     "failed",
		Reason:     "build error in main.go",
		Message:    "exit code 1",
		ReportedAt: time.Now(),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed WorkerStatusReport
	json.Unmarshal(data, &parsed)

	if parsed.Status != "failed" {
		t.Errorf("Status = %q, want %q", parsed.Status, "failed")
	}
	if parsed.Reason != "build error in main.go" {
		t.Errorf("Reason = %q, want %q", parsed.Reason, "build error in main.go")
	}
	if parsed.Message != "exit code 1" {
		t.Errorf("Message = %q, want %q", parsed.Message, "exit code 1")
	}
}

func TestWorkerStatusReport_ProgressRange(t *testing.T) {
	values := []int{0, 1, 25, 50, 75, 99, 100}
	for _, v := range values {
		report := WorkerStatusReport{
			IssueID:    "gt-range",
			Status:     "progress",
			Progress:   v,
			ReportedAt: time.Now(),
		}

		data, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("Marshal error for progress=%d: %v", v, err)
		}

		var parsed WorkerStatusReport
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("Unmarshal error for progress=%d: %v", v, err)
		}

		if parsed.Progress != v {
			t.Errorf("Progress round-trip: got %d, want %d", parsed.Progress, v)
		}
	}
}

func TestWorkerStatusReport_TimestampPreserved(t *testing.T) {
	ts := time.Date(2026, 2, 15, 10, 30, 45, 0, time.UTC)
	report := WorkerStatusReport{
		IssueID:    "gt-ts",
		Status:     "started",
		ReportedAt: ts,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed WorkerStatusReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if !parsed.ReportedAt.Equal(ts) {
		t.Errorf("ReportedAt = %v, want %v", parsed.ReportedAt, ts)
	}
}
