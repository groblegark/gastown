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
