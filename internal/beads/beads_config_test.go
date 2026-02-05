package beads

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigBeadID(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"hooks-base", "hq-cfg-hooks-base"},
		{"town-gt11", "hq-cfg-town-gt11"},
		{"rig-gt11-gastown", "hq-cfg-rig-gt11-gastown"},
		{"mcp-global", "hq-cfg-mcp-global"},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			if got := ConfigBeadID(tt.slug); got != tt.want {
				t.Errorf("ConfigBeadID(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}

func TestConfigScopeLabels(t *testing.T) {
	tests := []struct {
		name  string
		rig   string
		role  string
		agent string
		want  []string
	}{
		{
			name: "global scope",
			rig:  "*",
			want: []string{"scope:global"},
		},
		{
			name: "town scope",
			rig:  "gt11",
			want: []string{"town:gt11"},
		},
		{
			name: "rig scope",
			rig:  "gt11/gastown",
			want: []string{"town:gt11", "rig:gastown"},
		},
		{
			name: "global with role",
			rig:  "*",
			role: "crew",
			want: []string{"scope:global", "role:crew"},
		},
		{
			name:  "rig with role",
			rig:   "gt11/gastown",
			role:  "crew",
			want:  []string{"town:gt11", "rig:gastown", "role:crew"},
		},
		{
			name:  "rig with agent",
			rig:   "gt11/gastown",
			agent: "slack",
			want:  []string{"town:gt11", "rig:gastown", "agent:slack"},
		},
		{
			name: "empty rig",
			rig:  "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigScopeLabels(tt.rig, tt.role, tt.agent)
			if len(got) != len(tt.want) {
				t.Fatalf("ConfigScopeLabels(%q, %q, %q) = %v, want %v", tt.rig, tt.role, tt.agent, got, tt.want)
			}
			for i, label := range got {
				if label != tt.want[i] {
					t.Errorf("label[%d] = %q, want %q", i, label, tt.want[i])
				}
			}
		})
	}
}

func TestFormatConfigDescription(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		fields *ConfigFields
		want   []string // Lines that should be present
	}{
		{
			name:  "full config",
			title: "Claude Hooks: base",
			fields: &ConfigFields{
				Rig:       "*",
				Category:  "claude-hooks",
				Metadata:  `{"hooks":{"PreCompact":[{"command":"gt prime --hook"}]}}`,
				CreatedBy: "gastown/crew/slack",
				CreatedAt: "2026-02-05T04:00:00Z",
			},
			want: []string{
				"Claude Hooks: base",
				"rig: *",
				"category: claude-hooks",
				"created_by: gastown/crew/slack",
				"created_at: 2026-02-05T04:00:00Z",
				`metadata: {"hooks":{"PreCompact":[{"command":"gt prime --hook"}]}}`,
			},
		},
		{
			name:  "rig-scoped config",
			title: "Rig: gastown",
			fields: &ConfigFields{
				Rig:       "gt11/gastown",
				Category:  "rig-registry",
				Metadata:  `{"git_url":"git@gitlab.com:foo/gastown.git","prefix":"gt"}`,
				CreatedBy: "admin",
				CreatedAt: "2026-01-01T00:00:00Z",
				UpdatedBy: "gastown/crew/max",
				UpdatedAt: "2026-02-01T00:00:00Z",
			},
			want: []string{
				"rig: gt11/gastown",
				"category: rig-registry",
				"updated_by: gastown/crew/max",
				"updated_at: 2026-02-01T00:00:00Z",
			},
		},
		{
			name:  "empty rig defaults to global",
			title: "Global Config",
			fields: &ConfigFields{
				Category: "mcp",
				Metadata: `{"servers":{}}`,
			},
			want: []string{
				"rig: *",
				"category: mcp",
			},
		},
		{
			name:   "nil fields",
			title:  "Just a title",
			fields: nil,
			want:   []string{"Just a title"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatConfigDescription(tt.title, tt.fields)
			for _, line := range tt.want {
				if !strings.Contains(got, line) {
					t.Errorf("FormatConfigDescription() missing line %q\ngot:\n%s", line, got)
				}
			}
		})
	}
}

