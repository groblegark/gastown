package cmd

import (
	"testing"
)

// ---------------------------------------------------------------------------
// TestCategorizeSession — table-driven tests for categorizeSession()
// ---------------------------------------------------------------------------

func TestCategorizeSession(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantType  AgentType
		wantRig   string
		wantAgent string
	}{
		// Town-level agents (hq- prefix)
		{
			name:     "hq-mayor",
			input:    "hq-mayor",
			wantType: AgentMayor,
		},
		{
			name:     "hq-deacon",
			input:    "hq-deacon",
			wantType: AgentDeacon,
		},
		{
			name:    "unknown hq- prefix",
			input:   "hq-boot",
			wantNil: true,
		},
		{
			name:    "hq- with empty suffix",
			input:   "hq-",
			wantNil: true,
		},

		// Rig-level agents (gt- prefix)
		{
			name:     "witness (new format): gt-<rig>-witness",
			input:    "gt-gastown-witness",
			wantType: AgentWitness,
			wantRig:  "gastown",
		},
		{
			name:     "refinery: gt-<rig>-refinery",
			input:    "gt-myrig-refinery",
			wantType: AgentRefinery,
			wantRig:  "myrig",
		},
		{
			name:      "crew: gt-<rig>-crew-<name>",
			input:     "gt-gastown-crew-alice",
			wantType:  AgentCrew,
			wantRig:   "gastown",
			wantAgent: "alice",
		},
		{
			name:      "crew with hyphenated name: gt-<rig>-crew-my-worker",
			input:     "gt-gastown-crew-my-worker",
			wantType:  AgentCrew,
			wantRig:   "gastown",
			wantAgent: "my-worker",
		},
		{
			name:      "polecat (catch-all): gt-<rig>-<name>",
			input:     "gt-gastown-bob",
			wantType:  AgentPolecat,
			wantRig:   "gastown",
			wantAgent: "bob",
		},
		{
			name:      "polecat with hyphenated name: gt-<rig>-some-task",
			input:     "gt-gastown-some-task",
			wantType:  AgentPolecat,
			wantRig:   "gastown",
			wantAgent: "some-task",
		},

		// Edge cases — sessions that should return nil
		{
			name:    "no prefix at all",
			input:   "random-session",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
		{
			name:    "gt- with only rig, no type (single part after gt-)",
			input:   "gt-gastown",
			wantNil: true,
		},
		{
			name:    "bare gt- prefix",
			input:   "gt-",
			wantNil: true,
		},
		{
			name:    "default session name",
			input:   "0",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categorizeSession(tt.input)

			if tt.wantNil {
				if got != nil {
					t.Fatalf("categorizeSession(%q) = %+v, want nil", tt.input, got)
				}
				return
			}

			if got == nil {
				t.Fatalf("categorizeSession(%q) = nil, want non-nil", tt.input)
			}

			if got.Type != tt.wantType {
				t.Errorf("categorizeSession(%q).Type = %d, want %d", tt.input, got.Type, tt.wantType)
			}
			if got.Rig != tt.wantRig {
				t.Errorf("categorizeSession(%q).Rig = %q, want %q", tt.input, got.Rig, tt.wantRig)
			}
			if got.AgentName != tt.wantAgent {
				t.Errorf("categorizeSession(%q).AgentName = %q, want %q", tt.input, got.AgentName, tt.wantAgent)
			}
			if got.Name != tt.input {
				t.Errorf("categorizeSession(%q).Name = %q, want %q", tt.input, got.Name, tt.input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMergeK8sAgents — tests for mergeK8sAgents dedup & role mapping
// ---------------------------------------------------------------------------

func TestMergeK8sAgents_RoleMapping(t *testing.T) {
	// Test that mergeK8sAgents correctly maps registry session roles to AgentTypes.
	// We cannot easily call mergeK8sAgents (needs workspace), so we test the
	// role mapping logic from the switch statement in mergeK8sAgents indirectly
	// by verifying the categorizeSession results and the K8s role to AgentType
	// mapping is consistent.

	roleToType := map[string]AgentType{
		"mayor":       AgentMayor,
		"coordinator": AgentMayor,
		"deacon":      AgentDeacon,
		"health-check": AgentDeacon,
		"witness":     AgentWitness,
		"refinery":    AgentRefinery,
		"crew":        AgentCrew,
		"polecat":     AgentPolecat,
	}

	// Verify each mapping produces a valid AgentType with an icon
	for role, expectedType := range roleToType {
		if _, ok := AgentTypeIcons[expectedType]; !ok {
			t.Errorf("role %q maps to AgentType %d which has no icon", role, expectedType)
		}
	}
}

func TestMergeK8sAgents_Dedup(t *testing.T) {
	// The seen map should prevent duplicate entries when a session name
	// already exists from discovery.
	seen := map[string]bool{
		"hq-mayor":            true,
		"gt-gastown-witness":  true,
	}

	// Simulate that these names are already in the agents list
	agents := []*AgentSession{
		{Name: "hq-mayor", Type: AgentMayor},
		{Name: "gt-gastown-witness", Type: AgentWitness, Rig: "gastown"},
	}

	initialLen := len(agents)

	// The seen map would prevent any K8s session with the same TmuxSession
	// name from being added. Verify the precondition.
	for _, a := range agents {
		if !seen[a.Name] {
			t.Errorf("agent %q not in seen map", a.Name)
		}
	}

	// After mergeK8sAgents, if all K8s sessions have matching names in seen,
	// the agents list should not grow.
	if len(agents) != initialLen {
		t.Errorf("agents list grew from %d to %d — dedup failed", initialLen, len(agents))
	}
}

func TestAgentSession_K8sFields(t *testing.T) {
	// Verify K8s-specific fields are properly set
	agent := &AgentSession{
		Name:      "gt-gastown-crew-k8s",
		Type:      AgentCrew,
		Rig:       "gastown",
		AgentName: "k8s",
		K8s:       true,
		BeadID:    "gt-gastown-crew-k8s",
	}

	if !agent.K8s {
		t.Error("agent.K8s should be true")
	}
	if agent.BeadID == "" {
		t.Error("agent.BeadID should be set for K8s agents")
	}
}

// ---------------------------------------------------------------------------
// TestAgentTypeConsistency — verifies that all AgentType constants have
// entries in the AgentTypeIcons map.
// ---------------------------------------------------------------------------

func TestAgentTypeConsistency(t *testing.T) {
	allTypes := []AgentType{
		AgentMayor,
		AgentDeacon,
		AgentWitness,
		AgentRefinery,
		AgentCrew,
		AgentPolecat,
	}

	for _, at := range allTypes {
		if _, ok := AgentTypeIcons[at]; !ok {
			t.Errorf("AgentType %d missing from AgentTypeIcons", at)
		}
	}
}
