package slack

import (
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

func TestResolveChannel_EmptyConfig(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	router := NewRouter(cfg)

	got := router.ResolveChannel("gastown/polecats/furiosa")
	if got != "C0000000000" {
		t.Errorf("ResolveChannel() = %q, want %q", got, "C0000000000")
	}
}

func TestResolveChannel_EmptyAgentID(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	router := NewRouter(cfg)

	got := router.ResolveChannel("")
	if got != "C0000000000" {
		t.Errorf("ResolveChannel('') = %q, want %q", got, "C0000000000")
	}
}

func TestResolveChannel_ExactMatch(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/polecats/furiosa": "C1111111111",
		"gastown/polecats/*":       "C2222222222",
	}
	router := NewRouter(cfg)

	got := router.ResolveChannel("gastown/polecats/furiosa")
	if got != "C1111111111" {
		t.Errorf("ResolveChannel() = %q, want %q (exact match)", got, "C1111111111")
	}
}

func TestResolveChannel_WildcardMatch(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/polecats/*": "C1111111111",
	}
	router := NewRouter(cfg)

	got := router.ResolveChannel("gastown/polecats/max")
	if got != "C1111111111" {
		t.Errorf("ResolveChannel() = %q, want %q", got, "C1111111111")
	}
}

func TestResolveChannel_RoleMatch(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"*/polecats/*": "C1111111111",
	}
	router := NewRouter(cfg)

	tests := []struct {
		agent string
		want  string
	}{
		{"gastown/polecats/furiosa", "C1111111111"},
		{"beads/polecats/max", "C1111111111"},
		{"longeye/polecats/joe", "C1111111111"},
	}

	for _, tt := range tests {
		got := router.ResolveChannel(tt.agent)
		if got != tt.want {
			t.Errorf("ResolveChannel(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestResolveChannel_RigMatch(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/*": "C1111111111",
	}
	router := NewRouter(cfg)

	got := router.ResolveChannel("gastown/witness")
	if got != "C1111111111" {
		t.Errorf("ResolveChannel() = %q, want %q", got, "C1111111111")
	}
}

func TestResolveChannel_FallbackToDefault(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/polecats/*": "C1111111111",
	}
	router := NewRouter(cfg)

	got := router.ResolveChannel("beads/witness")
	if got != "C0000000000" {
		t.Errorf("ResolveChannel() = %q, want default %q", got, "C0000000000")
	}
}

func TestResolveChannel_SpecificityOrder(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/*":                "C4444444444", // rig match (1 segment, 1 wildcard)
		"*/polecats/*":             "C3333333333", // role match (3 segments, 2 wildcards)
		"gastown/polecats/*":       "C2222222222", // wildcard match (3 segments, 1 wildcard)
		"gastown/polecats/furiosa": "C1111111111", // exact match (3 segments, 0 wildcards)
	}
	router := NewRouter(cfg)

	tests := []struct {
		agent string
		want  string
		desc  string
	}{
		{"gastown/polecats/furiosa", "C1111111111", "exact match wins"},
		{"gastown/polecats/max", "C2222222222", "specific wildcard wins over role pattern"},
		{"beads/polecats/joe", "C3333333333", "role pattern matches"},
		{"gastown/witness", "C4444444444", "rig pattern matches 2-segment agent"},
		{"beads/witness", "C0000000000", "falls back to default"},
	}

	for _, tt := range tests {
		got := router.ResolveChannel(tt.agent)
		if got != tt.want {
			t.Errorf("ResolveChannel(%q) = %q, want %q (%s)", tt.agent, got, tt.want, tt.desc)
		}
	}
}

func TestResolveChannel_SingleSegmentAgent(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"mayor": "C1111111111",
		"*":     "C2222222222",
	}
	router := NewRouter(cfg)

	tests := []struct {
		agent string
		want  string
	}{
		{"mayor", "C1111111111"},
		{"deacon", "C2222222222"},
	}

	for _, tt := range tests {
		got := router.ResolveChannel(tt.agent)
		if got != tt.want {
			t.Errorf("ResolveChannel(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestResolveChannel_NoMatch(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"gastown/polecats/*": "C1111111111",
		"beads/*":            "C2222222222",
	}
	router := NewRouter(cfg)

	// Different segment count
	got := router.ResolveChannel("longeye/crew/max/sub")
	if got != "C0000000000" {
		t.Errorf("ResolveChannel() = %q, want default %q", got, "C0000000000")
	}
}

func TestResolveChannel_NilConfig(t *testing.T) {
	router := NewRouter(nil)

	got := router.ResolveChannel("gastown/polecats/furiosa")
	if got != "" {
		t.Errorf("ResolveChannel() with nil config = %q, want empty string", got)
	}
}

func TestResolveChannel_MalformedPatterns(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"":                  "C1111111111", // empty pattern
		"gastown//polecats": "C2222222222", // double slash
	}
	router := NewRouter(cfg)

	// Empty pattern has 1 segment (empty string)
	// Agent "gastown/polecats/furiosa" has 3 segments - no match
	got := router.ResolveChannel("gastown/polecats/furiosa")
	if got != "C0000000000" {
		t.Errorf("ResolveChannel() = %q, want default %q", got, "C0000000000")
	}
}

func TestPatternSpecificityOrder(t *testing.T) {
	cfg := config.NewSlackConfig()
	cfg.DefaultChannel = "C0000000000"
	cfg.Channels = map[string]string{
		"*/*/*":        "C5555555555", // 3 wildcards
		"*/*":          "C6666666666", // 2 wildcards, 2 segments
		"a/b/*":        "C2222222222", // 1 wildcard
		"a/b/c":        "C1111111111", // 0 wildcards (exact)
		"*/b/*":        "C3333333333", // 2 wildcards, 3 segments
		"*/b/c":        "C4444444444", // 1 wildcard, 3 segments
	}
	router := NewRouter(cfg)

	tests := []struct {
		agent string
		want  string
		desc  string
	}{
		{"a/b/c", "C1111111111", "exact match (0 wildcards)"},
		{"a/b/d", "C2222222222", "a/b/* (1 wildcard) beats */b/* (2 wildcards)"},
		{"x/b/c", "C4444444444", "*/b/c (1 wildcard) beats */b/* (2 wildcards)"},
		{"x/b/y", "C3333333333", "*/b/* matches"},
		{"x/y/z", "C5555555555", "*/*/* matches 3-segment"},
		{"x/y", "C6666666666", "*/* matches 2-segment"},
	}

	for _, tt := range tests {
		got := router.ResolveChannel(tt.agent)
		if got != tt.want {
			t.Errorf("ResolveChannel(%q) = %q, want %q (%s)", tt.agent, got, tt.want, tt.desc)
		}
	}
}
