package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
type Manager struct {
	rig          *rig.Rig
	workDir      string
	handoffState *WitnessHandoffState // Cached handoff state for persistence across burns
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig:     r,
		workDir: r.Path,
	}
}

// stateFile returns the path to the witness state file.
func (m *Manager) stateFile() string {
	return filepath.Join(m.rig.Path, ".runtime", "witness.json")
}

// loadState loads witness state from disk.
func (m *Manager) loadState() (*Witness, error) {
	data, err := os.ReadFile(m.stateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return &Witness{
				RigName: m.rig.Name,
				State:   StateStopped,
			}, nil
		}
		return nil, err
	}

	var w Witness
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}

	return &w, nil
}

// saveState persists witness state to disk.
func (m *Manager) saveState(w *Witness) error {
	dir := filepath.Dir(m.stateFile())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.stateFile(), data, 0644)
}

// handoffBeadID returns the well-known ID for this rig's witness handoff bead.
func (m *Manager) handoffBeadID() string {
	return fmt.Sprintf("gt-%s-%s", m.rig.Name, HandoffBeadID)
}

// loadHandoffState loads worker states from the handoff bead.
// If the bead doesn't exist, returns an empty state and creates the bead.
func (m *Manager) loadHandoffState() (*WitnessHandoffState, error) {
	beadID := m.handoffBeadID()

	// Try to read the bead
	cmd := exec.Command("bd", "show", beadID, "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Bead doesn't exist - create it
		if strings.Contains(stderr.String(), "not found") || strings.Contains(stderr.String(), "No issue") {
			if err := m.ensureHandoffBead(); err != nil {
				return nil, fmt.Errorf("creating handoff bead: %w", err)
			}
			return &WitnessHandoffState{
				WorkerStates: make(map[string]WorkerState),
			}, nil
		}
		return nil, fmt.Errorf("reading handoff bead: %s", stderr.String())
	}

	// Parse the bead JSON
	var issues []struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing handoff bead: %w", err)
	}

	if len(issues) == 0 {
		return &WitnessHandoffState{
			WorkerStates: make(map[string]WorkerState),
		}, nil
	}

	// The description contains our JSON state
	desc := issues[0].Description

	// Extract JSON from description (skip any markdown header)
	state := &WitnessHandoffState{
		WorkerStates: make(map[string]WorkerState),
	}

	// Try to find JSON in the description
	if idx := strings.Index(desc, "{"); idx >= 0 {
		jsonPart := desc[idx:]
		// Find the matching closing brace
		if endIdx := findMatchingBrace(jsonPart); endIdx > 0 {
			jsonPart = jsonPart[:endIdx+1]
			if err := json.Unmarshal([]byte(jsonPart), state); err != nil {
				// If parsing fails, just return empty state
				return &WitnessHandoffState{
					WorkerStates: make(map[string]WorkerState),
				}, nil
			}
		}
	}

	return state, nil
}

// findMatchingBrace finds the index of the matching closing brace.
func findMatchingBrace(s string) int {
	depth := 0
	inString := false
	escaped := false

	for i, c := range s {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// saveHandoffState persists worker states to the handoff bead.
func (m *Manager) saveHandoffState(state *WitnessHandoffState) error {
	beadID := m.handoffBeadID()

	// Serialize state to JSON
	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing state: %w", err)
	}

	// Update the bead's description with the JSON state
	desc := fmt.Sprintf("Witness handoff state for %s.\n\n```json\n%s\n```", m.rig.Name, string(stateJSON))

	cmd := exec.Command("bd", "update", beadID, "--description", desc)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("updating handoff bead: %s", strings.TrimSpace(string(out)))
	}

	return nil
}

// ensureHandoffBead creates the handoff bead if it doesn't exist.
func (m *Manager) ensureHandoffBead() error {
	beadID := m.handoffBeadID()
	title := fmt.Sprintf("Witness handoff state (%s)", m.rig.Name)
	desc := fmt.Sprintf("Witness handoff state for %s.\n\n```json\n{\"worker_states\": {}, \"last_patrol\": null}\n```", m.rig.Name)

	// Create pinned handoff bead with specific ID
	cmd := exec.Command("bd", "create",
		"--id", beadID,
		"--title", title,
		"--type", "task",
		"--priority", "4", // Low priority - just state storage
		"--description", desc,
	)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		// If it already exists, that's fine
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return fmt.Errorf("creating handoff bead: %s", strings.TrimSpace(string(out)))
	}

	// Pin the bead so it survives cleanup
	cmd = exec.Command("bd", "update", beadID, "--pinned")
	cmd.Dir = m.workDir
	_ = cmd.Run() // Best effort - pinning might not be supported

	return nil
}

// Status returns the current witness status.
func (m *Manager) Status() (*Witness, error) {
	w, err := m.loadState()
	if err != nil {
		return nil, err
	}

	// If running, verify process is still alive
	if w.State == StateRunning && w.PID > 0 {
		if !processExists(w.PID) {
			w.State = StateStopped
			w.PID = 0
			_ = m.saveState(w)
		}
	}

	// Update monitored polecats list
	w.MonitoredPolecats = m.rig.Polecats

	return w, nil
}

