package cmd

import (
	"testing"
	"time"
)

func TestCalculateEffectiveTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     string
		backoffBase string
		backoffMult int
		backoffMax  string
		idleCycles  int
		want        time.Duration
		wantErr     bool
	}{
		{
			name:    "simple timeout 60s",
			timeout: "60s",
			want:    60 * time.Second,
		},
		{
			name:    "simple timeout 5m",
			timeout: "5m",
			want:    5 * time.Minute,
		},
		{
			name:        "backoff base only, idle=0",
			timeout:     "60s",
			backoffBase: "30s",
			idleCycles:  0,
			want:        30 * time.Second,
		},
		{
			name:        "backoff with idle=1, mult=2",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			idleCycles:  1,
			want:        60 * time.Second,
		},
		{
			name:        "backoff with idle=2, mult=2",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			idleCycles:  2,
			want:        2 * time.Minute,
		},
		{
			name:        "backoff with max cap",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			backoffMax:  "5m",
			idleCycles:  10, // Would be 30s * 2^10 = ~8.5h but capped at 5m
			want:        5 * time.Minute,
		},
		{
			name:        "backoff base exceeds max",
			timeout:     "60s",
			backoffBase: "15m",
			backoffMax:  "10m",
			want:        10 * time.Minute,
		},
		{
			name:    "invalid timeout",
			timeout: "invalid",
			wantErr: true,
		},
		{
			name:        "invalid backoff base",
			timeout:     "60s",
			backoffBase: "invalid",
			wantErr:     true,
		},
		{
			name:        "invalid backoff max",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMax:  "invalid",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set package-level variables
			awaitSignalTimeout = tt.timeout
			awaitSignalBackoffBase = tt.backoffBase
			awaitSignalBackoffMult = tt.backoffMult
			if tt.backoffMult == 0 {
				awaitSignalBackoffMult = 2 // default
			}
			awaitSignalBackoffMax = tt.backoffMax

			got, err := calculateEffectiveTimeout(tt.idleCycles)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateEffectiveTimeout() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("calculateEffectiveTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsMeaningfulActivityEvent verifies that noise events (agent pod heartbeats,
// agent metadata updates) are filtered out, while real work events pass through.
// This prevents await-signal from waking on routine RPC noise. (hq-tmvc6u)
func TestIsMeaningfulActivityEvent(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		// Meaningful: issue creation
		{"create task", `{"type":"create","issue_id":"bd-xyz","issue_type":"task"}`, true},
		{"create bug", `{"type":"create","issue_id":"bd-bug1","issue_type":"bug"}`, true},
		{"create feature", `{"type":"create","issue_id":"hq-feat","issue_type":"feature"}`, true},
		// Meaningful: even agent creation is meaningful (new agent registered)
		{"create agent", `{"type":"create","issue_id":"gt-agent1","issue_type":"agent"}`, true},

		// Meaningful: status transitions
		{"status change", `{"type":"status","issue_id":"bd-xyz","issue_type":"task","new_status":"in_progress"}`, true},
		{"status completed", `{"type":"status","issue_id":"bd-xyz","issue_type":"task","new_status":"closed"}`, true},
		// Even agent status changes are meaningful (agent stopped/started)
		{"agent status", `{"type":"status","issue_id":"gt-a1","issue_type":"agent","new_status":"closed"}`, true},

		// Meaningful: molecule operations
		{"bonded", `{"type":"bonded","issue_id":"bd-mol1","issue_type":"task"}`, true},
		{"squashed", `{"type":"squashed","issue_id":"bd-mol1","issue_type":"task"}`, true},
		{"burned", `{"type":"burned","issue_id":"bd-mol1","issue_type":"task"}`, true},

		// Meaningful: deletion
		{"delete", `{"type":"delete","issue_id":"bd-old","issue_type":"task"}`, true},

		// Meaningful: updates to real work items
		{"update task", `{"type":"update","issue_id":"bd-xyz","issue_type":"task"}`, true},
		{"update bug", `{"type":"update","issue_id":"bd-bug1","issue_type":"bug"}`, true},
		{"update gate", `{"type":"update","issue_id":"bd-gate1","issue_type":"gate"}`, true},

		// NOISE: agent pod heartbeats and metadata updates
		{"agent update (pod heartbeat)", `{"type":"update","issue_id":"gt-witness","issue_type":"agent"}`, false},
		{"agent update (label churn)", `{"type":"update","issue_id":"gt-refinery","issue_type":"agent"}`, false},
		{"agent comment", `{"type":"comment","issue_id":"gt-witness","issue_type":"agent"}`, false},

		// Meaningful: comments on real work
		{"comment on task", `{"type":"comment","issue_id":"bd-xyz","issue_type":"task"}`, true},

		// Edge case: unparseable JSON is treated as meaningful (safety)
		{"invalid json", `not json at all`, true},

		// Edge case: empty issue_type is treated as real work
		{"update no type", `{"type":"update","issue_id":"bd-xyz"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMeaningfulActivityEvent(tt.line)
			if got != tt.want {
				t.Errorf("isMeaningfulActivityEvent(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestAwaitSignalResult(t *testing.T) {
	// Test that result struct marshals correctly
	result := AwaitSignalResult{
		Reason:  "signal",
		Elapsed: 5 * time.Second,
		Signal:  "[12:34:56] + gt-abc created · New issue",
	}

	if result.Reason != "signal" {
		t.Errorf("expected reason 'signal', got %q", result.Reason)
	}
	if result.Signal == "" {
		t.Error("expected signal to be set")
	}
}
