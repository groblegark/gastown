package beads

import (
	"reflect"
	"testing"
)

func TestExtractBeadIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		// Empty and nil cases
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "no bead IDs",
			input:    "This is just some plain text without any IDs",
			expected: nil,
		},

		// Hash-based IDs
		{
			name:     "simple hash ID",
			input:    "Working on gt-abc123",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "multiple hash IDs",
			input:    "Issues gt-abc123 and bd-xyz789 are related",
			expected: []string{"gt-abc123", "bd-xyz789"},
		},
		{
			name:     "hq prefix hash ID",
			input:    "Town bead hq-39bc13 needs review",
			expected: []string{"hq-39bc13"},
		},

		// Semantic IDs
		{
			name:     "epic semantic ID",
			input:    "Parent epic gt-epc-semantic7x9k",
			expected: []string{"gt-epc-semantic7x9k"},
		},
		{
			name:     "molecule semantic ID",
			input:    "Attached to gt-mol-abc123",
			expected: []string{"gt-mol-abc123"},
		},
		{
			name:     "merge request ID",
			input:    "Created MR gt-mr-def456",
			expected: []string{"gt-mr-def456"},
		},
		{
			name:     "bug semantic ID",
			input:    "Filed bd-bug-fixme123",
			expected: []string{"bd-bug-fixme123"},
		},

		// Hierarchical IDs
		{
			name:     "single level hierarchy",
			input:    "Subtask gt-abc.1",
			expected: []string{"gt-abc.1"},
		},
		{
			name:     "two level hierarchy",
			input:    "Sub-subtask gt-abc.1.2",
			expected: []string{"gt-abc.1.2"},
		},
		{
			name:     "hierarchical with gt prefix",
			input:    "Task gt-qtsup.16 is ready",
			expected: []string{"gt-qtsup.16"},
		},
		{
			name:     "multiple hierarchical levels",
			input:    "Working on gt-abc.1.2.3",
			expected: []string{"gt-abc.1.2.3"},
		},

		// Agent IDs
		{
			name:     "mayor agent ID",
			input:    "Mayor bead is hq-mayor",
			expected: []string{"hq-mayor"},
		},
		{
			name:     "deacon agent ID",
			input:    "Deacon at gt-deacon",
			expected: []string{"gt-deacon"},
		},
		{
			name:     "witness agent ID",
			input:    "Witness gt-gastown-witness is running",
			expected: []string{"gt-gastown-witness"},
		},
		{
			name:     "crew agent ID",
			input:    "Crew member gt-gastown-crew-max is active",
			expected: []string{"gt-gastown-crew-max"},
		},
		{
			name:     "polecat agent ID",
			input:    "Polecat bd-beads-polecat-pearl completed",
			expected: []string{"bd-beads-polecat-pearl"},
		},
		{
			name:     "dog agent ID",
			input:    "Dog hq-dog-alpha is watching",
			expected: []string{"hq-dog-alpha"},
		},

		// Mixed types
		{
			name:     "mixed ID types",
			input:    "Working on gt-abc123 which blocks gt-abc.1 and relates to hq-mayor",
			expected: []string{"gt-abc123", "gt-abc.1", "hq-mayor"},
		},

		// Deduplication
		{
			name:     "duplicate IDs removed",
			input:    "Issue gt-abc123 is mentioned again: gt-abc123",
			expected: []string{"gt-abc123"},
		},

		// Case handling
		{
			name:     "uppercase converted to lowercase",
			input:    "Working on GT-ABC123",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "mixed case converted",
			input:    "Issues Gt-Abc123 and BD-XYZ789",
			expected: []string{"gt-abc123", "bd-xyz789"},
		},

		// Edge cases - IDs with underscores
		{
			name:     "ID with underscore",
			input:    "Task gt-my_task-abc is ready",
			expected: []string{"gt-my_task-abc"},
		},

		// Boundary detection
		{
			name:     "ID at start of string",
			input:    "gt-abc123 is the issue",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "ID at end of string",
			input:    "The issue is gt-abc123",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "ID surrounded by punctuation",
			input:    "(gt-abc123) is blocked",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "ID in markdown link",
			input:    "See [gt-abc123](http://example.com)",
			expected: []string{"gt-abc123"},
		},

		// Things that should NOT match
		{
			name:     "single char prefix not matched",
			input:    "x-notabead",
			expected: nil,
		},
		{
			name:     "four char prefix not matched",
			input:    "abcd-notabead",
			expected: nil,
		},
		{
			name:     "prefix only not matched",
			input:    "just gt- alone",
			expected: nil,
		},
		{
			name:     "unknown prefix not matched",
			input:    "Task ap-qtsup.16 has unknown prefix",
			expected: nil,
		},
		{
			name:     "english words not matched",
			input:    "sub-subtask and pre-process are not beads",
			expected: nil,
		},

		// Real-world examples
		{
			name:     "decision prompt text",
			input:    "How should we handle gt-vhsnvd? It blocks gt-abc.1 and gt-abc.2",
			expected: []string{"gt-vhsnvd", "gt-abc.1", "gt-abc.2"},
		},
		{
			name:     "error message with bead IDs",
			input:    "Error processing bead bd-xyz789: parent gt-epc-main not found",
			expected: []string{"bd-xyz789", "gt-epc-main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBeadIDs(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractBeadIDs(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractBeadIDsFromJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		// Empty and invalid
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "invalid JSON treated as text",
			input:    "not json but has gt-abc123",
			expected: []string{"gt-abc123"},
		},

		// Simple JSON
		{
			name:     "string value with bead ID",
			input:    `{"issue": "gt-abc123"}`,
			expected: []string{"gt-abc123"},
		},
		{
			name:     "multiple string values",
			input:    `{"parent": "gt-abc123", "child": "gt-abc.1"}`,
			expected: []string{"gt-abc.1", "gt-abc123"}, // sorted by key order: child < parent
		},

		// Nested JSON
		{
			name:     "nested object",
			input:    `{"data": {"issue_id": "bd-xyz789"}}`,
			expected: []string{"bd-xyz789"},
		},
		{
			name:     "array values",
			input:    `{"blockers": ["gt-abc123", "gt-def456"]}`,
			expected: []string{"gt-abc123", "gt-def456"},
		},
		{
			name:     "deeply nested",
			input:    `{"level1": {"level2": {"level3": {"id": "hq-deep-nested"}}}}`,
			expected: []string{"hq-deep-nested"},
		},

		// Mixed content
		{
			name:     "mixed nested and array",
			input:    `{"parent": "gt-parent", "children": [{"id": "gt-child.1"}, {"id": "gt-child.2"}]}`,
			expected: []string{"gt-child.1", "gt-child.2", "gt-parent"},
		},

		// Keys containing bead IDs
		{
			name:     "bead ID in key",
			input:    `{"gt-abc123": "some value"}`,
			expected: []string{"gt-abc123"},
		},

		// Multiple IDs in single value
		{
			name:     "multiple IDs in one value",
			input:    `{"description": "This relates to gt-abc123 and gt-def456"}`,
			expected: []string{"gt-abc123", "gt-def456"},
		},

		// Real-world context JSON - order based on alphabetical key processing
		{
			name: "decision context",
			input: `{
				"type": "bug_fix",
				"affected_bead": "gt-vhsnvd",
				"parent_epic": "gt-epc-main",
				"blockers": ["gt-block.1", "gt-block.2"],
				"description": "Fix for hq-mayor agent"
			}`,
			expected: []string{"gt-vhsnvd", "gt-block.1", "gt-block.2", "hq-mayor", "gt-epc-main"},
		},

		// Deduplication across values
		{
			name:     "duplicate across values",
			input:    `{"a": "gt-abc123", "b": "gt-abc123", "c": "gt-abc123"}`,
			expected: []string{"gt-abc123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBeadIDsFromJSON(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractBeadIDsFromJSON(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractBeadIDsFromAll(t *testing.T) {
	tests := []struct {
		name     string
		inputs   []string
		expected []string
	}{
		{
			name:     "empty inputs",
			inputs:   []string{},
			expected: nil,
		},
		{
			name:     "single text input",
			inputs:   []string{"Working on gt-abc123"},
			expected: []string{"gt-abc123"},
		},
		{
			name:     "multiple text inputs",
			inputs:   []string{"Issue gt-abc123", "Blocks gt-def456"},
			expected: []string{"gt-abc123", "gt-def456"},
		},
		{
			name:     "text and JSON mixed",
			inputs:   []string{"Prompt about gt-abc123", `{"context": "gt-def456"}`},
			expected: []string{"gt-abc123", "gt-def456"},
		},
		{
			name:     "deduplication across inputs",
			inputs:   []string{"gt-abc123 mentioned", `{"id": "gt-abc123"}`},
			expected: []string{"gt-abc123"},
		},
		{
			name:     "empty strings skipped",
			inputs:   []string{"", "gt-abc123", "", "gt-def456", ""},
			expected: []string{"gt-abc123", "gt-def456"},
		},
		{
			name: "real usage: prompt, context, blocks",
			inputs: []string{
				"How to handle gt-vhsnvd warning?",                // prompt
				`{"epic": "gt-epc-main", "related": "bd-xyz789"}`, // context
				"gt-blocker",                                      // blocks
			},
			expected: []string{"gt-vhsnvd", "gt-epc-main", "bd-xyz789", "gt-blocker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBeadIDsFromAll(tt.inputs...)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractBeadIDsFromAll(%v) = %v, want %v", tt.inputs, result, tt.expected)
			}
		})
	}
}

