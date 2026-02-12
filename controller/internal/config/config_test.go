package config

import (
	"os"
	"testing"
)

func TestEnvOr(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		os.Setenv("TEST_CONFIG_KEY", "from-env")
		defer os.Unsetenv("TEST_CONFIG_KEY")

		got := envOr("TEST_CONFIG_KEY", "default")
		if got != "from-env" {
			t.Errorf("envOr() = %q, want %q", got, "from-env")
		}
	})

	t.Run("returns fallback when unset", func(t *testing.T) {
		os.Unsetenv("TEST_CONFIG_MISSING")

		got := envOr("TEST_CONFIG_MISSING", "default")
		if got != "default" {
			t.Errorf("envOr() = %q, want %q", got, "default")
		}
	})
}

func TestParseSidecarProfiles(t *testing.T) {
	t.Run("parses valid JSON", func(t *testing.T) {
		jsonVal := `{"toolchain-full":{"image":"ghcr.io/groblegark/gastown-toolchain:latest","cpuRequest":"250m","cpuLimit":"2","memoryRequest":"512Mi","memoryLimit":"4Gi"},"toolchain-minimal":{"image":"ghcr.io/groblegark/gastown-toolchain:minimal","cpuRequest":"50m","cpuLimit":"500m","memoryRequest":"128Mi","memoryLimit":"512Mi"}}`
		os.Setenv("SIDECAR_PROFILES_JSON", jsonVal)
		defer os.Unsetenv("SIDECAR_PROFILES_JSON")

		profiles := parseSidecarProfiles()
		if profiles == nil {
			t.Fatal("parseSidecarProfiles() returned nil")
		}
		if len(profiles) != 2 {
			t.Fatalf("got %d profiles, want 2", len(profiles))
		}
		full, ok := profiles["toolchain-full"]
		if !ok {
			t.Fatal("missing toolchain-full profile")
		}
		if full.Image != "ghcr.io/groblegark/gastown-toolchain:latest" {
			t.Errorf("Image = %q, want ghcr.io/groblegark/gastown-toolchain:latest", full.Image)
		}
		if full.CPURequest != "250m" {
			t.Errorf("CPURequest = %q, want 250m", full.CPURequest)
		}
	})

	t.Run("returns nil for empty env", func(t *testing.T) {
		os.Unsetenv("SIDECAR_PROFILES_JSON")
		profiles := parseSidecarProfiles()
		if profiles != nil {
			t.Errorf("expected nil, got %v", profiles)
		}
	})

	t.Run("returns nil for invalid JSON", func(t *testing.T) {
		os.Setenv("SIDECAR_PROFILES_JSON", "not-json")
		defer os.Unsetenv("SIDECAR_PROFILES_JSON")

		profiles := parseSidecarProfiles()
		if profiles != nil {
			t.Errorf("expected nil for invalid JSON, got %v", profiles)
		}
	})
}

func TestParseSidecarRegistryAllowlist(t *testing.T) {
	t.Run("parses comma-separated list", func(t *testing.T) {
		os.Setenv("SIDECAR_REGISTRY_ALLOWLIST", "ghcr.io/groblegark/,docker.io/library/")
		defer os.Unsetenv("SIDECAR_REGISTRY_ALLOWLIST")

		list := parseSidecarRegistryAllowlist()
		if len(list) != 2 {
			t.Fatalf("got %d entries, want 2", len(list))
		}
		if list[0] != "ghcr.io/groblegark/" {
			t.Errorf("list[0] = %q, want ghcr.io/groblegark/", list[0])
		}
	})

	t.Run("returns nil for empty env", func(t *testing.T) {
		os.Unsetenv("SIDECAR_REGISTRY_ALLOWLIST")
		list := parseSidecarRegistryAllowlist()
		if list != nil {
			t.Errorf("expected nil, got %v", list)
		}
	})
}

func TestEnvIntOr(t *testing.T) {
	t.Run("returns parsed int from env", func(t *testing.T) {
		os.Setenv("TEST_PORT", "8080")
		defer os.Unsetenv("TEST_PORT")

		got := envIntOr("TEST_PORT", 9876)
		if got != 8080 {
			t.Errorf("envIntOr() = %d, want %d", got, 8080)
		}
	})

	t.Run("returns fallback for non-numeric", func(t *testing.T) {
		os.Setenv("TEST_PORT_BAD", "notanumber")
		defer os.Unsetenv("TEST_PORT_BAD")

		got := envIntOr("TEST_PORT_BAD", 9876)
		if got != 9876 {
			t.Errorf("envIntOr() = %d, want %d", got, 9876)
		}
	})
}
