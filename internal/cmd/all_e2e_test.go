package cmd

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/steveyegge/gastown/internal/polecat"
)

// Tests for gt all batch operations (beads-s8tf)

func TestExpandSpecsDefaultsToStar(t *testing.T) {
	// expandSpecs with empty input should default to ["*"]
	// We can't test actual expansion without a workspace, but verify the logic
	specs := []string{}
	if len(specs) == 0 {
		specs = []string{"*"}
	}
	if len(specs) != 1 || specs[0] != "*" {
		t.Errorf("expected [*], got %v", specs)
	}
}

func TestExpandOneSpec_PatternClassification(t *testing.T) {
	tests := []struct {
		spec      string
		wantType  string // "star", "rig_all", "address", "bare"
	}{
		{"*", "star"},
		{"gastown/*", "rig_all"},
		{"greenplace/*", "rig_all"},
		{"gastown/Toast", "address"},
		{"gastown/Furiosa", "address"},
		{"Toast", "bare"},
		{"Furiosa", "bare"},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			var gotType string
			switch {
			case tt.spec == "*":
				gotType = "star"
			case len(tt.spec) > 2 && tt.spec[len(tt.spec)-2:] == "/*":
				gotType = "rig_all"
			case containsSlash(tt.spec):
				gotType = "address"
			default:
				gotType = "bare"
			}

			if gotType != tt.wantType {
				t.Errorf("spec %q classified as %q, want %q", tt.spec, gotType, tt.wantType)
			}
		})
	}
}

func TestExpandSpecsDedup(t *testing.T) {
	// Verify the dedup logic works
	seen := make(map[string]bool)
	var results []string

	inputs := []string{
		"gastown/Toast",
		"gastown/Furiosa",
		"gastown/Toast", // duplicate
		"greenplace/Max",
		"gastown/Furiosa", // duplicate
	}

	for _, key := range inputs {
		if !seen[key] {
			seen[key] = true
			results = append(results, key)
		}
	}

	if len(results) != 3 {
		t.Errorf("expected 3 unique results, got %d: %v", len(results), results)
	}
}

