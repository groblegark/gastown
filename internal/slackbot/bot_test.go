package slackbot

import (
	"testing"
)

func TestNewBot_MissingBotToken(t *testing.T) {
	cfg := Config{
		AppToken: "xapp-test",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for missing bot token")
	}
}

func TestNewBot_MissingAppToken(t *testing.T) {
	cfg := Config{
		BotToken: "xoxb-test",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for missing app token")
	}
}

func TestNewBot_InvalidAppToken(t *testing.T) {
	cfg := Config{
		BotToken: "xoxb-test",
		AppToken: "invalid-token",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid app token format")
	}
}

func TestNewBot_ValidConfig(t *testing.T) {
	cfg := Config{
		BotToken:    "xoxb-test-token",
		AppToken:    "xapp-test-token",
		RPCEndpoint: "http://localhost:8443",
	}
	bot, err := New(cfg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if bot == nil {
		t.Error("expected bot to be created")
	}
}

func TestAgentToChannelName(t *testing.T) {
	bot := &Bot{
		channelPrefix: "gt-decisions",
	}

	tests := []struct {
		agent    string
		expected string
	}{
		// Standard three-part agents → use rig and role
		{"gastown/polecats/furiosa", "gt-decisions-gastown-polecats"},
		{"beads/crew/wolf", "gt-decisions-beads-crew"},
		{"longeye/polecats/alpha", "gt-decisions-longeye-polecats"},

		// Two-part agents → use both parts
		{"gastown/witness", "gt-decisions-gastown-witness"},
		{"beads/refinery", "gt-decisions-beads-refinery"},

		// Single-part agents
		{"mayor", "gt-decisions-mayor"},

		// Edge cases
		{"", "gt-decisions"},
		{"a/b/c/d/e", "gt-decisions-a-b"}, // Only takes first two parts
	}

	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			got := bot.agentToChannelName(tt.agent)
			if got != tt.expected {
				t.Errorf("agentToChannelName(%q) = %q, want %q", tt.agent, got, tt.expected)
			}
		})
	}
}

func TestAgentToChannelName_Sanitization(t *testing.T) {
	bot := &Bot{
		channelPrefix: "gt-decisions",
	}

	tests := []struct {
		agent    string
		expected string
	}{
		// Uppercase gets lowercased
		{"GasTown/Polecats/Furiosa", "gt-decisions-gastown-polecats"},

		// Underscores become hyphens
		{"gas_town/pole_cats/agent", "gt-decisions-gas-town-pole-cats"},

		// Multiple consecutive hyphens collapsed
		{"foo--bar/baz", "gt-decisions-foo-bar-baz"},
	}

	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			got := bot.agentToChannelName(tt.agent)
			if got != tt.expected {
				t.Errorf("agentToChannelName(%q) = %q, want %q", tt.agent, got, tt.expected)
			}
		})
	}
}

func TestAgentToChannelName_CustomPrefix(t *testing.T) {
	bot := &Bot{
		channelPrefix: "custom-prefix",
	}

	got := bot.agentToChannelName("gastown/polecats/furiosa")
	expected := "custom-prefix-gastown-polecats"
	if got != expected {
		t.Errorf("agentToChannelName with custom prefix = %q, want %q", got, expected)
	}
}
