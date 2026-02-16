package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/monitoring"
)

// Tests for monitoring integration in gt status (beads-pqsb)

func TestInferAgentStatus_K8sPolecats(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{"k8s_spawning", "spawning", string(monitoring.StatusWorking)},
		{"k8s_working", "working", string(monitoring.StatusWorking)},
		{"k8s_done", "done", string(monitoring.StatusAvailable)},
		{"k8s_stuck", "stuck", string(monitoring.StatusError)},
		{"k8s_empty", "", string(monitoring.StatusOffline)},
		{"k8s_unknown", "something_else", string(monitoring.StatusOffline)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := AgentRuntime{
				Target: "k8s",
				State:  tt.state,
			}
			got := inferAgentStatus(agent)
			if got != tt.want {
				t.Errorf("inferAgentStatus(k8s, state=%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestInferAgentStatus_LocalNotRunning(t *testing.T) {
	agent := AgentRuntime{
		Target:  "local",
		Running: false,
		State:   "working",
	}
	got := inferAgentStatus(agent)
	if got != string(monitoring.StatusOffline) {
		t.Errorf("inferAgentStatus(local, not running) = %q, want %q", got, string(monitoring.StatusOffline))
	}
}

func TestInferAgentStatus_LocalRunningStates(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		hasWork bool
		want    string
	}{
		{"stuck", "stuck", false, string(monitoring.StatusError)},
		{"awaiting_gate", "awaiting-gate", false, string(monitoring.StatusBlocked)},
		{"paused", "paused", false, string(monitoring.StatusPaused)},
		{"muted", "muted", false, string(monitoring.StatusPaused)},
		{"degraded", "degraded", false, string(monitoring.StatusError)},
		{"working_with_work", "working", true, string(monitoring.StatusWorking)},
		{"empty_with_work", "", true, string(monitoring.StatusWorking)},
		{"empty_no_work", "", false, string(monitoring.StatusAvailable)},
		{"working_no_work", "working", false, string(monitoring.StatusAvailable)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := AgentRuntime{
				Target:  "local",
				Running: true,
				State:   tt.state,
				HasWork: tt.hasWork,
			}
			got := inferAgentStatus(agent)
			if got != tt.want {
				t.Errorf("inferAgentStatus(local, running, state=%q, hasWork=%v) = %q, want %q",
					tt.state, tt.hasWork, got, tt.want)
			}
		})
	}
}

func TestInferAgentStatus_DefaultTargetIsLocal(t *testing.T) {
	agent := AgentRuntime{
		Running: true,
		HasWork: true,
	}
	got := inferAgentStatus(agent)
	if got != string(monitoring.StatusWorking) {
		t.Errorf("inferAgentStatus(empty target, running, hasWork) = %q, want %q",
			got, string(monitoring.StatusWorking))
	}
}

func TestAgentRuntime_InferredStatusField(t *testing.T) {
	agent := AgentRuntime{
		Name:    "Toast",
		Target:  "local",
		Running: true,
		HasWork: true,
	}
	agent.InferredStatus = inferAgentStatus(agent)

	if agent.InferredStatus != string(monitoring.StatusWorking) {
		t.Errorf("InferredStatus = %q, want %q",
			agent.InferredStatus, string(monitoring.StatusWorking))
	}
}

func TestMonitoringStatuses_Comprehensive(t *testing.T) {
	statuses := []monitoring.AgentStatus{
		monitoring.StatusAvailable,
		monitoring.StatusWorking,
		monitoring.StatusBlocked,
		monitoring.StatusPaused,
		monitoring.StatusError,
		monitoring.StatusOffline,
	}

	for _, s := range statuses {
		t.Run(string(s), func(t *testing.T) {
			str := string(s)
			if str == "" {
				t.Errorf("status %v has empty string representation", s)
			}
		})
	}
}
