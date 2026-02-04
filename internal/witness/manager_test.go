package witness

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

func TestBuildWitnessStartCommand_UsesRoleConfig(t *testing.T) {
	roleConfig := &beads.RoleConfig{
		StartCommand: "exec run --town {town} --rig {rig} --role {role}",
	}

	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "", roleConfig)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}

	want := "exec run --town /town --rig gastown --role witness"
	if got != want {
		t.Errorf("buildWitnessStartCommand = %q, want %q", got, want)
	}
}

func TestBuildWitnessStartCommand_DefaultsToRuntime(t *testing.T) {
	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "", nil)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}

	if !strings.Contains(got, "GT_ROLE=gastown/witness") {
		t.Errorf("expected GT_ROLE=gastown/witness in command, got %q", got)
	}
	if !strings.Contains(got, "BD_ACTOR=gastown/witness") {
		t.Errorf("expected BD_ACTOR=gastown/witness in command, got %q", got)
	}
}

func TestBuildWitnessStartCommand_AgentOverrideWins(t *testing.T) {
	roleConfig := &beads.RoleConfig{
		StartCommand: "exec run --role {role}",
	}

	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "codex", roleConfig)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}
	if strings.Contains(got, "exec run") {
		t.Fatalf("expected agent override to bypass role start_command, got %q", got)
	}
	if !strings.Contains(got, "GT_ROLE=gastown/witness") {
		t.Errorf("expected GT_ROLE=gastown/witness in command, got %q", got)
	}
}

func TestWitnessStartPassesBDDaemonHostFromEnvironment(t *testing.T) {
	// This test verifies that witness Start() reads BD_DAEMON_HOST from the environment
	// and passes it to AgentEnv for inclusion in the agent's environment variables.
	//
	// We can't easily test the full Start() flow without a real tmux session,
	// but we can verify the config.AgentEnv call pattern that Start() uses.

	testCases := []struct {
		name         string
		envValue     string
		expectInEnv  bool
		expectedVal  string
	}{
		{
			name:         "BD_DAEMON_HOST set",
			envValue:     "192.168.1.100:7233",
			expectInEnv:  true,
			expectedVal:  "192.168.1.100:7233",
		},
		{
			name:         "BD_DAEMON_HOST empty",
			envValue:     "",
			expectInEnv:  false,
			expectedVal:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate what witness.Start() does: read from os.Getenv and pass to AgentEnv
			cfg := config.AgentEnvConfig{
				Role:         "witness",
				Rig:          "testrig",
				TownRoot:     "/town",
				BDDaemonHost: tc.envValue, // This is what os.Getenv("BD_DAEMON_HOST") returns
			}
			env := config.AgentEnv(cfg)

			if tc.expectInEnv {
				if got, ok := env["BD_DAEMON_HOST"]; !ok {
					t.Errorf("expected BD_DAEMON_HOST in env, but not found")
				} else if got != tc.expectedVal {
					t.Errorf("BD_DAEMON_HOST = %q, want %q", got, tc.expectedVal)
				}
			} else {
				if _, ok := env["BD_DAEMON_HOST"]; ok {
					t.Errorf("expected BD_DAEMON_HOST to not be in env when empty")
				}
			}
		})
	}
}
