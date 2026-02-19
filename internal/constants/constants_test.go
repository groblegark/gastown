package constants

import (
	"slices"
	"strings"
	"testing"
)

func TestRoleEmoji(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{RoleMayor, EmojiMayor},
		{RoleDeacon, EmojiDeacon},
		{RoleWitness, EmojiWitness},
		{RoleRefinery, EmojiRefinery},
		{RoleCrew, EmojiCrew},
		{RolePolecat, EmojiPolecat},
		{RoleBoot, EmojiBoot},
		{"unknown", "❓"},
		{"", "❓"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := RoleEmoji(tt.role)
			if got != tt.want {
				t.Errorf("RoleEmoji(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestBeadsCustomTypesList(t *testing.T) {
	types := BeadsCustomTypesList()
	if len(types) == 0 {
		t.Fatal("BeadsCustomTypesList() returned empty slice")
	}

	// Verify the list matches the comma-separated constant
	joined := strings.Join(types, ",")
	if joined != BeadsCustomTypes {
		t.Errorf("BeadsCustomTypesList() joined = %q, want %q", joined, BeadsCustomTypes)
	}

	// Verify known types are present
	expected := []string{"agent", "role", "rig", "convoy", "slot", "queue", "event", "message", "molecule", "gate", "merge-request", "config", "route"}
	for _, e := range expected {
		if !slices.Contains(types, e) {
			t.Errorf("BeadsCustomTypesList() missing expected type %q", e)
		}
	}
}

func TestPathHelpers(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) string
		arg  string
		want string
	}{
		{"MayorRigsPath", MayorRigsPath, "/home/agent/gt", "/home/agent/gt/mayor/rigs.json"},
		{"MayorTownPath", MayorTownPath, "/home/agent/gt", "/home/agent/gt/mayor/town.json"},
		{"RigMayorPath", RigMayorPath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/mayor/rig"},
		{"RigBeadsPath", RigBeadsPath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/mayor/rig/.beads"},
		{"RigPolecatsPath", RigPolecatsPath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/polecats"},
		{"RigCrewPath", RigCrewPath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/crew"},
		{"MayorConfigPath", MayorConfigPath, "/home/agent/gt", "/home/agent/gt/mayor/config.json"},
		{"TownRuntimePath", TownRuntimePath, "/home/agent/gt", "/home/agent/gt/.runtime"},
		{"RigRuntimePath", RigRuntimePath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/.runtime"},
		{"RigSettingsPath", RigSettingsPath, "/home/agent/gt/gastown", "/home/agent/gt/gastown/settings"},
		{"MayorAccountsPath", MayorAccountsPath, "/home/agent/gt", "/home/agent/gt/mayor/accounts.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn(tt.arg)
			if got != tt.want {
				t.Errorf("%s(%q) = %q, want %q", tt.name, tt.arg, got, tt.want)
			}
		})
	}
}

func TestDirectoryConstants(t *testing.T) {
	// Verify directory constants haven't been accidentally changed
	if DirMayor != "mayor" {
		t.Errorf("DirMayor = %q, want mayor", DirMayor)
	}
	if DirPolecats != "polecats" {
		t.Errorf("DirPolecats = %q, want polecats", DirPolecats)
	}
	if DirBeads != ".beads" {
		t.Errorf("DirBeads = %q, want .beads", DirBeads)
	}
	if DirRuntime != ".runtime" {
		t.Errorf("DirRuntime = %q, want .runtime", DirRuntime)
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleMayor != "mayor" {
		t.Errorf("RoleMayor = %q, want mayor", RoleMayor)
	}
	if RolePolecat != "polecat" {
		t.Errorf("RolePolecat = %q, want polecat", RolePolecat)
	}
}

func TestBranchConstants(t *testing.T) {
	if BranchMain != "main" {
		t.Errorf("BranchMain = %q, want main", BranchMain)
	}
	if BranchPolecatPrefix != "polecat/" {
		t.Errorf("BranchPolecatPrefix = %q, want polecat/", BranchPolecatPrefix)
	}
}

func TestSupportedShells(t *testing.T) {
	if len(SupportedShells) == 0 {
		t.Fatal("SupportedShells is empty")
	}
	// bash should always be supported
	if !slices.Contains(SupportedShells, "bash") {
		t.Error("SupportedShells should contain bash")
	}
}