// Start starts the witness.
// If foreground is true, runs in the current process (blocking).
// Otherwise, spawns a background process.
func (m *Manager) Start(foreground bool) error {
	w, err := m.loadState()
	if err != nil {
		return err
	}

	if w.State == StateRunning && w.PID > 0 && processExists(w.PID) {
		return ErrAlreadyRunning
	}

	now := time.Now()
	w.State = StateRunning
	w.StartedAt = &now
	w.PID = os.Getpid() // For foreground mode; background would set actual PID
	w.MonitoredPolecats = m.rig.Polecats

	if err := m.saveState(w); err != nil {
		return err
	}

	if foreground {
		// Run the monitoring loop (blocking)
		return m.run(w)
	}

	// Background mode: spawn a new process
	// For MVP, we just mark as running - actual daemon implementation later
	return nil
}

// Stop stops the witness.
func (m *Manager) Stop() error {
	w, err := m.loadState()
	if err != nil {
		return err
	}

	if w.State != StateRunning {
		return ErrNotRunning
	}

	// If we have a PID, try to stop it gracefully
	if w.PID > 0 && w.PID != os.Getpid() {
		// Send SIGTERM
		if proc, err := os.FindProcess(w.PID); err == nil {
			_ = proc.Signal(os.Interrupt)
		}
	}

	w.State = StateStopped
	w.PID = 0

	return m.saveState(w)
}

// run is the main monitoring loop (for foreground mode).
func (m *Manager) run(w *Witness) error {
	fmt.Println("Witness running...")
	fmt.Println("Press Ctrl+C to stop")

	// Load handoff state from persistent bead (survives wisp burns)
	handoffState, err := m.loadHandoffState()
	if err != nil {
		fmt.Printf("Warning: could not load handoff state: %v\n", err)
		handoffState = &WitnessHandoffState{
			WorkerStates: make(map[string]WorkerState),
		}
	}
	m.handoffState = handoffState
	fmt.Printf("Loaded handoff state with %d worker(s)\n", len(m.handoffState.WorkerStates))

	// Ensure mol-witness-patrol instance exists for tracking
	if err := m.ensurePatrolInstance(); err != nil {
		fmt.Printf("Warning: could not ensure patrol instance: %v\n", err)
	}

	// Initial check immediately
	m.checkAndProcess(w)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.checkAndProcess(w)
	}
	return nil
}

// ensurePatrolInstance ensures a mol-witness-patrol instance exists for tracking.
// If one already exists (from a previous session), it's reused. Otherwise, a new
// instance is created and pinned to the witness handoff bead.
func (m *Manager) ensurePatrolInstance() error {
	// Check if we already have a patrol instance
	if m.handoffState != nil && m.handoffState.PatrolInstanceID != "" {
		// Verify it still exists
		cmd := exec.Command("bd", "show", m.handoffState.PatrolInstanceID, "--json")
		cmd.Dir = m.workDir
		if err := cmd.Run(); err == nil {
			fmt.Printf("Using existing patrol instance: %s\n", m.handoffState.PatrolInstanceID)
			return nil
		}
		// Instance no longer exists, clear it
		m.handoffState.PatrolInstanceID = ""
	}

	// Create a new patrol instance
	// First, create a root issue for the patrol
	patrolTitle := fmt.Sprintf("Witness Patrol (%s)", m.rig.Name)
	patrolDesc := fmt.Sprintf(`Active mol-witness-patrol instance for %s.

rig: %s
started_at: %s
type: patrol-instance
`, m.rig.Name, m.rig.Name, time.Now().UTC().Format(time.RFC3339))

	cmd := exec.Command("bd", "create",
		"--title", patrolTitle,
		"--type", "task",
		"--priority", "3",
		"--description", patrolDesc,
	)
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating patrol instance: %s", stderr.String())
	}

	// Parse the created issue ID from stdout
	// Output format: "✓ Created issue: gt-xyz"
	output := stdout.String()
	var patrolID string
	if _, err := fmt.Sscanf(output, "✓ Created issue: %s", &patrolID); err != nil {
		// Try alternate parsing
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Created issue:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					patrolID = strings.TrimSpace(parts[len(parts)-1])
					break
				}
			}
		}
	}

	if patrolID == "" {
		return fmt.Errorf("could not parse patrol instance ID from: %s", output)
	}

	// Store the patrol instance ID
	if m.handoffState == nil {
		m.handoffState = &WitnessHandoffState{
			WorkerStates: make(map[string]WorkerState),
		}
	}
	m.handoffState.PatrolInstanceID = patrolID

	// Persist the updated handoff state
	if err := m.saveHandoffState(m.handoffState); err != nil {
		return fmt.Errorf("saving handoff state: %w", err)
	}

	fmt.Printf("Created patrol instance: %s\n", patrolID)
	return nil
}

