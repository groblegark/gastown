package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout is the default timeout for hook execution.
const DefaultTimeout = 30 * time.Second

// HookEntry mirrors the Claude Code settings.json hook matcher structure.
type HookEntry struct {
	Matcher string       `json:"matcher"`
	Hooks   []HookAction `json:"hooks"`
}

// HookAction mirrors a single hook action in settings.json.
type HookAction struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

// Runner executes hooks from Claude Code settings files.
type Runner struct {
	settingsPath string
	timeout      time.Duration
	env          []string // Additional env vars for hook execution
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithTimeout sets the execution timeout for each hook command.
func WithTimeout(d time.Duration) RunnerOption {
	return func(r *Runner) { r.timeout = d }
}

// WithEnv adds environment variables to the hook execution environment.
func WithEnv(env []string) RunnerOption {
	return func(r *Runner) { r.env = env }
}

// NewRunner creates a Runner that reads hooks from the given settings file.
func NewRunner(settingsPath string, opts ...RunnerOption) *Runner {
	r := &Runner{
		settingsPath: settingsPath,
		timeout:      DefaultTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Fire executes all hooks registered for the given event.
// Returns a result for each hook command executed.
func (r *Runner) Fire(ctx context.Context, event Event) ([]HookResult, error) {
	entries, err := r.loadHooks(event)
	if err != nil {
		return nil, err
	}

	var results []HookResult
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if hook.Type != "command" || hook.Command == "" {
				continue
			}
			result := r.execHook(ctx, hook.Command, entry.Matcher)
			results = append(results, result)
		}
	}
	return results, nil
}

// ListEvents returns all events that have hooks configured.
func (r *Runner) ListEvents() ([]Event, error) {
	hooks, err := r.loadAllHooks()
	if err != nil {
		return nil, err
	}

	var events []Event
	for _, event := range AllEvents() {
		if _, ok := hooks[string(event)]; ok {
			events = append(events, event)
		}
	}
	return events, nil
}

// loadHooks reads hooks for a specific event from the settings file.
func (r *Runner) loadHooks(event Event) ([]HookEntry, error) {
	all, err := r.loadAllHooks()
	if err != nil {
		return nil, err
	}
	return all[string(event)], nil
}

// loadAllHooks reads all hooks from the settings file.
func (r *Runner) loadAllHooks() (map[string][]HookEntry, error) {
	data, err := os.ReadFile(r.settingsPath)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}

	var settings struct {
		Hooks map[string][]HookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing settings: %w", err)
	}

	if settings.Hooks == nil {
		return make(map[string][]HookEntry), nil
	}
	return settings.Hooks, nil
}

// execHook runs a single hook command and captures the result.
func (r *Runner) execHook(ctx context.Context, command, matcher string) HookResult {
	result := HookResult{
		Command: command,
		Matcher: matcher,
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)

	// Set working directory to the settings file's parent's parent
	// (.claude/settings.json → project root)
	settingsDir := filepath.Dir(r.settingsPath)
	projectDir := filepath.Dir(settingsDir)
	cmd.Dir = projectDir

	// Inherit environment, add any extras
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, r.env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Provide empty stdin (hooks that read stdin will get EOF)
	cmd.Stdin = strings.NewReader("")

	err := cmd.Run()

	result.Stdout = truncateOutput(stdout.String(), 2000)
	result.Stderr = truncateOutput(stderr.String(), 2000)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Sprintf("timeout after %s", r.timeout)
			result.ExitCode = -1
		} else {
			result.Error = err.Error()
			result.ExitCode = -1
		}
	}

	result.Success = result.ExitCode == 0 && result.Error == ""
	return result
}

func truncateOutput(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
