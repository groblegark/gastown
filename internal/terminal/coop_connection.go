package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/portutil"
)

// DefaultCoopPort is the default coop API port inside agent pods.
const DefaultCoopPort = 8080

// CoopPodConnection manages a kubectl port-forward to a K8s pod running coop.
// It connects to the pod's coop HTTP/WebSocket API via a local port forward.
//
// Usage:
//  1. Open() starts kubectl port-forward and waits for coop to respond
//  2. LocalURL() returns the local coop endpoint (e.g., "http://localhost:18234")
//  3. Attach() execs into `coop attach <url>` (replaces process)
//  4. Close() kills the port-forward process
type CoopPodConnection struct {
	PodName    string
	Namespace  string
	CoopPort   int // container port, default 8080
	KubeConfig string

	localPort  int
	portFwdCmd *exec.Cmd
	mu         sync.Mutex
}

// CoopPodConnectionConfig configures a CoopPodConnection.
type CoopPodConnectionConfig struct {
	PodName    string
	Namespace  string
	CoopPort   int    // default 8080
	KubeConfig string
}

// NewCoopPodConnection creates a connection to a coop-enabled K8s pod.
func NewCoopPodConnection(cfg CoopPodConnectionConfig) *CoopPodConnection {
	coopPort := cfg.CoopPort
	if coopPort == 0 {
		coopPort = DefaultCoopPort
	}
	return &CoopPodConnection{
		PodName:    cfg.PodName,
		Namespace:  cfg.Namespace,
		CoopPort:   coopPort,
		KubeConfig: cfg.KubeConfig,
	}
}

// Open starts kubectl port-forward and waits for the coop health endpoint.
func (c *CoopPodConnection) Open(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Pick a free local port.
	port, err := portutil.FreePort()
	if err != nil {
		return fmt.Errorf("finding free port: %w", err)
	}
	c.localPort = port

	// Build kubectl port-forward command.
	args := []string{"port-forward"}
	if c.KubeConfig != "" {
		args = []string{"--kubeconfig", c.KubeConfig, "port-forward"}
	}
	if c.Namespace != "" {
		args = append(args, "-n", c.Namespace)
	}
	args = append(args, c.PodName, fmt.Sprintf("%d:%d", c.localPort, c.CoopPort))

	c.portFwdCmd = exec.CommandContext(ctx, "kubectl", args...)
	// Discard stdout/stderr to avoid blocking — port-forward is noisy.
	if err := c.portFwdCmd.Start(); err != nil {
		return fmt.Errorf("starting port-forward: %w", err)
	}

	slog.Info("port-forward started",
		"pod", c.PodName,
		"local_port", c.localPort,
		"remote_port", c.CoopPort,
	)

	// Wait for coop health endpoint to respond.
	healthURL := fmt.Sprintf("http://localhost:%d/api/v1/health", c.localPort)
	if err := waitForHealth(ctx, healthURL, 15*time.Second); err != nil {
		// Kill port-forward if health never comes up.
		c.portFwdCmd.Process.Kill()
		c.portFwdCmd.Wait()
		return fmt.Errorf("waiting for coop health: %w", err)
	}

	return nil
}

// Close kills the port-forward process.
func (c *CoopPodConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.portFwdCmd == nil || c.portFwdCmd.Process == nil {
		return nil
	}
	if err := c.portFwdCmd.Process.Kill(); err != nil {
		return fmt.Errorf("killing port-forward: %w", err)
	}
	c.portFwdCmd.Wait()
	slog.Info("port-forward stopped", "pod", c.PodName)
	return nil
}

// IsAlive checks if the port-forward is running and coop responds.
func (c *CoopPodConnection) IsAlive() bool {
	c.mu.Lock()
	if c.portFwdCmd == nil || c.portFwdCmd.Process == nil {
		c.mu.Unlock()
		return false
	}
	port := c.localPort
	c.mu.Unlock()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/v1/health", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// LocalURL returns the local coop HTTP endpoint.
func (c *CoopPodConnection) LocalURL() string {
	return fmt.Sprintf("http://localhost:%d", c.localPort)
}

// LocalPort returns the local port being forwarded.
func (c *CoopPodConnection) LocalPort() int {
	return c.localPort
}

// waitForHealth polls a URL until it returns 200 or the timeout expires.
func waitForHealth(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout after %s waiting for %s", timeout, url)
		case <-ticker.C:
			resp, err := client.Get(url)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}