// checkAndProcess performs health check, shutdown processing, and auto-spawn.
func (m *Manager) checkAndProcess(w *Witness) {
	// Perform health check
	if err := m.healthCheck(w); err != nil {
		fmt.Printf("Health check error: %v\n", err)
	}

	// Check for shutdown requests
	if err := m.processShutdownRequests(w); err != nil {
		fmt.Printf("Shutdown request error: %v\n", err)
	}

	// Check for polecats with closed issues that haven't signaled done
	if err := m.checkPendingCompletions(w); err != nil {
		fmt.Printf("Pending completions check error: %v\n", err)
	}

	// Auto-spawn for ready work (if enabled)
	if w.Config.AutoSpawn {
		if err := m.autoSpawnForReadyWork(w); err != nil {
			fmt.Printf("Auto-spawn error: %v\n", err)
		}
	}

	// Update last patrol time and persist handoff state
	if m.handoffState != nil {
		now := time.Now()
		m.handoffState.LastPatrol = &now
		// Note: individual nudge/activity updates already persist, so this is just for LastPatrol
	}
}

// healthCheck performs a health check on all monitored polecats.
func (m *Manager) healthCheck(w *Witness) error {
	now := time.Now()
	w.LastCheckAt = &now
	w.Stats.TotalChecks++
	w.Stats.TodayChecks++

	// List polecats
	polecatMgr := polecat.NewManager(m.rig, git.NewGit(m.rig.Path))
	polecats, err := polecatMgr.List()
	if err != nil {
		return fmt.Errorf("listing polecats: %w", err)
	}

	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)

	// Update monitored polecats list
	var active []string
	for _, p := range polecats {
		running, _ := sessMgr.IsRunning(p.Name)
		if running {
			active = append(active, p.Name)

			// Check health of each active polecat
			status := m.checkPolecatHealth(p.Name, p.ClonePath)
			if status == PolecatStuck {
				m.handleStuckPolecat(w, p.Name)
			} else if status == PolecatHealthy {
				// Worker is active - update activity tracking and clear nudge count
				m.updateWorkerActivity(p.Name, "")
			}
		}
	}
	w.MonitoredPolecats = active

	return m.saveState(w)
}

// PolecatHealthStatus represents the health status of a polecat.
type PolecatHealthStatus int

const (
	// PolecatHealthy means the polecat is working normally.
	PolecatHealthy PolecatHealthStatus = iota
	// PolecatStuck means the polecat has no recent activity.
	PolecatStuck
	// PolecatDead means the polecat session is not responding.
	PolecatDead
)

// StuckThresholdMinutes is the default time without activity before a polecat is considered stuck.
const StuckThresholdMinutes = 30

// checkPolecatHealth checks if a polecat is healthy based on recent activity.
func (m *Manager) checkPolecatHealth(name, path string) PolecatHealthStatus {
	threshold := time.Duration(StuckThresholdMinutes) * time.Minute

	// Check 1: Git activity (most reliable indicator of work)
	gitPath := filepath.Join(path, ".git")
	if info, err := os.Stat(gitPath); err == nil {
		if time.Since(info.ModTime()) < threshold {
			return PolecatHealthy
		}
	}

	// Check 2: State file activity
	stateFile := filepath.Join(path, ".runtime", "state.json")
	if info, err := os.Stat(stateFile); err == nil {
		if time.Since(info.ModTime()) < threshold {
			return PolecatHealthy
		}
	}

	// Check 3: Any file modification in the polecat directory
	latestMod := m.getLatestModTime(path)
	if !latestMod.IsZero() && time.Since(latestMod) < threshold {
		return PolecatHealthy
	}

	return PolecatStuck
}

