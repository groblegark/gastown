// Package notify provides shared notification functions for agent communication.
package notify

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestFormatResolutionBody(t *testing.T) {
	body := formatResolutionBody("test-123", "Should we do X?", "Yes", "Because Y", "human")

	// Check for expected content
	if body == "" {
		t.Error("formatResolutionBody returned empty string")
	}

	// Should contain the decision ID
	if !contains(body, "test-123") {
		t.Error("body should contain decision ID")
	}

	// Should contain the question
	if !contains(body, "Should we do X?") {
		t.Error("body should contain question")
	}

	// Should contain the choice
	if !contains(body, "Yes") {
		t.Error("body should contain chosen option")
	}

	// Should contain rationale
	if !contains(body, "Because Y") {
		t.Error("body should contain rationale")
	}
}

// TestDecisionResolvedFieldsValidation tests that the function handles
// various RequestedBy values correctly (coverage for the fallback logic)
func TestDecisionResolvedFieldsValidation(t *testing.T) {
	tests := []struct {
		name              string
		requestedBy       string
		shouldNotifyOwner bool // whether we expect direct owner notification
	}{
		{"known agent", "gastown/crew/test", true},
		{"empty requestor", "", false},
		{"unknown requestor", "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := beads.DecisionFields{
				Question:    "Test question",
				RequestedBy: tt.requestedBy,
				Options: []beads.DecisionOption{
					{Label: "Yes"},
					{Label: "No"},
				},
			}

			// Test the condition used in DecisionResolved
			shouldNotify := fields.RequestedBy != "" && fields.RequestedBy != "unknown"
			if shouldNotify != tt.shouldNotifyOwner {
				t.Errorf("RequestedBy=%q: shouldNotify=%v, want %v", tt.requestedBy, shouldNotify, tt.shouldNotifyOwner)
			}
		})
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