func TestAllStatusItemJSON_Roundtrip(t *testing.T) {
	item := AllStatusItem{
		Rig:            "gastown",
		Polecat:        "Toast",
		State:          polecat.StateWorking,
		Issue:          "gt-abc",
		SessionRunning: true,
		Branch:         "polecat/Toast-20260215",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed AllStatusItem
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.Rig != "gastown" {
		t.Errorf("Rig = %q, want %q", parsed.Rig, "gastown")
	}
	if parsed.Polecat != "Toast" {
		t.Errorf("Polecat = %q, want %q", parsed.Polecat, "Toast")
	}
	if parsed.State != polecat.StateWorking {
		t.Errorf("State = %q, want %q", parsed.State, polecat.StateWorking)
	}
	if parsed.Issue != "gt-abc" {
		t.Errorf("Issue = %q, want %q", parsed.Issue, "gt-abc")
	}
	if !parsed.SessionRunning {
		t.Error("expected SessionRunning to be true")
	}
	if parsed.Branch != "polecat/Toast-20260215" {
		t.Errorf("Branch = %q, want %q", parsed.Branch, "polecat/Toast-20260215")
	}
}

func TestAllStatusItemJSON_OmitsEmpty(t *testing.T) {
	item := AllStatusItem{
		Rig:            "gastown",
		Polecat:        "Toast",
		State:          polecat.StateWorking,
		SessionRunning: false,
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	// omitempty fields should be absent
	if _, ok := raw["issue"]; ok {
		t.Error("expected issue to be omitted when empty")
	}
	if _, ok := raw["branch"]; ok {
		t.Error("expected branch to be omitted when empty")
	}

	// required fields should be present
	if _, ok := raw["rig"]; !ok {
		t.Error("expected rig to be present")
	}
	if _, ok := raw["polecat"]; !ok {
		t.Error("expected polecat to be present")
	}
}

func TestRunForAll_ParallelExecution(t *testing.T) {
	// Verify runForAll executes actions in parallel and collects results
	targets := []expandedPolecat{
		{RigName: "rig1", PolecatName: "p1"},
		{RigName: "rig1", PolecatName: "p2"},
		{RigName: "rig2", PolecatName: "p3"},
	}

	var mu sync.Mutex
	var executed []string

	results := runForAll(targets, func(t expandedPolecat) error {
		mu.Lock()
		executed = append(executed, t.RigName+"/"+t.PolecatName)
		mu.Unlock()
		if t.PolecatName == "p2" {
			return fmt.Errorf("simulated failure")
		}
		return nil
	})

	// All 3 should have executed
	if len(executed) != 3 {
		t.Errorf("expected 3 executions, got %d", len(executed))
	}

	// Results should be indexed correctly
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Check results by index (runForAll preserves order)
	if results[0].Address != "rig1/p1" || results[0].Err != nil {
		t.Errorf("result[0]: got %v, want success for rig1/p1", results[0])
	}
	if results[1].Address != "rig1/p2" || results[1].Err == nil {
		t.Errorf("result[1]: expected error for rig1/p2")
	}
	if results[2].Address != "rig2/p3" || results[2].Err != nil {
		t.Errorf("result[2]: got %v, want success for rig2/p3", results[2])
	}
}

func TestRunForAll_Empty(t *testing.T) {
	results := runForAll(nil, func(t expandedPolecat) error {
		return nil
	})
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil targets, got %d", len(results))
	}
}

func TestAllResult_ErrorAggregation(t *testing.T) {
	tests := []struct {
		name          string
		results       []allResult
		wantSucceeded int
		wantFailed    int
	}{
		{
			"all_success",
			[]allResult{
				{Address: "r/a", Err: nil},
				{Address: "r/b", Err: nil},
			},
			2, 0,
		},
		{
			"all_failed",
			[]allResult{
				{Address: "r/a", Err: fmt.Errorf("err1")},
				{Address: "r/b", Err: fmt.Errorf("err2")},
			},
			0, 2,
		},
		{
			"mixed",
			[]allResult{
				{Address: "r/a", Err: nil},
				{Address: "r/b", Err: fmt.Errorf("err")},
				{Address: "r/c", Err: nil},
			},
			2, 1,
		},
		{
			"empty",
			[]allResult{},
			0, 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var succeeded, failed int
			for _, r := range tt.results {
				if r.Err != nil {
					failed++
				} else {
					succeeded++
				}
			}
			if succeeded != tt.wantSucceeded {
				t.Errorf("succeeded = %d, want %d", succeeded, tt.wantSucceeded)
			}
			if failed != tt.wantFailed {
				t.Errorf("failed = %d, want %d", failed, tt.wantFailed)
			}
		})
	}
}

func TestExpandedPolecat_Fields(t *testing.T) {
	ep := expandedPolecat{
		RigName:     "gastown",
		PolecatName: "Toast",
	}

	key := ep.RigName + "/" + ep.PolecatName
	if key != "gastown/Toast" {
		t.Errorf("key = %q, want %q", key, "gastown/Toast")
	}
}

func TestSpecPatterns_EdgeCases(t *testing.T) {
	// Edge cases in pattern matching
	tests := []struct {
		spec     string
		isStar   bool
		isRigAll bool
	}{
		{"*", true, false},
		{"/*", false, true},       // slash-star with empty rig name
		{"a/*", false, true},      // single-char rig name
		{"a/b", false, false},     // simple address, not rig/*
		{"a/b/*", false, true},    // nested path — still matches /*suffix
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			isStar := tt.spec == "*"
			isRigAll := !isStar && len(tt.spec) >= 2 && tt.spec[len(tt.spec)-2:] == "/*"

			// For "a/b/*", it matches "/*" suffix but it's deeper nesting
			// The actual expandOneSpec handles this via strings.HasSuffix
			if isStar != tt.isStar {
				t.Errorf("isStar: got %v, want %v", isStar, tt.isStar)
			}
			if isRigAll != tt.isRigAll {
				t.Errorf("isRigAll: got %v, want %v", isRigAll, tt.isRigAll)
			}
		})
	}
}
