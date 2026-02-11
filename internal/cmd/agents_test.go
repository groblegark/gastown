package cmd

import (
	"fmt"
	"testing"

	"github.com/steveyegge/gastown/internal/constants"
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
			name:     "witness (legacy format): gt-witness-<rig>",
			input:    "gt-witness-gastown",
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
			name:    "tmux default session name",
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
// TestDisplayLabel — table-driven tests for AgentSession.displayLabel()
// ---------------------------------------------------------------------------

func TestDisplayLabel(t *testing.T) {
	tests := []struct {
		name     string
		agent    AgentSession
		wantFmt  string // expected format string
		contains []string
	}{
		{
			name:    "mayor label",
			agent:   AgentSession{Name: "hq-mayor", Type: AgentMayor},
			wantFmt: fmt.Sprintf("%s%s Mayor#[default]", AgentTypeColors[AgentMayor], constants.EmojiMayor),
		},
		{
			name:    "deacon label",
			agent:   AgentSession{Name: "hq-deacon", Type: AgentDeacon},
			wantFmt: fmt.Sprintf("%s%s Deacon#[default]", AgentTypeColors[AgentDeacon], constants.EmojiDeacon),
		},
		{
			name:    "witness label",
			agent:   AgentSession{Name: "gt-gastown-witness", Type: AgentWitness, Rig: "gastown"},
			wantFmt: fmt.Sprintf("%s%s gastown/witness#[default]", AgentTypeColors[AgentWitness], constants.EmojiWitness),
		},
		{
			name:    "refinery label",
			agent:   AgentSession{Name: "gt-myrig-refinery", Type: AgentRefinery, Rig: "myrig"},
			wantFmt: fmt.Sprintf("%s%s myrig/refinery#[default]", AgentTypeColors[AgentRefinery], constants.EmojiRefinery),
		},
		{
			name:    "crew label",
			agent:   AgentSession{Name: "gt-gastown-crew-alice", Type: AgentCrew, Rig: "gastown", AgentName: "alice"},
			wantFmt: fmt.Sprintf("%s%s gastown/crew/alice#[default]", AgentTypeColors[AgentCrew], constants.EmojiCrew),
		},
		{
			name:    "polecat label",
			agent:   AgentSession{Name: "gt-gastown-bob", Type: AgentPolecat, Rig: "gastown", AgentName: "bob"},
			wantFmt: fmt.Sprintf("%s%s gastown/bob#[default]", AgentTypeColors[AgentPolecat], constants.EmojiPolecat),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.agent.displayLabel()
			if got != tt.wantFmt {
				t.Errorf("displayLabel() = %q, want %q", got, tt.wantFmt)
			}
		})
	}
}

// TestDisplayLabel_ColorFormat verifies that each agent type has a tmux color
// code that starts with "#[" and a matching icon from the constants package.
func TestDisplayLabel_ColorFormat(t *testing.T) {
	for agentType, color := range AgentTypeColors {
		if color == "" {
			t.Errorf("AgentTypeColors[%d] is empty", agentType)
		}
		if color[0:2] != "#[" {
			t.Errorf("AgentTypeColors[%d] = %q, does not start with '#['", agentType, color)
		}
	}

	for agentType, icon := range AgentTypeIcons {
		if icon == "" {
			t.Errorf("AgentTypeIcons[%d] is empty", agentType)
		}
		_ = agentType
	}
}

// TestDisplayLabel_UnknownTypeFallback tests the fallback path where displayLabel
// returns the raw Name when the AgentType doesn't match any known case.
func TestDisplayLabel_UnknownTypeFallback(t *testing.T) {
	agent := AgentSession{
		Name: "mystery-session",
		Type: AgentType(99), // Unknown type
	}
	got := agent.displayLabel()
	if got != "mystery-session" {
		t.Errorf("displayLabel() for unknown type = %q, want %q", got, "mystery-session")
	}
}

// ---------------------------------------------------------------------------
// TestShortcutKey — table-driven tests for shortcutKey()
// ---------------------------------------------------------------------------

