package refinery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/terminal"
)

// Common errors
var (
	ErrNotRunning     = errors.New("refinery not running")
	ErrAlreadyRunning = errors.New("refinery already running")
	ErrNoQueue        = errors.New("no items in queue")
)

// Manager handles refinery lifecycle and queue operations.
type Manager struct {
	rig     *rig.Rig
	workDir string
	output  io.Writer          // Output destination for user-facing messages
	config  *MergeQueueConfig  // Merge queue configuration (lazy-loaded)
	backend terminal.Backend   // Terminal backend for session liveness checks
}

// NewManager creates a new refinery manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig:     r,
		workDir: r.Path,
		output:  os.Stdout,
		config:  DefaultMergeQueueConfig(), // Use defaults until LoadConfig is called
		backend: terminal.NewCoopBackend(terminal.CoopConfig{}),
	}
}

// SetBackend overrides the terminal backend used for session liveness checks.
func (m *Manager) SetBackend(b terminal.Backend) {
	m.backend = b
}

// hasSession checks if a terminal session exists via the CoopBackend.
func (m *Manager) hasSession(sessionID string) (bool, error) {
	return m.backend.HasSession(sessionID)
}

// LoadConfig loads merge queue configuration from the rig's config.json.
// This delegates to Engineer.LoadConfig which does the actual parsing.
func (m *Manager) LoadConfig() error {
	// Create a temporary Engineer just to load config
	// The Engineer has the config parsing logic we need
	eng := NewEngineer(m.rig)
	if err := eng.LoadConfig(); err != nil {
		return err
	}
	m.config = eng.Config()
	return nil
}

// Config returns the current merge queue configuration.
// If LoadConfig hasn't been called, returns defaults.
func (m *Manager) Config() *MergeQueueConfig {
	return m.config
}

// SetOutput sets the output writer for user-facing messages.
// This is useful for testing or redirecting output.
func (m *Manager) SetOutput(w io.Writer) {
	m.output = w
}

// SessionName returns the session name for this refinery.
func (m *Manager) SessionName() string {
	return fmt.Sprintf("gt-%s-refinery", m.rig.Name)
}

// IsRunning checks if the refinery session is active.
// ZFC: session existence is the source of truth.
func (m *Manager) IsRunning() (bool, error) {
	return m.hasSession(m.SessionName())
}

// Status returns information about the refinery session.
// Uses the backend (coop) to check session existence.
func (m *Manager) Status() (*terminal.SessionInfo, error) {
	sessionID := m.SessionName()

	running, err := m.hasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	return &terminal.SessionInfo{Name: sessionID}, nil
}

// Start starts the refinery.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns an agent session via the backend.
// The agentOverride parameter allows specifying an agent alias to use instead of the town default.
// Session is the source of truth for running state.
func (m *Manager) Start(foreground bool, agentOverride string) error {
	sessionID := m.SessionName()

	if foreground {
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	// Check if session already exists
	running, _ := m.hasSession(sessionID)
	if running {
		// Session exists - check if agent is actually running (healthy vs zombie)
		agentRunning, _ := m.backend.IsAgentRunning(sessionID)
		if agentRunning {
			return ErrAlreadyRunning
		}
		// Zombie - session alive but agent dead. Kill and recreate.
		_, _ = fmt.Fprintln(m.output, "Detected zombie session (session alive, agent dead). Recreating...")
		if err := m.backend.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing zombie session: %w", err)
		}
	}

	// Working directory is the refinery worktree (shares .git with mayor/polecats)
	refineryRigDir := filepath.Join(m.rig.Path, "refinery", "rig")
	if _, err := os.Stat(refineryRigDir); os.IsNotExist(err) {
		refineryRigDir = filepath.Join(m.rig.Path, "mayor", "rig")
	}

	// Ensure runtime settings exist in refinery/ (not refinery/rig/) so we don't
	// write into the source repo. Runtime walks up the tree to find settings.
	refineryParentDir := filepath.Join(m.rig.Path, "refinery")
	townRoot := filepath.Dir(m.rig.Path)
	runtimeConfig := config.ResolveRoleAgentConfig("refinery", townRoot, m.rig.Path)
	if err := runtime.EnsureSettingsForRole(refineryParentDir, "refinery", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: fmt.Sprintf("%s/refinery", m.rig.Name),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "Check your hook and begin patrol.")

	var command string
	if agentOverride != "" {
		var err error
		command, err = config.BuildAgentStartupCommandWithAgentOverride("refinery", m.rig.Name, townRoot, m.rig.Path, initialPrompt, agentOverride)
		if err != nil {
			return fmt.Errorf("building startup command with agent override: %w", err)
		}
	} else {
		command = config.BuildAgentStartupCommand("refinery", m.rig.Name, townRoot, m.rig.Path, initialPrompt)
	}

	// In K8s, sessions are created by the controller when a bead is created.
	// For local development, the backend manages session lifecycle.
	// TODO(bd-e52ls): Replace with bead creation for K8s-native session lifecycle.
	_ = refineryRigDir
	_ = command
	_ = runtimeConfig

	// Set environment variables via backend (non-fatal)
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:          "refinery",
		Rig:           m.rig.Name,
		TownRoot:      townRoot,
		BeadsNoDaemon: true,
		BDDaemonHost:  os.Getenv("BD_DAEMON_HOST"),
	})
	envVars["GT_REFINERY"] = "1"

	for k, v := range envVars {
		_ = m.backend.SetEnvironment(sessionID, k, v)
	}

	return nil
}

