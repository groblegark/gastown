package cmd

import (
	"testing"
)

func TestDiscoverK8sDaemon_EnvVarHTTPURL(t *testing.T) {
	t.Setenv("BD_DAEMON_HTTP_URL", "http://daemon.gastown-uat.svc:9080")
	t.Setenv("BD_DAEMON_HOST", "")
	t.Setenv("BD_DAEMON_HTTP_PORT", "")

	url, err := discoverK8sDaemon("gastown-uat", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://daemon.gastown-uat.svc:9080" {
		t.Errorf("got %q, want %q", url, "http://daemon.gastown-uat.svc:9080")
	}
}

func TestDiscoverK8sDaemon_EnvVarHostPort(t *testing.T) {
	t.Setenv("BD_DAEMON_HTTP_URL", "")
	t.Setenv("BD_DAEMON_HOST", "10.0.0.5")
	t.Setenv("BD_DAEMON_HTTP_PORT", "8080")

	url, err := discoverK8sDaemon("gastown-uat", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://10.0.0.5:8080" {
		t.Errorf("got %q, want %q", url, "http://10.0.0.5:8080")
	}
}

func TestDiscoverK8sDaemon_EnvVarHostDefaultPort(t *testing.T) {
	t.Setenv("BD_DAEMON_HTTP_URL", "")
	t.Setenv("BD_DAEMON_HOST", "daemon-svc")
	t.Setenv("BD_DAEMON_HTTP_PORT", "")

	url, err := discoverK8sDaemon("gastown-uat", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://daemon-svc:9080" {
		t.Errorf("got %q, want %q", url, "http://daemon-svc:9080")
	}
}

func TestDiscoverK8sDaemon_HTTPURLTakesPrecedence(t *testing.T) {
	t.Setenv("BD_DAEMON_HTTP_URL", "https://custom-url.example.com")
	t.Setenv("BD_DAEMON_HOST", "should-not-use")
	t.Setenv("BD_DAEMON_HTTP_PORT", "1234")

	url, err := discoverK8sDaemon("gastown-uat", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://custom-url.example.com" {
		t.Errorf("got %q, want %q", url, "https://custom-url.example.com")
	}
}

func TestExtractK8sToken_EnvVar(t *testing.T) {
	t.Setenv("BD_DAEMON_TOKEN", "my-secret-token")

	token, err := extractK8sToken("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "my-secret-token" {
		t.Errorf("got %q, want %q", token, "my-secret-token")
	}
}

func TestExtractK8sToken_NoEnvRequiresNamespace(t *testing.T) {
	t.Setenv("BD_DAEMON_TOKEN", "")

	_, err := extractK8sToken("", "")
	if err == nil {
		t.Fatal("expected error for empty namespace without env var")
	}
}

func TestExtractHostFromMatch(t *testing.T) {
	tests := []struct {
		match string
		want  string
	}{
		{"Host(`gastown-uat.app.e2e.dev.fics.ai`)", "gastown-uat.app.e2e.dev.fics.ai"},
		{"Host(`example.com`) && PathPrefix(`/api`)", "example.com"},
		{"PathPrefix(`/foo`)", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractHostFromMatch(tt.match)
		if got != tt.want {
			t.Errorf("extractHostFromMatch(%q) = %q, want %q", tt.match, got, tt.want)
		}
	}
}