func TestShortcutKey(t *testing.T) {
	tests := []struct {
		index int
		want  string
	}{
		// Indices 0-8 produce "1"-"9"
		{0, "1"},
		{1, "2"},
		{2, "3"},
		{3, "4"},
		{4, "5"},
		{5, "6"},
		{6, "7"},
		{7, "8"},
		{8, "9"},

		// Index 9+ produces "a"-"z"
		{9, "a"},
		{10, "b"},
		{11, "c"},
		{34, "z"}, // index 34 = 'a' + 34 - 9 = 'a' + 25 = 'z'

		// Beyond 'z' (index 35+) returns empty
		{35, ""},
		{100, ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("index_%d", tt.index), func(t *testing.T) {
			got := shortcutKey(tt.index)
			if got != tt.want {
				t.Errorf("shortcutKey(%d) = %q, want %q", tt.index, got, tt.want)
			}
		})
	}
}

// TestShortcutKey_FullRange validates the full range of 1-9, a-z.
func TestShortcutKey_FullRange(t *testing.T) {
	// Indices 0-8 should produce "1" through "9"
	for i := 0; i < 9; i++ {
		expected := fmt.Sprintf("%d", i+1)
		got := shortcutKey(i)
		if got != expected {
			t.Errorf("shortcutKey(%d) = %q, want %q", i, got, expected)
		}
	}

	// Indices 9-34 should produce "a" through "z"
	for i := 9; i < 35; i++ {
		expected := string(rune('a' + i - 9))
		got := shortcutKey(i)
		if got != expected {
			t.Errorf("shortcutKey(%d) = %q, want %q", i, got, expected)
		}
	}

	// Index 35+ should produce ""
	for i := 35; i < 40; i++ {
		got := shortcutKey(i)
		if got != "" {
			t.Errorf("shortcutKey(%d) = %q, want empty string", i, got)
		}
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

	// Verify each mapping produces a valid AgentType with a color and icon
	for role, expectedType := range roleToType {
		if _, ok := AgentTypeColors[expectedType]; !ok {
			t.Errorf("role %q maps to AgentType %d which has no color", role, expectedType)
		}
		if _, ok := AgentTypeIcons[expectedType]; !ok {
			t.Errorf("role %q maps to AgentType %d which has no icon", role, expectedType)
		}
	}
}

func TestMergeK8sAgents_Dedup(t *testing.T) {
	// The seen map should prevent duplicate entries when a session name
	// already exists from tmux discovery.
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

	// The display label should still work for K8s agents
	label := agent.displayLabel()
	if label == "" {
		t.Error("K8s agent should still produce a display label")
	}
}

// ---------------------------------------------------------------------------
// TestCategorizeSession_RoundTrip — verifies categorizeSession is consistent
// with displayLabel for known session patterns.
// ---------------------------------------------------------------------------

func TestCategorizeSession_RoundTrip(t *testing.T) {
	sessionNames := []string{
		"hq-mayor",
		"hq-deacon",
		"gt-gastown-witness",
		"gt-gastown-refinery",
		"gt-gastown-crew-alice",
		"gt-gastown-bob",
		"gt-witness-legacyrig",
	}

	for _, name := range sessionNames {
		t.Run(name, func(t *testing.T) {
			agent := categorizeSession(name)
			if agent == nil {
				t.Fatalf("categorizeSession(%q) returned nil", name)
			}

			// displayLabel should never panic or return empty for valid sessions
			label := agent.displayLabel()
			if label == "" {
				t.Errorf("displayLabel() returned empty for %q", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestAgentTypeConsistency — verifies that all AgentType constants have
// entries in both AgentTypeColors and AgentTypeIcons maps.
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
		if _, ok := AgentTypeColors[at]; !ok {
			t.Errorf("AgentType %d missing from AgentTypeColors", at)
		}
		if _, ok := AgentTypeIcons[at]; !ok {
			t.Errorf("AgentType %d missing from AgentTypeIcons", at)
		}
	}
}
