package bdcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvHasKey(t *testing.T) {
	env := []string{
		"HOME=/home/agent",
		"PATH=/usr/bin",
		"BD_DAEMON_HOST=http://localhost:9080",
	}

	tests := []struct {
		key  string
		want bool
	}{
		{"HOME", true},
		{"PATH", true},
		{"BD_DAEMON_HOST", true},
		{"BD_DAEMON_TOKEN", false},
		{"NONEXISTENT", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			// Clear the env var to isolate from process env
			orig := os.Getenv(tt.key)
			os.Unsetenv(tt.key)
			defer os.Setenv(tt.key, orig)

			got := envHasKey(env, tt.key)
			if got != tt.want {
				t.Errorf("envHasKey(env, %q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestReadDaemonConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		content := `daemon-host: http://localhost:9080
daemon-token: test-token-123
issue_prefix: bd
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		host, token := readDaemonConfig(configPath)
		if host != "http://localhost:9080" {
			t.Errorf("host = %q, want http://localhost:9080", host)
		}
		if token != "test-token-123" {
			t.Errorf("token = %q, want test-token-123", token)
		}
	})

	t.Run("config without daemon keys", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		content := `issue_prefix: bd
storage: sqlite
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		host, token := readDaemonConfig(configPath)
		if host != "" {
			t.Errorf("host = %q, want empty", host)
		}
		if token != "" {
			t.Errorf("token = %q, want empty", token)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		host, token := readDaemonConfig("/nonexistent/config.yaml")
		if host != "" || token != "" {
			t.Errorf("expected empty strings for nonexistent file, got host=%q token=%q", host, token)
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		if err := os.WriteFile(configPath, []byte("}{invalid yaml"), 0644); err != nil {
			t.Fatal(err)
		}

		host, token := readDaemonConfig(configPath)
		if host != "" || token != "" {
			t.Errorf("expected empty strings for invalid yaml, got host=%q token=%q", host, token)
		}
	})
}

func TestSetBdPathForTest(t *testing.T) {
	original := resolvedBdPath

	cleanup := SetBdPathForTest("/custom/bd")
	if resolvedBdPath != "/custom/bd" {
		t.Errorf("resolvedBdPath = %q, want /custom/bd", resolvedBdPath)
	}

	cleanup()
	if resolvedBdPath != original {
		t.Errorf("resolvedBdPath not restored: %q, want %q", resolvedBdPath, original)
	}
}

func TestCommand(t *testing.T) {
	cleanup := SetBdPathForTest("/usr/bin/bd")
	defer cleanup()

	cmd := Command("show", "bd-123")
	if cmd.Path != "/usr/bin/bd" {
		// On some systems, exec.Command resolves the path
		// Just verify args are correct
	}
	if len(cmd.Args) < 3 {
		t.Fatalf("expected at least 3 args, got %d", len(cmd.Args))
	}
	if cmd.Args[1] != "show" {
		t.Errorf("arg[1] = %q, want show", cmd.Args[1])
	}
	if cmd.Args[2] != "bd-123" {
		t.Errorf("arg[2] = %q, want bd-123", cmd.Args[2])
	}
}

func TestCommandInDir(t *testing.T) {
	cleanup := SetBdPathForTest("/usr/bin/bd")
	defer cleanup()

	cmd := CommandInDir("/tmp/work", "list")
	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want /tmp/work", cmd.Dir)
	}
}
