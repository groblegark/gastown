package reconciler

import (
	"log/slog"
	"os"
	"testing"
)

func newTestTracker() *ImageDigestTracker {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewImageDigestTracker(logger, 0)
}

func TestRecordDigest_BaselineOnly(t *testing.T) {
	tr := newTestTracker()
	image := "ghcr.io/org/repo:v1"

	// First observation sets baseline.
	changed := tr.RecordDigest(image, "sha256:aaa")
	if changed {
		t.Error("RecordDigest should never return true (no drift trigger)")
	}
	if got := tr.LatestDigest(image); got != "sha256:aaa" {
		t.Errorf("LatestDigest = %q, want sha256:aaa", got)
	}

	// Second observation with different digest should NOT update latest.
	tr.RecordDigest(image, "sha256:bbb")
	if got := tr.LatestDigest(image); got != "sha256:aaa" {
		t.Errorf("LatestDigest changed to %q after pod observation, want sha256:aaa", got)
	}
}

func TestRecordRegistryDigest_SameDigest(t *testing.T) {
	tr := newTestTracker()
	image := "ghcr.io/org/repo:v1"

	tr.RecordDigest(image, "sha256:aaa")
	changed := tr.RecordRegistryDigest(image, "sha256:aaa")
	if changed {
		t.Error("RecordRegistryDigest should return false for same digest")
	}
}

func TestRecordRegistryDigest_RequiresConfirmation(t *testing.T) {
	tr := newTestTracker()
	image := "ghcr.io/org/repo:v1"

	tr.RecordDigest(image, "sha256:aaa")

	// First registry check with new digest — should NOT trigger drift.
	changed := tr.RecordRegistryDigest(image, "sha256:bbb")
	if changed {
		t.Error("First registry check should not trigger drift (needs confirmation)")
	}
	if got := tr.LatestDigest(image); got != "sha256:aaa" {
		t.Errorf("LatestDigest = %q, want sha256:aaa (not yet confirmed)", got)
	}

	// Second registry check with same new digest — should trigger drift.
	changed = tr.RecordRegistryDigest(image, "sha256:bbb")
	if !changed {
		t.Error("Second registry check should trigger drift (confirmed)")
	}
	if got := tr.LatestDigest(image); got != "sha256:bbb" {
		t.Errorf("LatestDigest = %q, want sha256:bbb (confirmed)", got)
	}
}

func TestRecordRegistryDigest_FlipFlopReset(t *testing.T) {
	tr := newTestTracker()
	image := "ghcr.io/org/repo:v1"

	tr.RecordDigest(image, "sha256:aaa")

	// First check returns bbb — pending.
	tr.RecordRegistryDigest(image, "sha256:bbb")

	// Second check returns ccc (different!) — resets counter.
	changed := tr.RecordRegistryDigest(image, "sha256:ccc")
	if changed {
		t.Error("Should not trigger drift when candidate changes")
	}
	if got := tr.LatestDigest(image); got != "sha256:aaa" {
		t.Errorf("LatestDigest = %q, want sha256:aaa (candidate reset)", got)
	}

	// Third check returns ccc again — now confirmed.
	changed = tr.RecordRegistryDigest(image, "sha256:ccc")
	if !changed {
		t.Error("Should trigger drift after 2 consecutive confirmations of ccc")
	}
	if got := tr.LatestDigest(image); got != "sha256:ccc" {
		t.Errorf("LatestDigest = %q, want sha256:ccc", got)
	}
}

func TestRecordRegistryDigest_ConfirmClearsPending(t *testing.T) {
	tr := newTestTracker()
	image := "ghcr.io/org/repo:v1"

	tr.RecordDigest(image, "sha256:aaa")

	// Start pending for bbb.
	tr.RecordRegistryDigest(image, "sha256:bbb")

	// Registry returns original digest — clears pending.
	tr.RecordRegistryDigest(image, "sha256:aaa")

	// Now bbb needs to start over from 0.
	changed := tr.RecordRegistryDigest(image, "sha256:bbb")
	if changed {
		t.Error("Should not trigger drift — pending was cleared by confirmation of old digest")
	}

	// Second bbb — now confirmed.
	changed = tr.RecordRegistryDigest(image, "sha256:bbb")
	if !changed {
		t.Error("Should trigger drift after 2 fresh confirmations")
	}
}

func TestRecordDigest_EmptyDigest(t *testing.T) {
	tr := newTestTracker()
	changed := tr.RecordDigest("ghcr.io/org/repo:v1", "")
	if changed {
		t.Error("RecordDigest with empty digest should return false")
	}
	if got := tr.LatestDigest("ghcr.io/org/repo:v1"); got != "" {
		t.Errorf("LatestDigest = %q, want empty", got)
	}
}

func TestRecordRegistryDigest_EmptyDigest(t *testing.T) {
	tr := newTestTracker()
	changed := tr.RecordRegistryDigest("ghcr.io/org/repo:v1", "")
	if changed {
		t.Error("RecordRegistryDigest with empty digest should return false")
	}
}

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		input    string
		wantRepo string
		wantTag  string
	}{
		{"ghcr.io/org/repo:v1.0", "ghcr.io/org/repo", "v1.0"},
		{"ghcr.io/org/repo:latest", "ghcr.io/org/repo", "latest"},
		{"ghcr.io/org/repo", "ghcr.io/org/repo", "latest"},
		{"ghcr.io/org/repo@sha256:abc", "", ""},
	}
	for _, tt := range tests {
		repo, tag := parseImageRef(tt.input)
		if repo != tt.wantRepo || tag != tt.wantTag {
			t.Errorf("parseImageRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, repo, tag, tt.wantRepo, tt.wantTag)
		}
	}
}

func TestExtractDigestFromImageID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ghcr.io/org/repo@sha256:abc123", "sha256:abc123"},
		{"ghcr.io/org/repo:latest", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractDigestFromImageID(tt.input)
		if got != tt.want {
			t.Errorf("extractDigestFromImageID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
