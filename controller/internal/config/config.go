// Package config provides controller configuration from flags and environment.
package config

import (
	"flag"
	"os"
	"strconv"
)

// Config holds controller configuration. Values come from flags, env vars,
// or defaults, in that priority order.
type Config struct {
	// DaemonHost is the BD Daemon hostname (env: BD_DAEMON_HOST).
	DaemonHost string

	// DaemonPort is the BD Daemon RPC port (env: BD_DAEMON_PORT).
	DaemonPort int

	// Namespace is the K8s namespace to operate in (env: NAMESPACE).
	Namespace string

	// KubeConfig is the path to kubeconfig file (env: KUBECONFIG).
	// Empty means use in-cluster config.
	KubeConfig string

	// LogLevel controls log verbosity: debug, info, warn, error (env: LOG_LEVEL).
	LogLevel string
}

// Parse reads configuration from flags and environment variables.
// Environment variables override defaults; flags override everything.
func Parse() *Config {
	cfg := &Config{
		DaemonHost: envOr("BD_DAEMON_HOST", "localhost"),
		DaemonPort: envIntOr("BD_DAEMON_PORT", 9876),
		Namespace:  envOr("NAMESPACE", "gastown"),
		KubeConfig: os.Getenv("KUBECONFIG"),
		LogLevel:   envOr("LOG_LEVEL", "info"),
	}

	flag.StringVar(&cfg.DaemonHost, "daemon-host", cfg.DaemonHost, "BD Daemon hostname")
	flag.IntVar(&cfg.DaemonPort, "daemon-port", cfg.DaemonPort, "BD Daemon RPC port")
	flag.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "Kubernetes namespace")
	flag.StringVar(&cfg.KubeConfig, "kubeconfig", cfg.KubeConfig, "Path to kubeconfig (empty for in-cluster)")
	flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level: debug, info, warn, error")
	flag.Parse()

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