func TestExtractBeadIDsWithPrefixes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		prefixes []string
		expected []string
	}{
		{
			name:     "empty input",
			input:    "",
			prefixes: []string{"gt", "bd"},
			expected: nil,
		},
		{
			name:     "empty prefixes",
			input:    "gt-abc123",
			prefixes: []string{},
			expected: nil,
		},
		{
			name:     "custom prefix ap",
			input:    "Task ap-qtsup.16 is ready",
			prefixes: []string{"ap"},
			expected: []string{"ap-qtsup.16"},
		},
		{
			name:     "multiple custom prefixes",
			input:    "Issues ap-abc123 and loc-def456 are related",
			prefixes: []string{"ap", "loc"},
			expected: []string{"ap-abc123", "loc-def456"},
		},
		{
			name:     "standard prefixes explicitly",
			input:    "Working on gt-abc123 and bd-xyz789",
			prefixes: []string{"gt", "bd"},
			expected: []string{"gt-abc123", "bd-xyz789"},
		},
		{
			name:     "mixed standard and custom",
			input:    "Issues gt-abc123 and custom-xyz are here",
			prefixes: []string{"gt", "custom"},
			expected: []string{"gt-abc123", "custom-xyz"},
		},
		{
			name:     "prefix not found",
			input:    "Issue gt-abc123 has wrong prefix",
			prefixes: []string{"bd", "hq"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBeadIDsWithPrefixes(tt.input, tt.prefixes)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractBeadIDsWithPrefixes(%q, %v) = %v, want %v", tt.input, tt.prefixes, result, tt.expected)
			}
		})
	}
}

