package configbeads

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

func TestDeepMergeRoleConfig(t *testing.T) {
	tests := []struct {
		name     string
		dst      map[string]interface{}
		src      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "simple overwrite",
			dst:      map[string]interface{}{"role": "crew", "scope": "rig"},
			src:      map[string]interface{}{"scope": "town"},
			expected: map[string]interface{}{"role": "crew", "scope": "town"},
		},
		{
			name: "nested merge",
			dst: map[string]interface{}{
				"health": map[string]interface{}{
					"ping_timeout":    "30s",
					"stuck_threshold": "4h",
				},
			},
			src: map[string]interface{}{
				"health": map[string]interface{}{
					"stuck_threshold": "6h",
				},
			},
			expected: map[string]interface{}{
				"health": map[string]interface{}{
					"ping_timeout":    "30s",
					"stuck_threshold": "6h",
				},
			},
		},
		{
			name: "add new keys",
			dst:  map[string]interface{}{"role": "crew"},
			src:  map[string]interface{}{"nudge": "hello"},
			expected: map[string]interface{}{
				"role":  "crew",
				"nudge": "hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deepMergeRoleConfig(tt.dst, tt.src)

			dstJSON, _ := json.Marshal(tt.dst)
			expectedJSON, _ := json.Marshal(tt.expected)
			if string(dstJSON) != string(expectedJSON) {
				t.Errorf("got %s, want %s", string(dstJSON), string(expectedJSON))
			}
		})
	}
}

func TestRoleDefJSONRoundTrip(t *testing.T) {
	// Verify that RoleDefinition can round-trip through JSON.
	// This tests the json tags we added.
	for _, roleName := range config.AllRoles() {
		t.Run(roleName, func(t *testing.T) {
			def, err := config.LoadBuiltinRoleDefinition(roleName)
			if err != nil {
				t.Fatalf("LoadBuiltinRoleDefinition(%s) error: %v", roleName, err)
			}

			// Marshal to JSON
			jsonData, err := json.Marshal(def)
			if err != nil {
				t.Fatalf("json.Marshal error: %v", err)
			}

			// Unmarshal back
			var roundTripped config.RoleDefinition
			if err := json.Unmarshal(jsonData, &roundTripped); err != nil {
				t.Fatalf("json.Unmarshal error: %v", err)
			}

			// Verify key fields survived the round trip
			if roundTripped.Role != def.Role {
				t.Errorf("Role = %q, want %q", roundTripped.Role, def.Role)
			}
			if roundTripped.Scope != def.Scope {
				t.Errorf("Scope = %q, want %q", roundTripped.Scope, def.Scope)
			}
			if roundTripped.Session.Pattern != def.Session.Pattern {
				t.Errorf("Session.Pattern = %q, want %q", roundTripped.Session.Pattern, def.Session.Pattern)
			}
			if roundTripped.Session.WorkDir != def.Session.WorkDir {
				t.Errorf("Session.WorkDir = %q, want %q", roundTripped.Session.WorkDir, def.Session.WorkDir)
			}
			if roundTripped.Session.NeedsPreSync != def.Session.NeedsPreSync {
				t.Errorf("Session.NeedsPreSync = %v, want %v", roundTripped.Session.NeedsPreSync, def.Session.NeedsPreSync)
			}
			if roundTripped.Session.StartCommand != def.Session.StartCommand {
				t.Errorf("Session.StartCommand = %q, want %q", roundTripped.Session.StartCommand, def.Session.StartCommand)
			}
			if roundTripped.Health.PingTimeout.Duration != def.Health.PingTimeout.Duration {
				t.Errorf("Health.PingTimeout = %v, want %v", roundTripped.Health.PingTimeout.Duration, def.Health.PingTimeout.Duration)
			}
			if roundTripped.Health.ConsecutiveFailures != def.Health.ConsecutiveFailures {
				t.Errorf("Health.ConsecutiveFailures = %d, want %d", roundTripped.Health.ConsecutiveFailures, def.Health.ConsecutiveFailures)
			}
			if roundTripped.Health.KillCooldown.Duration != def.Health.KillCooldown.Duration {
				t.Errorf("Health.KillCooldown = %v, want %v", roundTripped.Health.KillCooldown.Duration, def.Health.KillCooldown.Duration)
			}
			if roundTripped.Health.StuckThreshold.Duration != def.Health.StuckThreshold.Duration {
				t.Errorf("Health.StuckThreshold = %v, want %v", roundTripped.Health.StuckThreshold.Duration, def.Health.StuckThreshold.Duration)
			}
			if roundTripped.Nudge != def.Nudge {
				t.Errorf("Nudge = %q, want %q", roundTripped.Nudge, def.Nudge)
			}
			if roundTripped.PromptTemplate != def.PromptTemplate {
				t.Errorf("PromptTemplate = %q, want %q", roundTripped.PromptTemplate, def.PromptTemplate)
			}

			// Verify env vars
			for k, v := range def.Env {
				if roundTripped.Env[k] != v {
					t.Errorf("Env[%s] = %q, want %q", k, roundTripped.Env[k], v)
				}
			}
		})
	}
}

func TestRoleDefJSONFieldNames(t *testing.T) {
	// Verify the JSON field names match the spec (snake_case).
	def, err := config.LoadBuiltinRoleDefinition("crew")
	if err != nil {
		t.Fatalf("LoadBuiltinRoleDefinition error: %v", err)
	}

	jsonData, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(jsonData, &raw); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	// Check top-level keys
	expectedKeys := []string{"role", "scope", "session", "env", "health", "nudge", "prompt_template"}
	for _, key := range expectedKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected JSON key %q", key)
		}
	}

	// Check session keys
	session, ok := raw["session"].(map[string]interface{})
	if !ok {
		t.Fatal("session is not a map")
	}
	sessionKeys := []string{"pattern", "work_dir", "needs_pre_sync", "start_command"}
	for _, key := range sessionKeys {
		if _, ok := session[key]; !ok {
			t.Errorf("missing expected session JSON key %q", key)
		}
	}

	// Check health keys
	health, ok := raw["health"].(map[string]interface{})
	if !ok {
		t.Fatal("health is not a map")
	}
	healthKeys := []string{"ping_timeout", "consecutive_failures", "kill_cooldown", "stuck_threshold"}
	for _, key := range healthKeys {
		if _, ok := health[key]; !ok {
			t.Errorf("missing expected health JSON key %q", key)
		}
	}

	// Verify Duration fields marshal as strings (not numbers)
	pingTimeout, ok := health["ping_timeout"].(string)
	if !ok {
		t.Errorf("ping_timeout should be a string, got %T", health["ping_timeout"])
	}
	if pingTimeout != "30s" {
		t.Errorf("ping_timeout = %q, want %q", pingTimeout, "30s")
	}
}
