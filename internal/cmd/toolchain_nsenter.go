package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// findToolchainPID locates the toolchain sidecar's init process by scanning /proc.
// With shareProcessNamespace enabled, all container processes are visible.
// Returns the PID of the toolchain container's process 1 (its entrypoint).
//
// Detection strategy: scan each /proc/<pid>/environ for the GT_TOOLCHAIN_CONTAINER
// env var. The controller injects this into the toolchain container, so only that
// container's processes will have it. We pick the lowest-PID match as the container
// init process.
func findToolchainPID(containerName string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("reading /proc: %w", err)
	}

	myPID := os.Getpid()
	var bestPID int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == myPID {
			continue
		}

		// Read environment of this process.
		envData, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue // permission denied or process exited
		}

		// Environment is null-byte separated.
		target := "GT_TOOLCHAIN_CONTAINER=" + containerName
		for _, envVar := range bytes.Split(envData, []byte{0}) {
			if string(envVar) == target {
				if bestPID == 0 || pid < bestPID {
					bestPID = pid
				}
				break
			}
		}
	}

	if bestPID == 0 {
		return 0, fmt.Errorf("toolchain container %q not found in /proc (shared process namespace may not be enabled)", containerName)
	}
	return bestPID, nil
}

// nsenterExecAttached runs a command in the toolchain sidecar's mount namespace
// with stdin/stdout/stderr attached to the terminal. Used for interactive commands.
func nsenterExecAttached(pid int, args []string) error {
	nsenterArgs := []string{"--target", strconv.Itoa(pid), "--mount", "--"}
	nsenterArgs = append(nsenterArgs, args...)

	cmd := exec.Command("nsenter", nsenterArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// nsenterExecOutput runs a command in the toolchain sidecar's mount namespace
// and returns the combined stdout output.
func nsenterExecOutput(pid int, args []string) ([]byte, error) {
	nsenterArgs := []string{"--target", strconv.Itoa(pid), "--mount", "--"}
	nsenterArgs = append(nsenterArgs, args...)

	return exec.Command("nsenter", nsenterArgs...).Output()
}

// toolchainExec runs a command in the toolchain sidecar. It tries nsenter first
// (fast, no kubectl needed), then falls back to kubectl exec.
func toolchainExec(container, podName, namespace string, args []string) error {
	// Try nsenter via shared process namespace.
	pid, err := findToolchainPID(container)
	if err == nil {
		return nsenterExecAttached(pid, args)
	}

	// Fall back to kubectl exec.
	return kubectlToolchainExec(container, podName, namespace, args)
}

// toolchainExecOutput runs a command in the toolchain sidecar and captures output.
// Tries nsenter first, falls back to kubectl exec.
func toolchainExecOutput(container, podName, namespace string, args []string) ([]byte, error) {
	// Try nsenter via shared process namespace.
	pid, err := findToolchainPID(container)
	if err == nil {
		return nsenterExecOutput(pid, args)
	}

	// Fall back to kubectl exec.
	return kubectlToolchainExecOutput(container, podName, namespace, args)
}

// kubectlToolchainExec runs a command in the toolchain sidecar via kubectl exec
// with stdin/stdout/stderr attached.
func kubectlToolchainExec(container, podName, namespace string, args []string) error {
	kubectlArgs := []string{"exec", podName, "-c", container}
	if namespace != "" {
		kubectlArgs = append(kubectlArgs, "-n", namespace)
	}
	kubectlArgs = append(kubectlArgs, "--")
	kubectlArgs = append(kubectlArgs, args...)

	cmd := exec.Command("kubectl", kubectlArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// kubectlToolchainExecOutput runs a command in the toolchain sidecar via kubectl
// exec and returns stdout.
func kubectlToolchainExecOutput(container, podName, namespace string, args []string) ([]byte, error) {
	kubectlArgs := []string{"exec", podName, "-c", container}
	if namespace != "" {
		kubectlArgs = append(kubectlArgs, "-n", namespace)
	}
	kubectlArgs = append(kubectlArgs, "--")
	kubectlArgs = append(kubectlArgs, args...)

	return exec.Command("kubectl", kubectlArgs...).Output()
}

// isSidecarRunning checks if the toolchain sidecar is running by looking for its
// process in /proc (shared process namespace). Falls back to kubectl if unavailable.
func isSidecarRunning(container, podName, namespace string) bool {
	// Try /proc-based detection first.
	_, err := findToolchainPID(container)
	if err == nil {
		return true
	}

	// Fall back to kubectl get pod.
	args := []string{"get", "pod", podName, "-o",
		fmt.Sprintf("jsonpath={.status.initContainerStatuses[?(@.name=='%s')].state.running}", container)}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		return false
	}
	return len(out) > 0 && strings.TrimSpace(string(out)) != "{}"
}
