package terminal

// tmuxShim provides a minimal tmux subprocess wrapper that implements the
// tmuxClient interface. This replaces the deleted internal/tmux package for
// the few terminal-package callers that still need tmux subprocess calls
// (PodConnection, Server). These will be removed in subsequent K8s Habitat
// cleanup as Coop fully replaces the kubectl-exec-to-screen bridge.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const tmuxTimeout = 10 * time.Second

// tmuxShim implements tmuxClient by shelling out to the tmux binary.
type tmuxShim struct{}

func newTmuxShim() *tmuxShim {
	return &tmuxShim{}
}

func (s *tmuxShim) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("tmux command timed out: %v", args)
		}
		errStr := strings.TrimSpace(stderr.String())
		if errStr != "" {
			return "", fmt.Errorf("tmux %s: %s", args[0], errStr)
		}
		return "", fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (s *tmuxShim) HasSession(name string) (bool, error) {
	_, err := s.run("has-session", "-t", "="+name)
	if err != nil {
		if strings.Contains(err.Error(), "session not found") ||
			strings.Contains(err.Error(), "can't find session") ||
			strings.Contains(err.Error(), "no server running") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *tmuxShim) KillSessionWithProcesses(name string) error {
	_, err := s.run("kill-session", "-t", "="+name)
	return err
}

func (s *tmuxShim) NewSessionWithCommand(name, workDir, command string) error {
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	args = append(args, command)
	_, err := s.run(args...)
	return err
}

func (s *tmuxShim) SetRemainOnExit(pane string, on bool) error {
	val := "off"
	if on {
		val = "on"
	}
	_, err := s.run("set-option", "-t", "="+pane, "remain-on-exit", val)
	return err
}

func (s *tmuxShim) IsPaneDead(session string) (bool, error) {
	out, err := s.run("display-message", "-t", "="+session, "-p", "#{pane_dead}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "1", nil
}

func (s *tmuxShim) SendKeys(session, keys string) error {
	_, err := s.run("send-keys", "-t", "="+session, keys)
	return err
}

func (s *tmuxShim) CapturePane(session string, lines int) (string, error) {
	return s.run("capture-pane", "-p", "-t", "="+session, "-S", fmt.Sprintf("-%d", lines))
}
