package mail

import (
	"path/filepath"
	"testing"
)

func TestNewMailboxBeads(t *testing.T) {
	m := NewMailboxBeads("gastown/Toast", "/work/dir")
	if m.identity != "gastown/Toast" {
		t.Errorf("identity = %q, want %q", m.identity, "gastown/Toast")
	}
}

func TestMailboxBeadsIdentity(t *testing.T) {
	beads := NewMailboxBeads("gastown/Toast", "/work/dir")
	if beads.Identity() != "gastown/Toast" {
		t.Errorf("Beads mailbox identity = %q, want gastown/Toast", beads.Identity())
	}
}

func TestNewMailboxWithBeadsDir(t *testing.T) {
	m := NewMailboxWithBeadsDir("gastown/Toast", "/work/dir", "/custom/.beads")
	if m.identity != "gastown/Toast" {
		t.Errorf("identity = %q, want 'gastown/Toast'", m.identity)
	}
	if filepath.ToSlash(m.beadsDir) != "/custom/.beads" {
		t.Errorf("beadsDir = %q, want '/custom/.beads'", m.beadsDir)
	}
}

func TestNewMailboxWithTownRoot(t *testing.T) {
	m := NewMailboxWithTownRoot("gastown/Toast", "/work/dir", "/custom/.beads", "/home/user/gt")
	if m.identity != "gastown/Toast" {
		t.Errorf("identity = %q, want 'gastown/Toast'", m.identity)
	}
	if filepath.ToSlash(m.beadsDir) != "/custom/.beads" {
		t.Errorf("beadsDir = %q, want '/custom/.beads'", m.beadsDir)
	}
	if filepath.ToSlash(m.townRoot) != "/home/user/gt" {
		t.Errorf("townRoot = %q, want '/home/user/gt'", m.townRoot)
	}
}
