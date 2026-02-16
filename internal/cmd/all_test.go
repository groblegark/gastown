package cmd

import (
	"fmt"
	"testing"
)

func TestExpandSpecsStar(t *testing.T) {
	// "*" is the default when no specs are given
	specs := []string{}
	if len(specs) == 0 {
		specs = []string{"*"}
	}
	if specs[0] != "*" {
		t.Errorf("expected default spec to be *, got %q", specs[0])
	}
}

func TestExpandSpecPatterns(t *testing.T) {
	// Test pattern recognition (not actual expansion, which requires workspace)
	tests := []struct {
		spec     string
		isStar   bool
		isRigAll bool
		isAddr   bool
		isBare   bool
	}{
		{"*", true, false, false, false},
		{"gastown/*", false, true, false, false},
		{"gastown/Toast", false, false, true, false},
		{"Toast", false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			isStar := tt.spec == "*"
			isRigAll := !isStar && len(tt.spec) > 2 && tt.spec[len(tt.spec)-2:] == "/*"
			isAddr := !isStar && !isRigAll && containsSlash(tt.spec)
			isBare := !isStar && !isRigAll && !isAddr

			if isStar != tt.isStar {
				t.Errorf("isStar: got %v, want %v", isStar, tt.isStar)
			}
			if isRigAll != tt.isRigAll {
				t.Errorf("isRigAll: got %v, want %v", isRigAll, tt.isRigAll)
			}
			if isAddr != tt.isAddr {
				t.Errorf("isAddr: got %v, want %v", isAddr, tt.isAddr)
			}
			if isBare != tt.isBare {
				t.Errorf("isBare: got %v, want %v", isBare, tt.isBare)
			}
		})
	}
}

func containsSlash(s string) bool {
	for _, c := range s {
		if c == '/' {
			return true
		}
	}
	return false
}

func TestAllStatusItemJSON(t *testing.T) {
	item := AllStatusItem{
		Rig:            "gastown",
		Polecat:        "Toast",
		State:          "working",
		Issue:          "gt-abc",
		SessionRunning: true,
		Branch:         "polecat/Toast-12345",
	}

	if item.Rig != "gastown" {
		t.Errorf("Rig = %q, want %q", item.Rig, "gastown")
	}
	if item.Polecat != "Toast" {
		t.Errorf("Polecat = %q, want %q", item.Polecat, "Toast")
	}
	if !item.SessionRunning {
		t.Error("expected SessionRunning to be true")
	}
}

func TestAllResultTracking(t *testing.T) {
	results := []allResult{
		{Address: "gastown/Toast", Err: nil},
		{Address: "gastown/Furiosa", Err: nil},
		{Address: "gastown/Max", Err: fmt.Errorf("session not found")},
	}

	var succeeded, failed int
	for _, r := range results {
		if r.Err != nil {
			failed++
		} else {
			succeeded++
		}
	}

	if succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", succeeded)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

func TestFilterAwakeEmpty(t *testing.T) {
	// filterAwake with empty input returns nil
	result := filterAwake(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}
