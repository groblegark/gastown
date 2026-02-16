package monitoring

import (
	"regexp"
	"strings"
)

// Pattern maps output text patterns to agent statuses.
type Pattern struct {
	Regex  *regexp.Regexp
	Status AgentStatus
}

// PatternRegistry holds patterns for detecting agent status from output.
type PatternRegistry struct {
	patterns []Pattern
}

// NewPatternRegistry creates a registry with the default detection patterns.
func NewPatternRegistry() *PatternRegistry {
	return &PatternRegistry{
		patterns: defaultPatterns(),
	}
}

// Detect examines output text and returns the most relevant status match.
// Returns StatusWorking if no specific pattern matches but output is present.
// Returns empty string if output is empty.
func (r *PatternRegistry) Detect(output string) AgentStatus {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	// Check patterns in priority order (first match wins)
	for _, p := range r.patterns {
		if p.Regex.MatchString(output) {
			return p.Status
		}
	}

	// Non-empty output with no specific match → working
	return StatusWorking
}

// AddPattern adds a custom detection pattern.
func (r *PatternRegistry) AddPattern(pattern string, status AgentStatus) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	r.patterns = append(r.patterns, Pattern{Regex: re, Status: status})
	return nil
}

// defaultPatterns returns the built-in status detection patterns.
// Order matters — first match wins, so more specific patterns come first.
func defaultPatterns() []Pattern {
	return []Pattern{
		// Error states (highest priority)
		{regexp.MustCompile(`(?i)(?:^|\s)(?:error|fatal|panic|crash|segfault)(?:\s|:|$)`), StatusError},
		{regexp.MustCompile(`(?i)hook error`), StatusError},

		// Blocked states
		{regexp.MustCompile(`(?i)(?:^|\s)BLOCKED:`), StatusBlocked},
		{regexp.MustCompile(`(?i)waiting for (?:approval|review|merge|response)`), StatusBlocked},
		{regexp.MustCompile(`(?i)blocked by`), StatusBlocked},

		// Waiting states
		{regexp.MustCompile(`(?i)waiting for (?:human|user|input|decision)`), StatusWaiting},
		{regexp.MustCompile(`(?i)decision point`), StatusWaiting},
		{regexp.MustCompile(`(?i)awaiting (?:response|feedback)`), StatusWaiting},

		// Thinking/processing states
		{regexp.MustCompile(`(?i)(?:^|\s)thinking\.{3}`), StatusThinking},
		{regexp.MustCompile(`(?i)analyzing|processing|computing`), StatusThinking},

		// Reviewing states
		{regexp.MustCompile(`(?i)reviewing (?:code|changes|PR|pull request|diff)`), StatusReviewing},
		{regexp.MustCompile(`(?i)code review`), StatusReviewing},
	}
}