func TestParseConfigFields(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        *ConfigFields
	}{
		{
			name: "full config",
			description: `Claude Hooks: base

rig: *
category: claude-hooks
created_by: gastown/crew/slack
created_at: 2026-02-05T04:00:00Z
metadata: {"hooks":{"PreCompact":[{"command":"gt prime --hook"}]}}`,
			want: &ConfigFields{
				Rig:       "*",
				Category:  "claude-hooks",
				Metadata:  `{"hooks":{"PreCompact":[{"command":"gt prime --hook"}]}}`,
				CreatedBy: "gastown/crew/slack",
				CreatedAt: "2026-02-05T04:00:00Z",
			},
		},
		{
			name: "rig-scoped",
			description: `Rig: gastown

rig: gt11/gastown
category: rig-registry
created_by: admin
metadata: {"git_url":"git@gitlab.com:foo/gastown.git","prefix":"gt"}`,
			want: &ConfigFields{
				Rig:       "gt11/gastown",
				Category:  "rig-registry",
				Metadata:  `{"git_url":"git@gitlab.com:foo/gastown.git","prefix":"gt"}`,
				CreatedBy: "admin",
			},
		},
		{
			name: "with update fields",
			description: `Config

rig: gt11
category: identity
created_by: admin
created_at: 2026-01-01T00:00:00Z
updated_by: gastown/crew/max
updated_at: 2026-02-01T00:00:00Z
metadata: {"name":"gt11"}`,
			want: &ConfigFields{
				Rig:       "gt11",
				Category:  "identity",
				Metadata:  `{"name":"gt11"}`,
				CreatedBy: "admin",
				CreatedAt: "2026-01-01T00:00:00Z",
				UpdatedBy: "gastown/crew/max",
				UpdatedAt: "2026-02-01T00:00:00Z",
			},
		},
		{
			name:        "empty description",
			description: "",
			want:        &ConfigFields{Rig: "*"},
		},
		{
			name: "metadata with nested JSON",
			description: `Config

rig: *
category: mcp
metadata: {"mcpServers":{"playwright":{"command":"npx","args":["-y","@playwright/mcp@latest"]}}}`,
			want: &ConfigFields{
				Rig:      "*",
				Category: "mcp",
				Metadata: `{"mcpServers":{"playwright":{"command":"npx","args":["-y","@playwright/mcp@latest"]}}}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseConfigFields(tt.description)
			if got.Rig != tt.want.Rig {
				t.Errorf("Rig = %q, want %q", got.Rig, tt.want.Rig)
			}
			if got.Category != tt.want.Category {
				t.Errorf("Category = %q, want %q", got.Category, tt.want.Category)
			}
			if got.Metadata != tt.want.Metadata {
				t.Errorf("Metadata = %q, want %q", got.Metadata, tt.want.Metadata)
			}
			if got.CreatedBy != tt.want.CreatedBy {
				t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, tt.want.CreatedBy)
			}
			if got.CreatedAt != tt.want.CreatedAt {
				t.Errorf("CreatedAt = %q, want %q", got.CreatedAt, tt.want.CreatedAt)
			}
			if got.UpdatedBy != tt.want.UpdatedBy {
				t.Errorf("UpdatedBy = %q, want %q", got.UpdatedBy, tt.want.UpdatedBy)
			}
			if got.UpdatedAt != tt.want.UpdatedAt {
				t.Errorf("UpdatedAt = %q, want %q", got.UpdatedAt, tt.want.UpdatedAt)
			}
		})
	}
}

func TestConfigRoundTrip(t *testing.T) {
	original := &ConfigFields{
		Rig:       "gt11/gastown",
		Category:  "claude-hooks",
		Metadata:  `{"hooks":{"SessionStart":[{"command":"gt prime --hook"}],"Stop":[{"command":"gt costs record"}]}}`,
		CreatedBy: "gastown/crew/slack",
		CreatedAt: "2026-02-05T04:00:00Z",
		UpdatedBy: "gastown/crew/max",
		UpdatedAt: "2026-02-05T05:00:00Z",
	}

	description := FormatConfigDescription("Claude Hooks: gastown-crew", original)
	parsed := ParseConfigFields(description)

	if parsed.Rig != original.Rig {
		t.Errorf("Rig: got %q, want %q", parsed.Rig, original.Rig)
	}
	if parsed.Category != original.Category {
		t.Errorf("Category: got %q, want %q", parsed.Category, original.Category)
	}
	if parsed.Metadata != original.Metadata {
		t.Errorf("Metadata: got %q, want %q", parsed.Metadata, original.Metadata)
	}
	if parsed.CreatedBy != original.CreatedBy {
		t.Errorf("CreatedBy: got %q, want %q", parsed.CreatedBy, original.CreatedBy)
	}
	if parsed.CreatedAt != original.CreatedAt {
		t.Errorf("CreatedAt: got %q, want %q", parsed.CreatedAt, original.CreatedAt)
	}
	if parsed.UpdatedBy != original.UpdatedBy {
		t.Errorf("UpdatedBy: got %q, want %q", parsed.UpdatedBy, original.UpdatedBy)
	}
	if parsed.UpdatedAt != original.UpdatedAt {
		t.Errorf("UpdatedAt: got %q, want %q", parsed.UpdatedAt, original.UpdatedAt)
	}
}

func TestConfigRoundTripComplexMetadata(t *testing.T) {
	// Test with deeply nested JSON containing special characters
	metadata := map[string]interface{}{
		"hooks": map[string]interface{}{
			"UserPromptSubmit": []map[string]string{
				{"command": `_stdin=$(cat) && echo "$_stdin" | gt nudge drain --quiet`},
			},
			"PreToolUse": []map[string]interface{}{
				{
					"matcher": "Bash(gh pr create*)",
					"command": "gt tap guard pr-workflow",
				},
			},
		},
		"editorMode": "normal",
	}
	metadataJSON, _ := json.Marshal(metadata)

	original := &ConfigFields{
		Rig:       "*",
		Category:  "claude-hooks",
		Metadata:  string(metadataJSON),
		CreatedBy: "admin",
		CreatedAt: "2026-02-05T00:00:00Z",
	}

	description := FormatConfigDescription("Claude Hooks: complex", original)
	parsed := ParseConfigFields(description)

	// Verify JSON equivalence (order may differ)
	var origMap, parsedMap map[string]interface{}
	if err := json.Unmarshal([]byte(original.Metadata), &origMap); err != nil {
		t.Fatalf("unmarshal original metadata: %v", err)
	}
	if err := json.Unmarshal([]byte(parsed.Metadata), &parsedMap); err != nil {
		t.Fatalf("unmarshal parsed metadata: %v\nparsed.Metadata = %q", err, parsed.Metadata)
	}

	origJSON, _ := json.Marshal(origMap)
	parsedJSON, _ := json.Marshal(parsedMap)
	if string(origJSON) != string(parsedJSON) {
		t.Errorf("Metadata JSON mismatch:\n  got:  %s\n  want: %s", parsedJSON, origJSON)
	}
}

func TestValidateConfigFields(t *testing.T) {
	tests := []struct {
		name    string
		fields  *ConfigFields
		wantErr string
	}{
		{
			name:    "nil fields",
			fields:  nil,
			wantErr: "config fields are required",
		},
		{
			name:    "missing rig",
			fields:  &ConfigFields{Category: "mcp", Metadata: "{}"},
			wantErr: "rig field is required",
		},
		{
			name:    "missing category",
			fields:  &ConfigFields{Rig: "*", Metadata: "{}"},
			wantErr: "category field is required",
		},
		{
			name:    "invalid category",
			fields:  &ConfigFields{Rig: "*", Category: "invalid", Metadata: "{}"},
			wantErr: "invalid config category",
		},
		{
			name:    "missing metadata",
			fields:  &ConfigFields{Rig: "*", Category: "mcp"},
			wantErr: "metadata field is required",
		},
		{
			name:    "invalid JSON metadata",
			fields:  &ConfigFields{Rig: "*", Category: "mcp", Metadata: "not json"},
			wantErr: "metadata must be valid JSON",
		},
		{
			name:   "valid fields",
			fields: &ConfigFields{Rig: "*", Category: "mcp", Metadata: `{"servers":{}}`},
		},
		{
			name:   "valid with rig scope",
			fields: &ConfigFields{Rig: "gt11/gastown", Category: "claude-hooks", Metadata: `{"hooks":{}}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfigFields(tt.fields)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validateConfigFields() unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("validateConfigFields() expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("validateConfigFields() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestCompactJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already compact",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "with whitespace",
			input: `{ "key" : "value" , "nested" : { "a" : 1 } }`,
			want:  `{"key":"value","nested":{"a":1}}`,
		},
		{
			name:  "multiline JSON",
			input: "{\n  \"key\": \"value\"\n}",
			want:  `{"key":"value"}`,
		},
		{
			name:  "invalid JSON returns original",
			input: "not json",
			want:  "not json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactJSON(tt.input); got != tt.want {
				t.Errorf("compactJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigScopeScore(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		town   string
		rig    string
		role   string
		agent  string
		want   int
	}{
		{
			name:   "global match",
			labels: []string{"scope:global", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			want:   0,
		},
		{
			name:   "global with role match",
			labels: []string{"scope:global", "role:crew", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			role:   "crew",
			want:   1,
		},
		{
			name:   "rig match",
			labels: []string{"town:gt11", "rig:gastown", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			want:   2,
		},
		{
			name:   "rig + role match",
			labels: []string{"town:gt11", "rig:gastown", "role:crew", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			role:   "crew",
			want:   3,
		},
		{
			name:   "agent match (most specific)",
			labels: []string{"town:gt11", "rig:gastown", "agent:slack", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			agent:  "slack",
			want:   4,
		},
		{
			name:   "no match - wrong town",
			labels: []string{"town:gt12", "rig:gastown", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			want:   -1,
		},
		{
			name:   "no match - wrong rig",
			labels: []string{"town:gt11", "rig:beads", "config:claude-hooks"},
			town:   "gt11",
			rig:    "gastown",
			want:   -1,
		},
		{
			name:   "town-only match",
			labels: []string{"town:gt11", "config:identity"},
			town:   "gt11",
			rig:    "gastown",
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{Labels: tt.labels}
			got := configScopeScore(issue, tt.town, tt.rig, tt.role, tt.agent)
			if got != tt.want {
				t.Errorf("configScopeScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidConfigCategories(t *testing.T) {
	expected := []string{
		"identity", "claude-hooks", "mcp", "rig-registry",
		"agent-preset", "role-definition", "slack-routing",
		"accounts", "daemon", "messaging", "escalation",
	}

	for _, cat := range expected {
		if !ValidConfigCategories[cat] {
			t.Errorf("ValidConfigCategories missing %q", cat)
		}
	}

	if ValidConfigCategories["nonexistent"] {
		t.Error("ValidConfigCategories should not contain 'nonexistent'")
	}
}
