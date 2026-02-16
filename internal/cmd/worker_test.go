package cmd

import (
	"encoding/json"
	"testing"
	"time"
)

func TestWorkerStatusReport_JSON(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "beads-abc",
		Status:     "progress",
		Progress:   42,
		Reason:     "",
		Message:    "halfway there",
		ReportedAt: time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed WorkerStatusReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.IssueID != "beads-abc" {
		t.Errorf("IssueID = %q, want %q", parsed.IssueID, "beads-abc")
	}
	if parsed.Status != "progress" {
		t.Errorf("Status = %q, want %q", parsed.Status, "progress")
	}
	if parsed.Progress != 42 {
		t.Errorf("Progress = %d, want %d", parsed.Progress, 42)
	}
	if parsed.Message != "halfway there" {
		t.Errorf("Message = %q, want %q", parsed.Message, "halfway there")
	}
}

func TestWorkerStatusReport_OmitsEmpty(t *testing.T) {
	report := WorkerStatusReport{
		IssueID:    "gt-xyz",
		Status:     "started",
		ReportedAt: time.Now(),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Verify omitempty fields are absent
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["progress"]; ok {
		t.Error("expected progress to be omitted when 0")
	}
	if _, ok := raw["reason"]; ok {
		t.Error("expected reason to be omitted when empty")
	}
	if _, ok := raw["message"]; ok {
		t.Error("expected message to be omitted when empty")
	}

	// Required fields should be present
	if _, ok := raw["issue_id"]; !ok {
		t.Error("expected issue_id to be present")
	}
	if _, ok := raw["status"]; !ok {
		t.Error("expected status to be present")
	}
}

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		status   string
		progress int
		reason   string
		want     string
	}{
		{"started", 0, "", "work started"},
		{"progress", 75, "", "75% complete"},
		{"progress", 0, "", "0% complete"},
		{"progress", 100, "", "100% complete"},
		{"blocked", 0, "waiting for API key", "waiting for API key"},
		{"completed", 100, "", "done"},
		{"failed", 0, "build error", "build error"},
		{"unknown", 0, "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := statusLabel(tt.status, tt.progress, tt.reason)
			if got != tt.want {
				t.Errorf("statusLabel(%q, %d, %q) = %q, want %q",
					tt.status, tt.progress, tt.reason, got, tt.want)
			}
		})
	}
}

func TestWorkerStatusReport_AllStatuses(t *testing.T) {
	statuses := []string{"started", "progress", "blocked", "completed", "failed"}
	for _, s := range statuses {
		t.Run(s, func(t *testing.T) {
			report := WorkerStatusReport{
				IssueID:    "beads-test",
				Status:     s,
				ReportedAt: time.Now(),
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("Marshal error for status %q: %v", s, err)
			}
			var parsed WorkerStatusReport
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Unmarshal error for status %q: %v", s, err)
			}
			if parsed.Status != s {
				t.Errorf("Status round-trip: got %q, want %q", parsed.Status, s)
			}
		})
	}
}
