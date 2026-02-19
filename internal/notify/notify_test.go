package notify

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over length", "hello world", 8, "hello..."},
		{"very short max", "hello", 3, "hel"},
		{"max 2", "hello", 2, "he"},
		{"max 1", "hello", 1, "h"},
		{"max 0", "hello", 0, ""},
		{"empty string", "", 10, ""},
		{"unicode", "héllo wörld", 8, "héll..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFormatResolutionBody(t *testing.T) {
	t.Run("with rationale", func(t *testing.T) {
		body := formatResolutionBody("bd-123", "What should we do?", "Option A", "best approach", "human")
		if !strings.Contains(body, "Decision ID: bd-123") {
			t.Errorf("body should contain decision ID")
		}
		if !strings.Contains(body, "Question: What should we do?") {
			t.Errorf("body should contain question")
		}
		if !strings.Contains(body, "Chosen: Option A") {
			t.Errorf("body should contain chosen option")
		}
		if !strings.Contains(body, "Rationale: best approach") {
			t.Errorf("body should contain rationale")
		}
		if !strings.Contains(body, "Resolved by: human") {
			t.Errorf("body should contain resolved by")
		}
		if !strings.Contains(body, "This decision has been resolved") {
			t.Errorf("body should contain resolution notice")
		}
	})

	t.Run("without rationale", func(t *testing.T) {
		body := formatResolutionBody("bd-456", "Pick a color", "Blue", "", "slack:user")
		if strings.Contains(body, "Rationale:") {
			t.Errorf("body should not contain rationale line when empty")
		}
		if !strings.Contains(body, "Chosen: Blue") {
			t.Errorf("body should contain chosen option")
		}
	})
}
