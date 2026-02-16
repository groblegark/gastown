package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

func TestDiscoverHooksSkipsPolecatDotDirs(t *testing.T) {
	townRoot := setupTestTownForDotDir(t)
	rigPath := filepath.Join(townRoot, "gastown")

	settingsPath := filepath.Join(rigPath, "polecats", ".claude", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}

	settings := `{"hooks":{"SessionStart":[{"matcher":"*","hooks":[{"type":"Stop","command":"echo hi"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(settings), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	hooks, err := discoverHooks(townRoot)
	if err != nil {
		t.Fatalf("discoverHooks: %v", err)
	}

	if len(hooks) != 0 {
		t.Fatalf("expected no hooks, got %d", len(hooks))
	}
}

func addRigEntry(t *testing.T, townRoot, rigName string) {
	t.Helper()

	rigsPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsPath)
	if err != nil {
		t.Fatalf("load rigs.json: %v", err)
	}
	if rigsConfig.Rigs == nil {
		rigsConfig.Rigs = make(map[string]config.RigEntry)
	}
	rigsConfig.Rigs[rigName] = config.RigEntry{
		GitURL:  "file:///dev/null",
		AddedAt: time.Now(),
	}
	if err := config.SaveRigsConfig(rigsPath, rigsConfig); err != nil {
		t.Fatalf("save rigs.json: %v", err)
	}
}

func setupTestTownForDotDir(t *testing.T) string {
	t.Helper()

	townRoot := t.TempDir()

	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}

	rigsPath := filepath.Join(mayorDir, "rigs.json")
	rigsConfig := &config.RigsConfig{
		Version: 1,
		Rigs:    make(map[string]config.RigEntry),
	}
	if err := config.SaveRigsConfig(rigsPath, rigsConfig); err != nil {
		t.Fatalf("save rigs.json: %v", err)
	}

	beadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}

	return townRoot
}

func writeScript(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
