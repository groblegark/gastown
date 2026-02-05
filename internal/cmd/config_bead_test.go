package cmd

import (
	"encoding/json"
	"testing"
)

func TestDeepMerge(t *testing.T) {
	t.Run("simple key override", func(t *testing.T) {
		dst := map[string]interface{}{
			"a": "old",
			"b": "keep",
		}
		src := map[string]interface{}{
			"a": "new",
		}
		deepMerge(dst, src)

		if dst["a"] != "new" {
			t.Errorf("a = %v, want new", dst["a"])
		}
		if dst["b"] != "keep" {
			t.Errorf("b = %v, want keep", dst["b"])
		}
	})

	t.Run("nested map merge", func(t *testing.T) {
		dst := map[string]interface{}{
			"hooks": map[string]interface{}{
				"PreToolUse": []interface{}{"a"},
				"Stop":       []interface{}{"b"},
			},
		}
		src := map[string]interface{}{
			"hooks": map[string]interface{}{
				"PreToolUse": []interface{}{"c"},
			},
		}
		deepMerge(dst, src)

		hooks := dst["hooks"].(map[string]interface{})
		if hooks["Stop"] == nil {
			t.Error("Stop was deleted but should be preserved")
		}
		// PreToolUse is overridden (not merged - that's deep merge's behavior)
		pre := hooks["PreToolUse"].([]interface{})
		if len(pre) != 1 || pre[0] != "c" {
			t.Errorf("PreToolUse = %v, want [c]", pre)
		}
	})

	t.Run("null deletes key", func(t *testing.T) {
		dst := map[string]interface{}{
			"a": "value",
			"b": "keep",
		}
		src := map[string]interface{}{
			"a": nil,
		}
		deepMerge(dst, src)

		if _, ok := dst["a"]; ok {
			t.Error("key 'a' should have been deleted by nil")
		}
		if dst["b"] != "keep" {
			t.Errorf("b = %v, want keep", dst["b"])
		}
	})

	t.Run("new key added", func(t *testing.T) {
		dst := map[string]interface{}{
			"existing": "value",
		}
		src := map[string]interface{}{
			"new_key": "new_value",
		}
		deepMerge(dst, src)

		if dst["new_key"] != "new_value" {
			t.Errorf("new_key = %v, want new_value", dst["new_key"])
		}
	})
}

func TestParseMaterializeScope(t *testing.T) {
	tests := []struct {
		scope string
		town  string
		rig   string
		role  string
		agent string
	}{
		{"", "", "", "", ""},
		{"gt11", "gt11", "", "", ""},
		{"gt11/gastown", "gt11", "gastown", "", ""},
		{"gt11/gastown/crew", "gt11", "gastown", "crew", ""},
		{"gt11/gastown/crew/slack", "gt11", "gastown", "crew", "slack"},
	}

	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			town, rig, role, agent := parseMaterializeScope(tt.scope)
			if town != tt.town {
				t.Errorf("town = %q, want %q", town, tt.town)
			}
			if rig != tt.rig {
				t.Errorf("rig = %q, want %q", rig, tt.rig)
			}
			if role != tt.role {
				t.Errorf("role = %q, want %q", role, tt.role)
			}
			if agent != tt.agent {
				t.Errorf("agent = %q, want %q", agent, tt.agent)
			}
		})
	}
}

func TestConfigBeadListItem_JSON(t *testing.T) {
	item := ConfigBeadListItem{
		ID:       "hq-cfg-hooks-base",
		Slug:     "hooks-base",
		Category: "claude-hooks",
		Rig:      "*",
		Title:    "claude-hooks: hooks-base",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ConfigBeadListItem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != item.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, item.ID)
	}
	if decoded.Category != item.Category {
		t.Errorf("Category = %q, want %q", decoded.Category, item.Category)
	}
}

func TestConfigBeadDetail_JSON(t *testing.T) {
	detail := ConfigBeadDetail{
		ID:        "hq-cfg-hooks-base",
		Slug:      "hooks-base",
		Category:  "claude-hooks",
		Rig:       "*",
		Labels:    []string{"gt:config", "config:claude-hooks", "scope:global"},
		CreatedBy: "gastown/crew/config",
		CreatedAt: "2026-02-05T00:00:00Z",
		Metadata:  map[string]interface{}{"hooks": map[string]interface{}{}},
	}

	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ConfigBeadDetail
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != detail.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, detail.ID)
	}
	if decoded.Category != detail.Category {
		t.Errorf("Category = %q, want %q", decoded.Category, detail.Category)
	}
	if len(decoded.Labels) != 3 {
		t.Errorf("Labels count = %d, want 3", len(decoded.Labels))
	}
}

func TestRunConfigBeadCreate_Validation(t *testing.T) {
	t.Run("rejects missing category", func(t *testing.T) {
		cmd := configBeadCreateCmd
		cmd.Flags().Set("metadata", `{"key":"value"}`)
		cmd.Flags().Set("category", "")
		err := runConfigBeadCreate(cmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for missing category")
		}
	})

	t.Run("rejects invalid category", func(t *testing.T) {
		cmd := configBeadCreateCmd
		cmd.Flags().Set("metadata", `{"key":"value"}`)
		cmd.Flags().Set("category", "invalid-category")
		err := runConfigBeadCreate(cmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for invalid category")
		}
	})

	t.Run("rejects invalid JSON metadata", func(t *testing.T) {
		cmd := configBeadCreateCmd
		cmd.Flags().Set("metadata", "not-json")
		cmd.Flags().Set("category", "claude-hooks")
		err := runConfigBeadCreate(cmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("rejects empty metadata", func(t *testing.T) {
		cmd := configBeadCreateCmd
		cmd.Flags().Set("metadata", "")
		cmd.Flags().Set("category", "claude-hooks")
		err := runConfigBeadCreate(cmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for empty metadata")
		}
	})
}

func TestRunConfigBeadUpdate_Validation(t *testing.T) {
	t.Run("rejects no update flags", func(t *testing.T) {
		configBeadUpdateMeta = ""
		configBeadUpdateMerge = ""
		err := runConfigBeadUpdate(configBeadUpdateCmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error when no update flags specified")
		}
	})

	t.Run("rejects invalid metadata JSON", func(t *testing.T) {
		configBeadUpdateMeta = "not-json"
		defer func() { configBeadUpdateMeta = "" }()
		err := runConfigBeadUpdate(configBeadUpdateCmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("rejects invalid merge-metadata JSON", func(t *testing.T) {
		configBeadUpdateMerge = "not-json"
		defer func() { configBeadUpdateMerge = "" }()
		err := runConfigBeadUpdate(configBeadUpdateCmd, []string{"test-slug"})
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestRunConfigMaterialize_Validation(t *testing.T) {
	t.Run("rejects no type flags", func(t *testing.T) {
		materializeHooks = false
		materializeMCP = false
		err := runConfigMaterialize(configMaterializeCmd, nil)
		if err == nil {
			t.Fatal("expected error when no type flags specified")
		}
	})
}
