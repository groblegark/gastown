// Package beads provides a wrapper for the bd (beads) CLI.
package beads

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// KnownPrefixes lists the standard bead ID prefixes.
// gt- is for Gas Town/rig-level beads
// bd- is for Beads project beads
// hq- is for headquarters/town-level beads
var KnownPrefixes = []string{"gt", "bd", "hq"}

// beadIDPattern matches bead IDs with known prefixes followed by hyphen and identifier.
// Patterns matched:
//   - Hash-based: gt-abc123, bd-a3f8e9, hq-39bc13
//   - Semantic: gt-epc-semantic7x9k, gt-mol-xyz, gt-mr-abc123
//   - Hierarchical: gt-abc.1.2, gt-abc.1, gt-qtsup.16
//   - Agent: gt-gastown-witness, hq-mayor, gt-gastown-crew-joe
//
// The pattern requires:
//   - A known prefix (gt, bd, hq)
//   - A hyphen separator
//   - At least one alphanumeric character
//   - Optional additional characters (alphanumeric, hyphens, underscores, dots)
//   - Must end with alphanumeric or digit after dot (for hierarchical)
//
// Note: Only known prefixes are matched to avoid false positives with English words
// like "sub-subtask" or "pre-process". For custom prefixes, use ExtractBeadIDsWithPrefixes.
var beadIDPattern = regexp.MustCompile(`\b(gt|bd|hq)-([a-z0-9][a-z0-9_-]*(?:\.[0-9]+)*)\b`)

// ExtractBeadIDs extracts all bead IDs from text using known prefix patterns.
// Handles: gt-abc123, bd-xyz789, hq-mol-1s1, hierarchical gt-abc.1.2
//
// The function searches for bead ID patterns in plain text and extracts unique
// IDs in the order they first appear. It is case-insensitive for matching but
// returns IDs in lowercase.
//
// For JSON input, use ExtractBeadIDsFromJSON which recursively searches all
// string values in the JSON structure.
func ExtractBeadIDs(text string) []string {
	if text == "" {
		return nil
	}

	// Convert to lowercase for consistent matching
	lowered := strings.ToLower(text)

	// Find all matches
	matches := beadIDPattern.FindAllString(lowered, -1)
	if len(matches) == 0 {
		return nil
	}

	// Deduplicate while preserving order
	seen := make(map[string]bool)
	var result []string
	for _, match := range matches {
		if !seen[match] {
			seen[match] = true
			result = append(result, match)
		}
	}

	return result
}

// ExtractBeadIDsFromJSON extracts all bead IDs from a JSON string.
// It recursively searches all string values in the JSON structure,
// including nested objects and arrays.
//
// Returns nil if the input is empty or not valid JSON.
// Non-JSON text is also searched for bead IDs in string values.
func ExtractBeadIDsFromJSON(jsonText string) []string {
	if jsonText == "" {
		return nil
	}

	// Try to parse as JSON
	var data interface{}
	if err := json.Unmarshal([]byte(jsonText), &data); err != nil {
		// Not valid JSON, treat as plain text
		return ExtractBeadIDs(jsonText)
	}

	// Collect all string values from the JSON
	var strings []string
	collectStrings(data, &strings)

	// Extract bead IDs from all collected strings
	seen := make(map[string]bool)
	var result []string
	for _, s := range strings {
		ids := ExtractBeadIDs(s)
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}

	return result
}

// collectStrings recursively collects all string values from a JSON structure.
func collectStrings(data interface{}, result *[]string) {
	switch v := data.(type) {
	case string:
		*result = append(*result, v)
	case map[string]interface{}:
		// Sort keys for deterministic output order
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// Also check the key itself (might contain bead ID)
			*result = append(*result, k)
			collectStrings(v[k], result)
		}
	case []interface{}:
		for _, item := range v {
			collectStrings(item, result)
		}
	}
}

// ExtractBeadIDsWithPrefixes extracts bead IDs using a custom set of prefixes.
// This is useful when you need to match prefixes beyond the standard gt/bd/hq.
// Each prefix should be 2-3 characters without the trailing hyphen.
func ExtractBeadIDsWithPrefixes(text string, prefixes []string) []string {
	if text == "" || len(prefixes) == 0 {
		return nil
	}

	// Build pattern with custom prefixes
	escapedPrefixes := make([]string, len(prefixes))
	for i, p := range prefixes {
		escapedPrefixes[i] = regexp.QuoteMeta(strings.ToLower(p))
	}
	patternStr := `\b(` + strings.Join(escapedPrefixes, "|") + `)-([a-z0-9][a-z0-9_-]*(?:\.[0-9]+)*)\b`
	pattern := regexp.MustCompile(patternStr)

	// Convert to lowercase for consistent matching
	lowered := strings.ToLower(text)

	// Find all matches
	matches := pattern.FindAllString(lowered, -1)
	if len(matches) == 0 {
		return nil
	}

	// Deduplicate while preserving order
	seen := make(map[string]bool)
	var result []string
	for _, match := range matches {
		if !seen[match] {
			seen[match] = true
			result = append(result, match)
		}
	}

	return result
}

// ExtractBeadIDsFromAll extracts bead IDs from multiple text sources.
// This is a convenience function for extracting from prompt, context, and blocks
// in a single call. Duplicates across sources are removed.
func ExtractBeadIDsFromAll(texts ...string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, text := range texts {
		if text == "" {
			continue
		}

		var ids []string
		// Try JSON extraction first (handles both JSON and plain text)
		if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[") {
			ids = ExtractBeadIDsFromJSON(text)
		} else {
			ids = ExtractBeadIDs(text)
		}

		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}

	return result
}