// getLatestModTime finds the most recent modification time in a directory.
func (m *Manager) getLatestModTime(dir string) time.Time {
	var latest time.Time

	// Quick check: just look at a few key locations
	locations := []string{
		filepath.Join(dir, ".git", "logs", "HEAD"),
		filepath.Join(dir, ".git", "index"),
		filepath.Join(dir, ".beads", "issues.jsonl"),
	}

	for _, loc := range locations {
		if info, err := os.Stat(loc); err == nil {
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
	}

	return latest
}

// handleStuckPolecat handles a polecat that appears to be stuck.
func (m *Manager) handleStuckPolecat(w *Witness, polecatName string) {
	fmt.Printf("Polecat %s appears stuck (no activity for %d minutes)\n",
		polecatName, StuckThresholdMinutes)

	// Check nudge history for this polecat
	nudgeCount := m.getNudgeCount(w, polecatName)

	if nudgeCount == 0 {
		// First stuck detection: send a nudge
		fmt.Printf("  Sending nudge to %s...\n", polecatName)
		if err := m.sendNudge(polecatName, "No activity detected. Are you still working?"); err != nil {
			fmt.Printf("  Warning: failed to send nudge: %v\n", err)
		}
		m.recordNudge(w, polecatName)
		w.Stats.TotalNudges++
		w.Stats.TodayNudges++
	} else if nudgeCount == 1 {
		// Second stuck detection: escalate to Mayor
		fmt.Printf("  Escalating %s to Mayor (no response to nudge)...\n", polecatName)
		if err := m.escalateToMayor(polecatName); err != nil {
			fmt.Printf("  Warning: failed to escalate: %v\n", err)
		}
		w.Stats.TotalEscalations++
		m.recordNudge(w, polecatName)
	} else {
		// Third+ stuck detection: log but wait for human confirmation
		fmt.Printf("  %s still stuck (waiting for human intervention)\n", polecatName)
	}
}

// getNudgeCount returns how many times a polecat has been nudged.
// Uses handoff state for persistence across wisp burns.
func (m *Manager) getNudgeCount(w *Witness, polecatName string) int {
	// First check handoff state (persistent across burns)
	if m.handoffState != nil {
		if ws, ok := m.handoffState.WorkerStates[polecatName]; ok {
			return ws.NudgeCount
		}
	}

	// Fallback to legacy SpawnedIssues for backwards compatibility
	count := 0
	nudgeKey := "nudge:" + polecatName
	for _, entry := range w.SpawnedIssues {
		if entry == nudgeKey {
			count++
		}
	}
	return count
}

// recordNudge records that a nudge was sent to a polecat.
// Updates both handoff state (persistent) and legacy SpawnedIssues.
func (m *Manager) recordNudge(w *Witness, polecatName string) {
	now := time.Now()

	// Update handoff state (persistent across burns)
	if m.handoffState != nil {
		if m.handoffState.WorkerStates == nil {
			m.handoffState.WorkerStates = make(map[string]WorkerState)
		}
		ws := m.handoffState.WorkerStates[polecatName]
		ws.NudgeCount++
		ws.LastNudge = &now
		m.handoffState.WorkerStates[polecatName] = ws

		// Persist to handoff bead
		if err := m.saveHandoffState(m.handoffState); err != nil {
			fmt.Printf("Warning: failed to persist handoff state: %v\n", err)
		}
	}

	// Also update legacy SpawnedIssues for backwards compatibility
	nudgeKey := "nudge:" + polecatName
	w.SpawnedIssues = append(w.SpawnedIssues, nudgeKey)
}

// clearNudgeCount clears the nudge count for a polecat (e.g., when they become active again).
func (m *Manager) clearNudgeCount(polecatName string) {
	if m.handoffState != nil && m.handoffState.WorkerStates != nil {
		if ws, ok := m.handoffState.WorkerStates[polecatName]; ok {
			ws.NudgeCount = 0
			ws.LastNudge = nil
			now := time.Now()
			ws.LastActive = &now
			m.handoffState.WorkerStates[polecatName] = ws

			// Persist to handoff bead
			if err := m.saveHandoffState(m.handoffState); err != nil {
				fmt.Printf("Warning: failed to persist handoff state: %v\n", err)
			}
		}
	}
}

// updateWorkerActivity updates the last active time for a worker.
func (m *Manager) updateWorkerActivity(polecatName, issueID string) {
	if m.handoffState != nil {
		if m.handoffState.WorkerStates == nil {
			m.handoffState.WorkerStates = make(map[string]WorkerState)
		}
		ws := m.handoffState.WorkerStates[polecatName]
		now := time.Now()
		ws.LastActive = &now
		if issueID != "" {
			ws.Issue = issueID
		}
		// Reset nudge count if worker is active
		if ws.NudgeCount > 0 {
			ws.NudgeCount = 0
			ws.LastNudge = nil
		}
		m.handoffState.WorkerStates[polecatName] = ws
	}
}

// escalateToMayor sends an escalation message to the Mayor.
func (m *Manager) escalateToMayor(polecatName string) error {
	subject := fmt.Sprintf("ESCALATION: Polecat %s stuck", polecatName)
	body := fmt.Sprintf(`Polecat %s in rig %s appears stuck.

This polecat has been unresponsive for over %d minutes despite nudging.

Recommended actions:
1. Check 'gt session attach %s/%s' to see current state
2. If truly stuck, run 'gt session stop %s/%s' to kill the session
3. Investigate root cause

Rig: %s
Time: %s
`, polecatName, m.rig.Name, StuckThresholdMinutes*2,
		m.rig.Name, polecatName,
		m.rig.Name, polecatName,
		m.rig.Name, time.Now().Format(time.RFC3339))

	cmd := exec.Command("bd", "mail", "send", "mayor/",
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// processShutdownRequests checks mail for lifecycle requests and handles them.
func (m *Manager) processShutdownRequests(w *Witness) error {
	// Get witness mailbox via gt mail
	messages, err := m.getWitnessMessages()
	if err != nil {
		return fmt.Errorf("getting messages: %w", err)
	}

	for _, msg := range messages {
		// Handle POLECAT_DONE messages (polecat has completed work and is ready for cleanup)
		if strings.HasPrefix(msg.Subject, "POLECAT_DONE ") {
			polecatName := extractPolecatNameFromDone(msg.Subject)
			if polecatName == "" {
				fmt.Printf("Warning: could not extract polecat name from POLECAT_DONE message\n")
				m.ackMessage(msg.ID)
				continue
			}

			fmt.Printf("Processing POLECAT_DONE from %s\n", polecatName)

			// Record that this polecat has signaled done
			m.recordDone(w, polecatName)

			// Verify polecat state before cleanup
			if err := m.verifyPolecatState(polecatName); err != nil {
				fmt.Printf("  Verification failed: %v\n", err)

				// Send nudge to polecat to fix state
				if err := m.sendNudge(polecatName, err.Error()); err != nil {
					fmt.Printf("  Warning: failed to send nudge: %v\n", err)
				}

				// Don't ack message - will retry on next check
				continue
			}

			// Perform cleanup
			if err := m.cleanupPolecat(polecatName); err != nil {
				fmt.Printf("  Cleanup error: %v\n", err)
				// Don't ack message on error - will retry
				continue
			}

			fmt.Printf("  Cleanup complete\n")

			// Acknowledge the message
			m.ackMessage(msg.ID)
			continue
		}

		// Handle LIFECYCLE shutdown requests (legacy/Deacon-managed)
		if strings.Contains(msg.Subject, "LIFECYCLE:") && strings.Contains(msg.Subject, "shutdown") {
			fmt.Printf("Processing shutdown request: %s\n", msg.Subject)

			// Extract polecat name from message body
			polecatName := extractPolecatName(msg.Body)
			if polecatName == "" {
				fmt.Printf("  Warning: could not extract polecat name from message\n")
				m.ackMessage(msg.ID)
				continue
			}

			fmt.Printf("  Polecat: %s\n", polecatName)

			// SAFETY: Only cleanup if polecat has sent POLECAT_DONE
			if !m.hasSentDone(w, polecatName) {
				fmt.Printf("  Waiting for POLECAT_DONE from %s before cleanup\n", polecatName)

				// Send reminder to polecat to complete shutdown sequence
				if err := m.sendNudge(polecatName, "Please run 'gt done' to signal completion"); err != nil {
					fmt.Printf("  Warning: failed to send nudge: %v\n", err)
				}

				// Don't ack message - will retry on next check
				continue
			}

			// Verify polecat state before cleanup
			if err := m.verifyPolecatState(polecatName); err != nil {
				fmt.Printf("  Verification failed: %v\n", err)

				// Send nudge to polecat
				if err := m.sendNudge(polecatName, err.Error()); err != nil {
					fmt.Printf("  Warning: failed to send nudge: %v\n", err)
				}

				// Don't ack message - will retry on next check
				continue
			}

			// Perform cleanup
			if err := m.cleanupPolecat(polecatName); err != nil {
				fmt.Printf("  Cleanup error: %v\n", err)
				// Don't ack message on error - will retry
				continue
			}

			fmt.Printf("  Cleanup complete\n")

			// Acknowledge the message
			m.ackMessage(msg.ID)
		}
	}

	return nil
}

// verifyPolecatState checks that a polecat is safe to clean up.
func (m *Manager) verifyPolecatState(polecatName string) error {
	polecatPath := filepath.Join(m.rig.Path, "polecats", polecatName)

	// Check if polecat directory exists
	if _, err := os.Stat(polecatPath); os.IsNotExist(err) {
		// Already cleaned up, that's fine
		return nil
	}

	// 1. Check git status is clean
	polecatGit := git.NewGit(polecatPath)
	status, err := polecatGit.Status()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if !status.Clean {
		return fmt.Errorf("git working tree is not clean")
	}

	// Note: beads changes would be reflected in git status above,
	// since beads files are tracked in git.

	// 2. Check that the polecat branch was pushed to remote
	// This catches the case where a polecat closes an issue without pushing their work.
	// Without this check, work can be lost when the polecat worktree is cleaned up.
	branchName := "polecat/" + polecatName
	pushed, unpushedCount, err := polecatGit.BranchPushedToRemote(branchName, "origin")
	if err != nil {
		// Log but don't fail - could be network issue
		fmt.Printf("  Warning: could not verify branch push status: %v\n", err)
	} else if !pushed {
		return fmt.Errorf("branch %s has %d unpushed commit(s) - run 'git push origin %s' before closing",
			branchName, unpushedCount, branchName)
	}

	return nil
}

// sendNudge sends a message to a polecat asking it to fix its state.
func (m *Manager) sendNudge(polecatName, reason string) error {
	subject := fmt.Sprintf("NUDGE: Cannot shutdown - %s", reason)
	body := fmt.Sprintf(`Your shutdown request was denied because: %s

Please fix the issue and run 'gt handoff' again.

Polecat: %s
Rig: %s
Time: %s
`, reason, polecatName, m.rig.Name, time.Now().Format(time.RFC3339))

	// Send via gt mail
	recipient := fmt.Sprintf("%s/%s", m.rig.Name, polecatName)
	cmd := exec.Command("gt", "mail", "send", recipient,
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// WitnessMessage represents a mail message for the witness.
type WitnessMessage struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	From    string `json:"from"`
}

// getWitnessMessages retrieves unread messages for the witness.
func (m *Manager) getWitnessMessages() ([]WitnessMessage, error) {
	// Use gt mail inbox --json
	cmd := exec.Command("gt", "mail", "inbox", "--json")
	cmd.Dir = m.workDir
	cmd.Env = append(os.Environ(), "BEADS_AGENT_NAME="+m.rig.Name+"-witness")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// No messages is not an error
		if strings.Contains(stderr.String(), "no messages") {
			return nil, nil
		}
		return nil, fmt.Errorf("%s", stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, nil
	}

	var messages []WitnessMessage
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		// Try parsing as empty array
		if strings.TrimSpace(stdout.String()) == "[]" {
			return nil, nil
		}
		return nil, fmt.Errorf("parsing messages: %w", err)
	}

	return messages, nil
}

// ackMessage acknowledges a message (marks it as read/handled).
func (m *Manager) ackMessage(id string) {
	cmd := exec.Command("bd", "mail", "ack", id)
	cmd.Dir = m.workDir
	_ = cmd.Run() // Ignore errors
}

// extractPolecatName extracts the polecat name from a lifecycle request body.
func extractPolecatName(body string) string {
	// Look for "Polecat: <name>" pattern
	re := regexp.MustCompile(`Polecat:\s*(\S+)`)
	matches := re.FindStringSubmatch(body)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// extractPolecatNameFromDone extracts the polecat name from a POLECAT_DONE subject.
// Subject format: "POLECAT_DONE {name}"
func extractPolecatNameFromDone(subject string) string {
	const prefix = "POLECAT_DONE "
	if strings.HasPrefix(subject, prefix) {
		return strings.TrimSpace(subject[len(prefix):])
	}
	return ""
}

// recordDone records that a polecat has sent POLECAT_DONE.
// Uses SpawnedIssues with "done:" prefix to track.
func (m *Manager) recordDone(w *Witness, polecatName string) {
	doneKey := "done:" + polecatName
	// Don't record duplicates
	for _, entry := range w.SpawnedIssues {
		if entry == doneKey {
			return
		}
	}
	w.SpawnedIssues = append(w.SpawnedIssues, doneKey)
	_ = m.saveState(w)
}

// hasSentDone checks if a polecat has sent POLECAT_DONE.
func (m *Manager) hasSentDone(w *Witness, polecatName string) bool {
	doneKey := "done:" + polecatName
	for _, entry := range w.SpawnedIssues {
		if entry == doneKey {
			return true
		}
	}
	return false
}

// PendingCompletionTimeout is how long to wait for POLECAT_DONE after issue is closed
// before force-killing the polecat session.
const PendingCompletionTimeout = 10 * time.Minute

// checkPendingCompletions checks for polecats with closed issues that haven't sent POLECAT_DONE.
// It nudges them to complete, and force-kills after timeout.
func (m *Manager) checkPendingCompletions(w *Witness) error {
	polecatMgr := polecat.NewManager(m.rig, git.NewGit(m.rig.Path))
	polecats, err := polecatMgr.List()
	if err != nil {
		return fmt.Errorf("listing polecats: %w", err)
	}

	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)

	for _, p := range polecats {
		// Skip if not running
		running, _ := sessMgr.IsRunning(p.Name)
		if !running {
			continue
		}

		// Skip if already signaled done
		if m.hasSentDone(w, p.Name) {
			continue
		}

		// Check if the polecat's issue is closed
		issueID := m.getPolecatIssue(p.Name, p.ClonePath)
		if issueID == "" {
			continue
		}

		closed, err := m.isIssueClosed(issueID)
		if err != nil || !closed {
			continue
		}

		// Issue is closed but polecat hasn't sent POLECAT_DONE
		waitKey := "waiting:" + p.Name
		waitingSince := m.getWaitingTimestamp(w, waitKey)

		if waitingSince.IsZero() {
			// First detection - record timestamp and nudge
			fmt.Printf("Issue %s is closed but polecat %s hasn't signaled done\n", issueID, p.Name)
			m.recordWaiting(w, waitKey)
			if err := m.sendNudge(p.Name, "Your issue is closed. Please run 'gt done' to complete shutdown."); err != nil {
				fmt.Printf("  Warning: failed to send nudge: %v\n", err)
			}
		} else if time.Since(waitingSince) > PendingCompletionTimeout {
			// Timeout reached - force cleanup
			fmt.Printf("Timeout waiting for POLECAT_DONE from %s, force cleaning up\n", p.Name)

			// Verify state first (this still protects uncommitted work)
			if err := m.verifyPolecatState(p.Name); err != nil {
				fmt.Printf("  Cannot force cleanup - %v\n", err)
				// Escalate to Mayor
				m.escalateToMayor(p.Name)
				continue
			}

			if err := m.cleanupPolecat(p.Name); err != nil {
				fmt.Printf("  Force cleanup failed: %v\n", err)
				continue
			}

			// Clean up tracking
			m.clearWaiting(w, waitKey)
		} else {
			// Still waiting
			elapsed := time.Since(waitingSince).Round(time.Minute)
			remaining := (PendingCompletionTimeout - time.Since(waitingSince)).Round(time.Minute)
			fmt.Printf("Waiting for POLECAT_DONE from %s (elapsed: %v, timeout in: %v)\n",
				p.Name, elapsed, remaining)
		}
	}

	return nil
}

// getPolecatIssue tries to determine which issue a polecat is working on.
func (m *Manager) getPolecatIssue(polecatName, polecatPath string) string {
	// Try to read from state file
	stateFile := filepath.Join(polecatPath, ".runtime", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}

	var state struct {
		IssueID string `json:"issue_id"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}

	return state.IssueID
}

// isIssueClosed checks if an issue is closed.
func (m *Manager) isIssueClosed(issueID string) (bool, error) {
	cmd := exec.Command("bd", "show", issueID, "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s", stderr.String())
	}

	// Parse to check status
	var issues []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return false, err
	}

	if len(issues) == 0 {
		return false, nil
	}

	return issues[0].Status == "closed", nil
}

// getWaitingTimestamp retrieves when we started waiting for a polecat.
func (m *Manager) getWaitingTimestamp(w *Witness, key string) time.Time {
	// Parse timestamps from SpawnedIssues with "waiting:{name}:{timestamp}" format
	for _, entry := range w.SpawnedIssues {
		if strings.HasPrefix(entry, key+":") {
			tsStr := entry[len(key)+1:]
			if ts, err := time.Parse(time.RFC3339, tsStr); err == nil {
				return ts
			}
		}
	}
	return time.Time{}
}

// recordWaiting records when we started waiting for a polecat to complete.
func (m *Manager) recordWaiting(w *Witness, key string) {
	entry := fmt.Sprintf("%s:%s", key, time.Now().Format(time.RFC3339))
	w.SpawnedIssues = append(w.SpawnedIssues, entry)
	_ = m.saveState(w)
}

// clearWaiting removes the waiting timestamp for a polecat.
func (m *Manager) clearWaiting(w *Witness, key string) {
	var filtered []string
	for _, entry := range w.SpawnedIssues {
		if !strings.HasPrefix(entry, key) {
			filtered = append(filtered, entry)
		}
	}
	w.SpawnedIssues = filtered
	_ = m.saveState(w)
}

// cleanupPolecat performs the full cleanup sequence for a transient polecat.
// 1. Check for uncommitted work (stubbornly refuses to lose work)
// 2. Kill session
// 3. Remove worktree
// 4. Delete branch
//
// If the polecat has uncommitted work (changes, stashes, or unpushed commits),
// the cleanup is aborted and an error is returned. The Witness will retry later.
func (m *Manager) cleanupPolecat(polecatName string) error {
	fmt.Printf("  Cleaning up polecat %s...\n", polecatName)

	// Get managers
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)
	polecatGit := git.NewGit(m.rig.Path)
	polecatMgr := polecat.NewManager(m.rig, polecatGit)

	// Get polecat path for git check
	polecatPath := filepath.Join(m.rig.Path, "polecats", polecatName)

	// 1. Check for uncommitted work BEFORE doing anything destructive
	pGit := git.NewGit(polecatPath)
	status, err := pGit.CheckUncommittedWork()
	if err != nil {
		// If we can't check (e.g., not a git repo), log warning but continue
		fmt.Printf("    Warning: could not check uncommitted work: %v\n", err)
	} else if !status.Clean() {
		// REFUSE to clean up - this is the key safety feature
		fmt.Printf("    REFUSING to cleanup - polecat has uncommitted work:\n")
		if status.HasUncommittedChanges {
			fmt.Printf("      • %d uncommitted change(s)\n", len(status.ModifiedFiles)+len(status.UntrackedFiles))
		}
		if status.StashCount > 0 {
			fmt.Printf("      • %d stash(es)\n", status.StashCount)
		}
		if status.UnpushedCommits > 0 {
			fmt.Printf("      • %d unpushed commit(s)\n", status.UnpushedCommits)
		}
		return fmt.Errorf("polecat %s has uncommitted work: %s", polecatName, status.String())
	}

	// 2. Kill session
	running, err := sessMgr.IsRunning(polecatName)
	if err == nil && running {
		fmt.Printf("    Killing session...\n")
		if err := sessMgr.Stop(polecatName, true); err != nil {
			fmt.Printf("    Warning: failed to stop session: %v\n", err)
		}
	}

	// 3. Remove worktree (this also removes the directory)
	// Use force=true since we've already verified no uncommitted work
	fmt.Printf("    Removing worktree...\n")
	if err := polecatMgr.RemoveWithOptions(polecatName, true, true); err != nil {
		// Only error if polecat actually exists
		if !errors.Is(err, polecat.ErrPolecatNotFound) {
			return fmt.Errorf("removing worktree: %w", err)
		}
	}

	// 4. Delete branch from mayor's clone
	branchName := fmt.Sprintf("polecat/%s", polecatName)
	mayorPath := filepath.Join(m.rig.Path, "mayor", "rig")
	mayorGit := git.NewGit(mayorPath)

	fmt.Printf("    Deleting branch %s...\n", branchName)
	if err := mayorGit.DeleteBranch(branchName, true); err != nil {
		// Branch might already be deleted or merged, not a critical error
		fmt.Printf("    Warning: failed to delete branch: %v\n", err)
	}

	return nil
}

// processExists checks if a process with the given PID exists.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; signal 0 tests existence
	err = proc.Signal(nil)
	return err == nil
}

// ReadyIssue represents an issue from bd ready --json output.
type ReadyIssue struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"issue_type"`
	Status string `json:"status"`
}

// autoSpawnForReadyWork spawns polecats for ready work up to capacity.
func (m *Manager) autoSpawnForReadyWork(w *Witness) error {
	// Get current active polecat count
	activeCount, err := m.getActivePolecatCount()
	if err != nil {
		return fmt.Errorf("counting polecats: %w", err)
	}

	maxWorkers := w.Config.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 4 // Default
	}

	if activeCount >= maxWorkers {
		// At capacity, nothing to do
		return nil
	}

	// Get ready issues
	issues, err := m.getReadyIssues()
	if err != nil {
		return fmt.Errorf("getting ready issues: %w", err)
	}

	// Filter issues (exclude merge-requests, epics, and already-spawned issues)
	var spawnableIssues []ReadyIssue
	for _, issue := range issues {
		// Skip merge-requests and epics
		if issue.Type == "merge-request" || issue.Type == "epic" {
			continue
		}

		// Skip if already spawned
		if m.isAlreadySpawned(w, issue.ID) {
			continue
		}

		// Filter by epic if configured
		if w.Config.EpicID != "" {
			isChild, err := m.isChildOfEpic(issue.ID, w.Config.EpicID)
			if err != nil {
				// Skip issues we can't verify - safer than including unknown work
				continue
			}
			if !isChild {
				continue
			}
		}

		// Filter by prefix if configured
		if w.Config.IssuePrefix != "" {
			if !strings.HasPrefix(issue.ID, w.Config.IssuePrefix) {
				continue
			}
		}

		spawnableIssues = append(spawnableIssues, issue)
	}

	// Spawn up to capacity
	spawnDelay := w.Config.SpawnDelayMs
	if spawnDelay <= 0 {
		spawnDelay = 5000 // Default 5 seconds
	}

	spawned := 0
	for _, issue := range spawnableIssues {
		if activeCount+spawned >= maxWorkers {
			break
		}

		fmt.Printf("Auto-spawning for issue %s: %s\n", issue.ID, issue.Title)

		if err := m.spawnPolecat(issue.ID); err != nil {
			fmt.Printf("  Spawn failed: %v\n", err)
			continue
		}

		// Track that we spawned for this issue
		w.SpawnedIssues = append(w.SpawnedIssues, issue.ID)
		spawned++

		// Delay between spawns
		if spawned < len(spawnableIssues) && activeCount+spawned < maxWorkers {
			time.Sleep(time.Duration(spawnDelay) * time.Millisecond)
		}
	}

	if spawned > 0 {
		// Save state to persist spawned issues list
		return m.saveState(w)
	}

	return nil
}

// getActivePolecatCount returns the number of polecats with active tmux sessions.
func (m *Manager) getActivePolecatCount() (int, error) {
	polecatMgr := polecat.NewManager(m.rig, git.NewGit(m.rig.Path))
	polecats, err := polecatMgr.List()
	if err != nil {
		return 0, err
	}

	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)

	count := 0
	for _, p := range polecats {
		running, _ := sessMgr.IsRunning(p.Name)
		if running {
			count++
		}
	}

	return count, nil
}

// getReadyIssues returns issues ready to work (no blockers).
func (m *Manager) getReadyIssues() ([]ReadyIssue, error) {
	cmd := exec.Command("bd", "ready", "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s", stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, nil
	}

	var issues []ReadyIssue
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing ready issues: %w", err)
	}

	return issues, nil
}

// issueDependency represents a dependency from bd show --json output.
type issueDependency struct {
	ID             string `json:"id"`
	DependencyType string `json:"dependency_type"`
}

// issueWithDeps represents an issue with its dependencies from bd show --json.
type issueWithDeps struct {
	ID         string            `json:"id"`
	Dependents []issueDependency `json:"dependents"`
}

// isChildOfEpic checks if an issue blocks (is a child of) the given epic.
func (m *Manager) isChildOfEpic(issueID, epicID string) (bool, error) {
	cmd := exec.Command("bd", "show", issueID, "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s", stderr.String())
	}

	var issues []issueWithDeps
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return false, fmt.Errorf("parsing issue: %w", err)
	}

	if len(issues) == 0 {
		return false, nil
	}

	// Check if the epic is in the dependents with type "blocks"
	for _, dep := range issues[0].Dependents {
		if dep.ID == epicID && dep.DependencyType == "blocks" {
			return true, nil
		}
	}

	return false, nil
}

// isAlreadySpawned checks if an issue has already been spawned.
func (m *Manager) isAlreadySpawned(w *Witness, issueID string) bool {
	for _, id := range w.SpawnedIssues {
		if id == issueID {
			return true
		}
	}
	return false
}

// spawnPolecat spawns a polecat for an issue using gt spawn.
func (m *Manager) spawnPolecat(issueID string) error {
	cmd := exec.Command("gt", "spawn", "--rig", m.rig.Name, "--issue", issueID)
	cmd.Dir = m.workDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}

	fmt.Printf("  Spawned: %s\n", strings.TrimSpace(string(output)))
	return nil
}