// Stop stops the refinery.
// Session existence is the source of truth.
func (m *Manager) Stop() error {
	sessionID := m.SessionName()

	// Check if session exists
	running, _ := m.hasSession(sessionID)
	if !running {
		return ErrNotRunning
	}

	// Kill the session via backend.
	return m.backend.KillSession(sessionID)
}

// Queue returns the current merge queue.
// Uses beads merge-request issues as the source of truth (not git branches).
// ZFC-compliant: beads is the source of truth, no state file.
func (m *Manager) Queue() ([]QueueItem, error) {
	// Query beads for open merge-request type issues
	// BeadsPath() returns the git-synced beads location
	b := beads.New(m.rig.BeadsPath())
	issues, err := b.List(beads.ListOptions{
		Type:     "merge-request",
		Status:   "open",
		Priority: -1, // No priority filter
	})
	if err != nil {
		return nil, fmt.Errorf("querying merge queue from beads: %w", err)
	}

	// Score and sort issues by priority score (highest first)
	now := time.Now()
	type scoredIssue struct {
		issue *beads.Issue
		score float64
	}
	scored := make([]scoredIssue, 0, len(issues))
	for _, issue := range issues {
		score := m.calculateIssueScore(issue, now)
		scored = append(scored, scoredIssue{issue: issue, score: score})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Convert scored issues to queue items
	var items []QueueItem
	pos := 1
	for _, s := range scored {
		mr := m.issueToMR(s.issue)
		if mr != nil {
			items = append(items, QueueItem{
				Position: pos,
				MR:       mr,
				Age:      formatAge(mr.CreatedAt),
			})
			pos++
		}
	}

	return items, nil
}

// calculateIssueScore computes the priority score for an MR issue.
// Higher scores mean higher priority (process first).
func (m *Manager) calculateIssueScore(issue *beads.Issue, now time.Time) float64 {
	fields := beads.ParseMRFields(issue)

	// Parse MR creation time
	mrCreatedAt := parseTime(issue.CreatedAt)
	if mrCreatedAt.IsZero() {
		mrCreatedAt = now // Fallback
	}

	// Build score input
	input := ScoreInput{
		Priority:    issue.Priority,
		MRCreatedAt: mrCreatedAt,
		Now:         now,
	}

	// Add fields from MR metadata if available
	if fields != nil {
		input.RetryCount = fields.RetryCount

		// Parse convoy created at if available
		if fields.ConvoyCreatedAt != "" {
			if convoyTime := parseTime(fields.ConvoyCreatedAt); !convoyTime.IsZero() {
				input.ConvoyCreatedAt = &convoyTime
			}
		}
	}

	return ScoreMRWithDefaults(input)
}

// issueToMR converts a beads issue to a MergeRequest.
func (m *Manager) issueToMR(issue *beads.Issue) *MergeRequest {
	if issue == nil {
		return nil
	}

	// Get configured default branch for this rig
	defaultBranch := m.rig.DefaultBranch()

	fields := beads.ParseMRFields(issue)
	if fields == nil {
		// No MR fields in description, construct from title/ID
		return &MergeRequest{
			ID:           issue.ID,
			IssueID:      issue.ID,
			Status:       MROpen,
			CreatedAt:    parseTime(issue.CreatedAt),
			TargetBranch: defaultBranch,
		}
	}

	// Default target to rig's default branch if not specified
	target := fields.Target
	if target == "" {
		target = defaultBranch
	}

	return &MergeRequest{
		ID:           issue.ID,
		Branch:       fields.Branch,
		Worker:       fields.Worker,
		IssueID:      fields.SourceIssue,
		TargetBranch: target,
		Status:       MROpen,
		CreatedAt:    parseTime(issue.CreatedAt),
	}
}

// parseTime parses a time string, returning zero time on error.
func parseTime(s string) time.Time {
	// Try RFC3339 first (most common)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try date-only format as fallback
		t, _ = time.Parse("2006-01-02", s)
	}
	return t
}

