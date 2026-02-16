package cmd

import (
	"encoding/json"
	"testing"
	"time"
)

// Tests for gt plugin status command (beads-zndq)

func TestPluginStatusOutput_JSONRoundtrip(t *testing.T) {
	output := PluginStatusOutput{
		Name:     "merge-oracle",
		Location: "/path/to/plugin",
		RigName:  "gastown",
		GateType: "cooldown",
		GateOpen: true,
		GateInfo: "cooldown expired",
	}
	output.Stats.Total = 10
	output.Stats.Success = 8
	output.Stats.Failures = 1
	output.Stats.Skipped = 1

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed PluginStatusOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.Name != "merge-oracle" {
		t.Errorf("Name = %q, want %q", parsed.Name, "merge-oracle")
	}
	if parsed.GateType != "cooldown" {
		t.Errorf("GateType = %q, want %q", parsed.GateType, "cooldown")
	}
	if !parsed.GateOpen {
		t.Error("expected GateOpen to be true")
	}
	if parsed.Stats.Total != 10 {
		t.Errorf("Stats.Total = %d, want 10", parsed.Stats.Total)
	}
	if parsed.Stats.Success != 8 {
		t.Errorf("Stats.Success = %d, want 8", parsed.Stats.Success)
	}
}

func TestPluginStatusOutput_OmitsEmpty(t *testing.T) {
	output := PluginStatusOutput{
		Name:     "test-plugin",
		Location: "/path",
		GateType: "manual",
		GateOpen: true,
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["rig_name"]; ok {
		t.Error("expected rig_name to be omitted when empty")
	}
	if _, ok := raw["gate_info"]; ok {
		t.Error("expected gate_info to be omitted when empty")
	}
	if _, ok := raw["last_run"]; ok {
		t.Error("expected last_run to be omitted when nil")
	}

	if _, ok := raw["name"]; !ok {
		t.Error("expected name to be present")
	}
	if _, ok := raw["gate_type"]; !ok {
		t.Error("expected gate_type to be present")
	}
}

func TestPluginStatusOutput_WithLastRun(t *testing.T) {
	output := PluginStatusOutput{
		Name:     "test-plugin",
		Location: "/path",
		GateType: "cron",
		GateOpen: false,
		GateInfo: "next run in 5m",
	}
	output.LastRun = &struct {
		ID        string    `json:"id"`
		Result    string    `json:"result"`
		Timestamp time.Time `json:"timestamp"`
		Age       string    `json:"age"`
	}{
		ID:        "run-123",
		Result:    "success",
		Timestamp: time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC),
		Age:       "2h ago",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed PluginStatusOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.LastRun == nil {
		t.Fatal("expected LastRun to be non-nil")
	}
	if parsed.LastRun.ID != "run-123" {
		t.Errorf("LastRun.ID = %q, want %q", parsed.LastRun.ID, "run-123")
	}
	if parsed.LastRun.Result != "success" {
		t.Errorf("LastRun.Result = %q, want %q", parsed.LastRun.Result, "success")
	}
}

func TestPluginStatusOutput_GateTypes(t *testing.T) {
	gateTypes := []string{"cooldown", "cron", "condition", "event", "manual"}

	for _, gt := range gateTypes {
		t.Run(gt, func(t *testing.T) {
			output := PluginStatusOutput{
				Name:     "test-" + gt,
				Location: "/path",
				GateType: gt,
				GateOpen: true,
			}

			data, err := json.Marshal(output)
			if err != nil {
				t.Fatalf("Marshal error for gate type %q: %v", gt, err)
			}

			var parsed PluginStatusOutput
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Unmarshal error for gate type %q: %v", gt, err)
			}

			if parsed.GateType != gt {
				t.Errorf("GateType round-trip: got %q, want %q", parsed.GateType, gt)
			}
		})
	}
}

func TestPluginStatusOutput_StatsZeroValues(t *testing.T) {
	output := PluginStatusOutput{
		Name:     "idle-plugin",
		Location: "/path",
		GateType: "manual",
		GateOpen: true,
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["stats"]; !ok {
		t.Error("expected stats to be present even with zero values")
	}
}

func TestPluginStatusOutput_GateClosed(t *testing.T) {
	output := PluginStatusOutput{
		Name:     "gated-plugin",
		Location: "/path",
		GateType: "cooldown",
		GateOpen: false,
		GateInfo: "cooldown active, 3m remaining",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed PluginStatusOutput
	json.Unmarshal(data, &parsed)

	if parsed.GateOpen {
		t.Error("expected GateOpen to be false")
	}
	if parsed.GateInfo != "cooldown active, 3m remaining" {
		t.Errorf("GateInfo = %q, want %q", parsed.GateInfo, "cooldown active, 3m remaining")
	}
}
