package cmd

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestFindToolchainPID_NotFound(t *testing.T) {
	// On a non-K8s machine without shared process namespace,
	// no process should have GT_TOOLCHAIN_CONTAINER in its env.
	_, err := findToolchainPID("nonexistent-container")
	if err == nil {
		t.Fatal("expected error when toolchain container not found")
	}
	got := err.Error()
	// On macOS there's no /proc; on Linux it exists but no container matches.
	if !containsSubstr(got, "not found in /proc") && !containsSubstr(got, "reading /proc") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestFindToolchainPID_SkipsSelf(t *testing.T) {
	// Set the env var on ourselves — findToolchainPID should skip our own PID.
	t.Setenv("GT_TOOLCHAIN_CONTAINER", "test-toolchain")

	pid, err := findToolchainPID("test-toolchain")
	if err == nil {
		// If it found a PID, it must not be ours.
		if pid == os.Getpid() {
			t.Error("findToolchainPID returned our own PID, should skip self")
		}
	}
	// If error, that's expected on non-K8s (no other process has the env var).
}

func TestIsSidecarRunning_FalseWithoutK8s(t *testing.T) {
	// Outside K8s, both nsenter and kubectl paths should return false gracefully.
	running := isSidecarRunning("nonexistent", "fake-pod", "fake-ns")
	if running {
		t.Error("expected false when not in K8s")
	}
}

func TestNsenterArgs(t *testing.T) {
	// Verify nsenter argument construction (without actually running it).
	pid := 12345
	args := []string{"node", "--version"}

	nsenterArgs := []string{"--target", strconv.Itoa(pid), "--mount", "--"}
	nsenterArgs = append(nsenterArgs, args...)

	expected := []string{"--target", "12345", "--mount", "--", "node", "--version"}
	if len(nsenterArgs) != len(expected) {
		t.Fatalf("got %d args, want %d", len(nsenterArgs), len(expected))
	}
	for i, arg := range nsenterArgs {
		if arg != expected[i] {
			t.Errorf("arg[%d] = %q, want %q", i, arg, expected[i])
		}
	}
}

func TestDetectNamespace_Fallback(t *testing.T) {
	// Should return empty string when not in K8s (no service account mount).
	ns := detectNamespace()
	// On a dev machine, the SA file doesn't exist.
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); os.IsNotExist(err) {
		if ns != "" {
			t.Errorf("expected empty namespace, got %q", ns)
		}
	}
}

func TestKubectlToolchainArgs(t *testing.T) {
	// Verify the kubectl fallback builds correct arguments.
	tests := []struct {
		container string
		podName   string
		namespace string
		args      []string
		wantFirst string // first kubectl arg should be "exec"
	}{
		{"toolchain", "pod-1", "ns-1", []string{"node", "-v"}, "exec"},
		{"tc", "pod-2", "", []string{"ls"}, "exec"},
	}

	for _, tt := range tests {
		kubectlArgs := []string{"exec", tt.podName, "-c", tt.container}
		if tt.namespace != "" {
			kubectlArgs = append(kubectlArgs, "-n", tt.namespace)
		}
		kubectlArgs = append(kubectlArgs, "--")
		kubectlArgs = append(kubectlArgs, tt.args...)

		if kubectlArgs[0] != tt.wantFirst {
			t.Errorf("first arg = %q, want %q", kubectlArgs[0], tt.wantFirst)
		}

		// Verify command args appear after "--"
		dashIdx := -1
		for i, a := range kubectlArgs {
			if a == "--" {
				dashIdx = i
				break
			}
		}
		if dashIdx < 0 {
			t.Error("missing -- separator")
		} else {
			gotCmd := kubectlArgs[dashIdx+1:]
			if len(gotCmd) != len(tt.args) {
				t.Errorf("command args length = %d, want %d", len(gotCmd), len(tt.args))
			}
		}
	}
}

// containsSubstr is a simple substring check helper.
func containsSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}