// MergeResult contains the result of a merge attempt.
type MergeResult struct {
	Success     bool
	MergeCommit string // SHA of merge commit on success
	Error       string
	Conflict    bool
	TestsFailed bool
}

// formatAge formats a duration since the given time.
func formatAge(t time.Time) string {
	d := time.Since(t)

	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// Common errors for MR operations
var (
	ErrMRNotFound  = errors.New("merge request not found")
	ErrMRNotFailed = errors.New("merge request has not failed")
)

// FindMR finds a merge request by ID or branch name in the queue.
func (m *Manager) FindMR(idOrBranch string) (*MergeRequest, error) {
	queue, err := m.Queue()
	if err != nil {
		return nil, err
	}

	for _, item := range queue {
		// Match by ID
		if item.MR.ID == idOrBranch {
			return item.MR, nil
		}
		// Match by branch name (with or without polecat/ prefix)
		if item.MR.Branch == idOrBranch {
			return item.MR, nil
		}
		if constants.BranchPolecatPrefix+idOrBranch == item.MR.Branch {
			return item.MR, nil
		}
		// Match by worker name (partial match for convenience)
		if strings.Contains(item.MR.ID, idOrBranch) {
			return item.MR, nil
		}
	}

	return nil, ErrMRNotFound
}

// RejectMR manually rejects a merge request.
// It closes the MR with rejected status and optionally notifies the worker.
// Returns the rejected MR for display purposes.
func (m *Manager) RejectMR(idOrBranch string, reason string, notify bool) (*MergeRequest, error) {
	mr, err := m.FindMR(idOrBranch)
	if err != nil {
		return nil, err
	}

	// Verify MR is open or in_progress (can't reject already closed)
	if mr.IsClosed() {
		return nil, fmt.Errorf("%w: MR is already closed with reason: %s", ErrClosedImmutable, mr.CloseReason)
	}

	// Close the bead in storage with the rejection reason
	b := beads.New(m.rig.BeadsPath())
	if err := b.CloseWithReason("rejected: "+reason, mr.ID); err != nil {
		return nil, fmt.Errorf("failed to close MR bead: %w", err)
	}

	// Update in-memory state for return value
	if err := mr.Close(CloseReasonRejected); err != nil {
		// Non-fatal: bead is already closed, just log
		_, _ = fmt.Fprintf(m.output, "Warning: failed to update MR state: %v\n", err)
	}
	mr.Error = reason

	// Optionally notify worker
	if notify {
		m.notifyWorkerRejected(mr, reason)
	}

	return mr, nil
}

// notifyWorkerRejected sends a rejection notification to a polecat.
func (m *Manager) notifyWorkerRejected(mr *MergeRequest, reason string) {
	router := mail.NewRouter(m.workDir)
	msg := &mail.Message{
		From:    fmt.Sprintf("%s/refinery", m.rig.Name),
		To:      fmt.Sprintf("%s/%s", m.rig.Name, mr.Worker),
		Subject: "Merge request rejected",
		Body: fmt.Sprintf(`Your merge request has been rejected.

Branch: %s
Issue: %s
Reason: %s

Please review the feedback and address the issues before resubmitting.`,
			mr.Branch, mr.IssueID, reason),
		Priority: mail.PriorityNormal,
	}
	_ = router.Send(msg) // best-effort notification
}

// findTownRoot walks up directories to find the town root.
func findTownRoot(startPath string) string {
	path := startPath
	for {
		// Check for mayor/ subdirectory (indicates town root)
		if _, err := os.Stat(filepath.Join(path, "mayor")); err == nil {
			return path
		}
		// Check for config.json with type: workspace
		configPath := filepath.Join(path, "config.json")
		if data, err := os.ReadFile(configPath); err == nil {
			if strings.Contains(string(data), `"type": "workspace"`) {
				return path
			}
		}

		parent := filepath.Dir(path)
		if parent == path {
			break // Reached root
		}
		path = parent
	}
	return ""
}
