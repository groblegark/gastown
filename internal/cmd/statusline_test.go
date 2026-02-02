package cmd

import "testing"

func TestCategorizeSessionRig(t *testing.T) {
	tests := []struct {
		session string
		wantRig string
	}{
		// Standard polecat sessions
		{"gt-gastown-slit", "gastown"},
		{"gt-gastown-Toast", "gastown"},
		{"gt-myrig-worker", "myrig"},

		// Crew sessions
		{"gt-gastown-crew-max", "gastown"},
		{"gt-myrig-crew-user", "myrig"},

		// Witness sessions (canonical format: gt-<rig>-witness)
		{"gt-gastown-witness", "gastown"},
		{"gt-myrig-witness", "myrig"},
		// Legacy format still works as fallback
		{"gt-witness-gastown", "gastown"},
		{"gt-witness-myrig", "myrig"},

		// Refinery sessions
		{"gt-gastown-refinery", "gastown"},
		{"gt-myrig-refinery", "myrig"},

		// Edge cases
		{"gt-a-b", "a"}, // minimum valid

		// Town-level agents (no rig, use hq- prefix)
		{"hq-mayor", ""},
		{"hq-deacon", ""},
	}

	for _, tt := range tests {
		t.Run(tt.session, func(t *testing.T) {
			agent := categorizeSession(tt.session)
			gotRig := ""
			if agent != nil {
				gotRig = agent.Rig
			}
			if gotRig != tt.wantRig {
				t.Errorf("categorizeSession(%q).Rig = %q, want %q", tt.session, gotRig, tt.wantRig)
			}
		})
	}
}

func TestCategorizeSessionType(t *testing.T) {
	tests := []struct {
		session  string
		wantType AgentType
	}{
		// Polecat sessions
		{"gt-gastown-slit", AgentPolecat},
		{"gt-gastown-Toast", AgentPolecat},
		{"gt-myrig-worker", AgentPolecat},
		{"gt-a-b", AgentPolecat},

		// Non-polecat sessions
		{"gt-gastown-witness", AgentWitness}, // canonical format
		{"gt-witness-gastown", AgentWitness}, // legacy fallback
		{"gt-gastown-refinery", AgentRefinery},
		{"gt-gastown-crew-max", AgentCrew},
		{"gt-myrig-crew-user", AgentCrew},

		// Town-level agents (hq- prefix)
		{"hq-mayor", AgentMayor},
		{"hq-deacon", AgentDeacon},
	}

	for _, tt := range tests {
		t.Run(tt.session, func(t *testing.T) {
			agent := categorizeSession(tt.session)
			if agent == nil {
				t.Fatalf("categorizeSession(%q) returned nil", tt.session)
			}
			if agent.Type != tt.wantType {
				t.Errorf("categorizeSession(%q).Type = %v, want %v", tt.session, agent.Type, tt.wantType)
			}
		})
	}
}

// TestSessionToIdentity tests conversion from tmux session names to identity strings (gt-avr97i.1).
func TestSessionToIdentity(t *testing.T) {
	tests := []struct {
		session      string
		wantIdentity string
	}{
		// Town-level agents
		{"hq-mayor", "mayor/"},
		{"hq-deacon", "deacon/"},

		// Witness sessions
		{"gt-gastown-witness", "gastown/witness"},
		{"gt-myrig-witness", "myrig/witness"},
		{"gt-witness-gastown", "gastown/witness"}, // legacy format

		// Refinery sessions
		{"gt-gastown-refinery", "gastown/refinery"},
		{"gt-myrig-refinery", "myrig/refinery"},

		// Crew sessions
		{"gt-gastown-crew-decisions", "gastown/crew/decisions"},
		{"gt-myrig-crew-max", "myrig/crew/max"},

		// Polecat sessions
		{"gt-gastown-slit", "gastown/slit"},
		{"gt-myrig-worker", "myrig/worker"},

		// Edge cases
		{"gt-a-b", "a/b"},       // minimum valid
		{"invalid-session", ""}, // no gt- or hq- prefix
		{"gt-norig", ""},        // missing agent type
		{"hq-unknown", ""},      // unknown hq- session
	}

	for _, tt := range tests {
		t.Run(tt.session, func(t *testing.T) {
			got := sessionToIdentity(tt.session)
			if got != tt.wantIdentity {
				t.Errorf("sessionToIdentity(%q) = %q, want %q", tt.session, got, tt.wantIdentity)
			}
		})
	}
}
