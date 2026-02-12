//go:build integration

package terminal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findCoopBinaryForTest locates the coop binary, returning its path
// or empty string if not found.
func findCoopBinaryForTest() string {
	if p, err := exec.LookPath("coop"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, rel := range []string{
			"coop/target/release/coop",
			"coop/target/debug/coop",
		} {
			p := filepath.Join(home, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// freePort asks the OS for an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// startCoop starts a real coop process for integration testing.
// Returns the base URL and a cleanup function.
// Skips the test if the coop binary is not found.
func startCoop(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	coopPath := findCoopBinaryForTest()
	if coopPath == "" {
		t.Skip("coop binary not found")
	}

	port := freePort(t)

	cmd := exec.Command(coopPath,
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"bash",
	)

	// Coop writes JSON logs to stdout; capture it for startup detection.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("startCoop: stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("startCoop: start: %v", err)
	}

	// Wait for the "HTTP listening" log line from coop stdout (JSON format).
	listenReady := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "HTTP listening") {
				close(listenReady)
				return
			}
		}
	}()

	select {
	case <-listenReady:
		// Server has logged that it's listening.
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("startCoop: timed out waiting for coop to start listening")
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Poll /api/v1/health until status is "running" or timeout.
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/v1/health")
		if err == nil {
			var health struct {
				Status string `json:"status"`
			}
			json.NewDecoder(resp.Body).Decode(&health)
			resp.Body.Close()
			if health.Status == "running" {
				cleanupFunc := func() {
					cmd.Process.Kill()
					cmd.Wait()
				}
				return base, cleanupFunc
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	cmd.Process.Kill()
	cmd.Wait()
	t.Fatal("startCoop: timed out waiting for health check to return running")
	return "", nil // unreachable
}

// TestCoopHarness_StartsAndStops verifies the harness can start a coop process,
// reach its health endpoint, and clean it up.
func TestCoopHarness_StartsAndStops(t *testing.T) {
	base, cleanup := startCoop(t)
	defer cleanup()

	// Verify health endpoint is reachable.
	resp, err := http.Get(base + "/api/v1/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health returned status %d, want 200", resp.StatusCode)
	}

	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if health.Status != "running" {
		t.Errorf("health status = %q, want %q", health.Status, "running")
	}

	t.Logf("coop running at %s, status=%s", base, health.Status)

	// Cleanup kills the process; verify it becomes unreachable.
	cleanup()
	time.Sleep(500 * time.Millisecond)

	_, err = (&http.Client{Timeout: 1 * time.Second}).Get(base + "/api/v1/health")
	if err == nil {
		t.Error("expected connection failure after cleanup, but request succeeded")
	}
}
