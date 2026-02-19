package version

import (
	"testing"
)

func TestShortCommit(t *testing.T) {
	tests := []struct {
		name string
		hash string
		want string
	}{
		{"full sha", "abc123def456789012345678901234567890abcd", "abc123def456"},
		{"exactly 12", "abc123def456", "abc123def456"},
		{"shorter than 12", "abc123", "abc123"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortCommit(tt.hash)
			if got != tt.want {
				t.Errorf("ShortCommit(%q) = %q, want %q", tt.hash, got, tt.want)
			}
		})
	}
}

func TestCommitsMatch(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical full", "abc123def456789012345678901234567890abcd", "abc123def456789012345678901234567890abcd", true},
		{"short prefix", "abc123def456", "abc123def456789012345678901234567890abcd", true},
		{"reverse prefix", "abc123def456789012345678901234567890abcd", "abc123def456", true},
		{"different", "abc123def456", "def456abc123", false},
		{"too short a", "abc12", "abc1234567", false},
		{"too short b", "abc1234567", "abc12", false},
		{"both too short", "abc", "abc", false},
		{"exactly 7 matching", "abcdefg", "abcdefghijk", true},
		{"exactly 7 different", "abcdefg", "abcdefx", false},
		{"empty both", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commitsMatch(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("commitsMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSetCommit(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	SetCommit("test-commit-hash")
	if Commit != "test-commit-hash" {
		t.Errorf("Commit = %q, want test-commit-hash", Commit)
	}

	SetCommit("")
	if Commit != "" {
		t.Errorf("Commit = %q, want empty", Commit)
	}
}

func TestResolveCommitHash(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	t.Run("uses Commit variable when set", func(t *testing.T) {
		Commit = "explicit-commit"
		got := resolveCommitHash()
		if got != "explicit-commit" {
			t.Errorf("resolveCommitHash() = %q, want explicit-commit", got)
		}
	})

	t.Run("falls back to build info when Commit is empty", func(t *testing.T) {
		Commit = ""
		got := resolveCommitHash()
		// We can't control debug.ReadBuildInfo() in tests, but we can verify
		// it doesn't panic and returns a string
		_ = got // may be empty if no VCS info
	})
}

func TestStaleBinaryInfo_Fields(t *testing.T) {
	info := &StaleBinaryInfo{
		IsStale:       true,
		BinaryCommit:  "abc123",
		RepoCommit:    "def456",
		CommitsBehind: 5,
	}
	if !info.IsStale {
		t.Error("expected IsStale to be true")
	}
	if info.CommitsBehind != 5 {
		t.Errorf("CommitsBehind = %d, want 5", info.CommitsBehind)
	}
}