func TestBeadIDPatternEdgeCases(t *testing.T) {
	// Additional edge case tests for the regex pattern
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		// Minimum valid IDs with known prefixes
		{
			name:     "gt prefix with single char ID",
			input:    "gt-x",
			expected: []string{"gt-x"},
		},
		{
			name:     "bd prefix with single char ID",
			input:    "bd-x",
			expected: []string{"bd-x"},
		},
		{
			name:     "hq prefix with single char ID",
			input:    "hq-x",
			expected: []string{"hq-x"},
		},

		// Maximum/complex IDs
		{
			name:     "long semantic ID",
			input:    "gt-epc-very_long_semantic_name123",
			expected: []string{"gt-epc-very_long_semantic_name123"},
		},
		{
			name:     "agent ID with hyphenated name",
			input:    "gt-gastown-polecat-my-agent-name",
			expected: []string{"gt-gastown-polecat-my-agent-name"},
		},

		// Adjacent to other text
		{
			name:     "ID followed by colon",
			input:    "gt-abc123: description",
			expected: []string{"gt-abc123"},
		},
		{
			name:     "ID in quotes",
			input:    `"gt-abc123"`,
			expected: []string{"gt-abc123"},
		},
		{
			name:     "ID after equals",
			input:    "id=gt-abc123",
			expected: []string{"gt-abc123"},
		},

		// Numbers in ID
		{
			name:     "ID starting with number after prefix",
			input:    "gt-123abc",
			expected: []string{"gt-123abc"},
		},
		{
			name:     "ID that is all numbers after prefix",
			input:    "gt-123456",
			expected: []string{"gt-123456"},
		},

		// Special boundary cases
		{
			name:     "newline separated IDs",
			input:    "gt-abc123\ngt-def456",
			expected: []string{"gt-abc123", "gt-def456"},
		},
		{
			name:     "tab separated IDs",
			input:    "gt-abc123\tgt-def456",
			expected: []string{"gt-abc123", "gt-def456"},
		},

		// Non-matching patterns
		{
			name:     "unknown 2-char prefix not matched",
			input:    "ab-x",
			expected: nil,
		},
		{
			name:     "unknown 3-char prefix not matched",
			input:    "abc-x",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBeadIDs(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractBeadIDs(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Benchmark tests
func BenchmarkExtractBeadIDs(b *testing.B) {
	input := "Working on gt-abc123 which blocks gt-def456 and relates to hq-mayor. " +
		"The parent epic gt-epc-main has children gt-abc.1, gt-abc.2, and gt-abc.3. " +
		"See also bd-xyz789 and bd-beads-polecat-pearl for context."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractBeadIDs(input)
	}
}

func BenchmarkExtractBeadIDsFromJSON(b *testing.B) {
	input := `{
		"type": "bug_fix",
		"affected_bead": "gt-vhsnvd",
		"parent_epic": "gt-epc-main",
		"blockers": ["gt-block.1", "gt-block.2"],
		"children": [
			{"id": "gt-child.1", "status": "open"},
			{"id": "gt-child.2", "status": "closed"}
		],
		"description": "Fix for hq-mayor agent affecting bd-xyz789"
	}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractBeadIDsFromJSON(input)
	}
}
