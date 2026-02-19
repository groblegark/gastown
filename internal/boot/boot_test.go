package boot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	b := New("/home/agent/gt")
	if b.townRoot != "/home/agent/gt" {
		t.Errorf("townRoot = %q, want /home/agent/gt", b.townRoot)
	}
	if b.bootDir != "/home/agent/gt/deacon/dogs/boot" {
		t.Errorf("bootDir = %q, want /home/agent/gt/deacon/dogs/boot", b.bootDir)
	}
	if b.deaconDir != "/home/agent/gt/deacon" {
		t.Errorf("deaconDir = %q, want /home/agent/gt/deacon", b.deaconDir)
	}
	if b.backend == nil {
		t.Error("backend should not be nil")
	}
}

func TestDir(t *testing.T) {
	b := New("/tmp/testtown")
	if b.Dir() != "/tmp/testtown/deacon/dogs/boot" {
		t.Errorf("Dir() = %q", b.Dir())
	}
}

func TestDeaconDir(t *testing.T) {
	b := New("/tmp/testtown")
	if b.DeaconDir() != "/tmp/testtown/deacon" {
		t.Errorf("DeaconDir() = %q", b.DeaconDir())
	}
}

func TestIsDegraded(t *testing.T) {
	// Save and restore env
	orig := os.Getenv("GT_DEGRADED")
	defer os.Setenv("GT_DEGRADED", orig)

	os.Setenv("GT_DEGRADED", "true")
	b := New("/tmp/test")
	if !b.IsDegraded() {
		t.Error("IsDegraded() should be true when GT_DEGRADED=true")
	}

	os.Setenv("GT_DEGRADED", "")
	b = New("/tmp/test")
	if b.IsDegraded() {
		t.Error("IsDegraded() should be false when GT_DEGRADED is empty")
	}
}

func TestConstants(t *testing.T) {
	if SessionName != "gt-boot" {
		t.Errorf("SessionName = %q, want gt-boot", SessionName)
	}
	if MarkerFileName != ".boot-running" {
		t.Errorf("MarkerFileName = %q, want .boot-running", MarkerFileName)
	}
	if StatusFileName != ".boot-status.json" {
		t.Errorf("StatusFileName = %q, want .boot-status.json", StatusFileName)
	}
}

func TestSaveAndLoadStatus(t *testing.T) {
	tmpDir := t.TempDir()
	b := New(tmpDir)
	// Adjust bootDir to a writable location
	b.bootDir = filepath.Join(tmpDir, "boot")

	now := time.Now().UTC().Truncate(time.Second)
	status := &Status{
		Running:     true,
		StartedAt:   now,
		LastAction:  "wake",
		Target:      "deacon",
	}

	if err := b.SaveStatus(status); err != nil {
		t.Fatalf("SaveStatus() error: %v", err)
	}

	// Verify file exists
	data, err := os.ReadFile(b.statusPath())
	if err != nil {
		t.Fatalf("reading status file: %v", err)
	}

	var saved Status
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshaling status: %v", err)
	}
	if saved.LastAction != "wake" {
		t.Errorf("saved LastAction = %q, want wake", saved.LastAction)
	}
	if saved.Target != "deacon" {
		t.Errorf("saved Target = %q, want deacon", saved.Target)
	}

	// Load it back
	loaded, err := b.LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus() error: %v", err)
	}
	if loaded.LastAction != "wake" {
		t.Errorf("loaded LastAction = %q, want wake", loaded.LastAction)
	}
	if !loaded.Running {
		t.Error("loaded Running should be true")
	}
}

func TestLoadStatus_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	b := New(tmpDir)
	b.bootDir = filepath.Join(tmpDir, "nonexistent")

	status, err := b.LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus() error: %v", err)
	}
	if status.Running {
		t.Error("default status should not be running")
	}
	if status.LastAction != "" {
		t.Errorf("default LastAction = %q, want empty", status.LastAction)
	}
}

func TestAcquireAndReleaseLock(t *testing.T) {
	tmpDir := t.TempDir()
	b := New(tmpDir)
	b.bootDir = filepath.Join(tmpDir, "boot")

	// Acquire lock
	if err := b.AcquireLock(); err != nil {
		t.Fatalf("AcquireLock() error: %v", err)
	}

	// Marker file should exist
	if _, err := os.Stat(b.markerPath()); err != nil {
		t.Errorf("marker file should exist: %v", err)
	}

	// Release lock
	if err := b.ReleaseLock(); err != nil {
		t.Fatalf("ReleaseLock() error: %v", err)
	}

	// Marker file should be gone
	if _, err := os.Stat(b.markerPath()); !os.IsNotExist(err) {
		t.Error("marker file should not exist after release")
	}
}

func TestBackend(t *testing.T) {
	b := New("/tmp/test")
	if b.Backend() == nil {
		t.Error("Backend() should not return nil")
	}
}
