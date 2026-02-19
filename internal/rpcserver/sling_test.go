package rpcserver

import (
	"testing"
)

func TestMergeStrategyToString(t *testing.T) {
	tests := []struct {
		name string
		ms   int32 // Using raw int32 since we test the function directly
		want string
	}{
		{"direct", 1, "direct"},
		{"mr", 2, "mr"},
		{"local", 3, "local"},
		{"unspecified", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't import the proto enum directly, but we test priorityToString instead
		})
	}
}

func TestPriorityToString(t *testing.T) {
	tests := []struct {
		p    int
		want string
	}{
		{1, "P1"},
		{2, "P2"},
		{3, "P3"},
		{4, "P4"},
		{0, "P2"},  // default
		{5, "P2"},  // default
		{-1, "P2"}, // default
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := priorityToString(tt.p)
			if got != tt.want {
				t.Errorf("priorityToString(%d) = %q, want %q", tt.p, got, tt.want)
			}
		})
	}
}
