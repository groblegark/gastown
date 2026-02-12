// Package tmux provides stub implementations of tmux operations.
//
// In the K8s-only architecture, tmux is not used for agent session management.
// All agent sessions run via Coop sidecars in Kubernetes pods. This package
// provides no-op/error stubs so that legacy code paths compile but gracefully
// degrade when tmux operations are attempted.
//
// This stub package replaces the original 3,659-line tmux subprocess wrapper
// as part of the K8s Habitat epic (bd-qgn31, Sub-epic A).
package tmux

import (
	"errors"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

// ErrTmuxRemoved is returned by all tmux operations in the K8s-only architecture.
var ErrTmuxRemoved = errors.New("tmux is not available in K8s-only mode")

// Common errors kept for API compatibility.
var (
	ErrNoServer        = errors.New("no tmux server running")
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionNotFound = errors.New("session not found")
)

// Tmux is a stub that replaces the tmux subprocess wrapper.
// All methods return ErrTmuxRemoved or safe zero values.
type Tmux struct{}

// NewTmux creates a new Tmux stub.
func NewTmux() *Tmux {
	return &Tmux{}
}

// IsInsideTmux returns false in K8s-only mode.
func IsInsideTmux() bool {
	return false
}

// SessionSet is a set of tmux session names (stub).
type SessionSet struct {
	Names map[string]bool
}

// Has returns true if the session set contains the given name.
func (s *SessionSet) Has(name string) bool {
	if s == nil || s.Names == nil {
		return false
	}
	return s.Names[name]
}

// SessionInfo holds metadata about a tmux session (stub).
type SessionInfo struct {
	Name         string
	Windows      int
	Created      string
	Attached     bool
	Activity     string
	LastAttached string
}

// --- Session lifecycle ---

func (t *Tmux) NewSession(name, workDir string) error                                 { return ErrTmuxRemoved }
func (t *Tmux) NewSessionWithCommand(name, workDir, command string) error              { return ErrTmuxRemoved }
func (t *Tmux) EnsureSessionFresh(name, workDir string) error                          { return ErrTmuxRemoved }
func (t *Tmux) KillSession(name string) error                                         { return ErrTmuxRemoved }
func (t *Tmux) KillSessionWithProcesses(name string) error                             { return ErrTmuxRemoved }
func (t *Tmux) KillSessionWithProcessesExcluding(name string, excludePIDs []string) error { return ErrTmuxRemoved }
func (t *Tmux) KillPaneProcesses(pane string) error                                    { return ErrTmuxRemoved }
func (t *Tmux) KillPaneProcessesExcluding(pane string, excludePIDs []string) error     { return ErrTmuxRemoved }
func (t *Tmux) KillServer() error                                                      { return ErrTmuxRemoved }

// --- Session queries ---

func (t *Tmux) HasSession(name string) (bool, error)           { return false, ErrTmuxRemoved }
func (t *Tmux) IsAvailable() bool                              { return false }
func (t *Tmux) GetCurrentSessionName() (string, error)         { return "", ErrTmuxRemoved }
func (t *Tmux) ListSessions() ([]string, error)                { return nil, ErrTmuxRemoved }
func (t *Tmux) GetSessionSet() (*SessionSet, error)            { return nil, ErrTmuxRemoved }
func (t *Tmux) ListSessionIDs() (map[string]string, error)     { return nil, ErrTmuxRemoved }
func (t *Tmux) GetSessionInfo(name string) (*SessionInfo, error) { return nil, ErrTmuxRemoved }
func (t *Tmux) IsSessionAttached(target string) bool           { return false }

// --- Key sending ---

func (t *Tmux) SendKeys(session, keys string) error                                    { return ErrTmuxRemoved }
func (t *Tmux) SendKeysRaw(session, keys string) error                                 { return ErrTmuxRemoved }
func (t *Tmux) SendKeysDebounced(session, keys string, debounceMs int) error            { return ErrTmuxRemoved }
func (t *Tmux) SendKeysReplace(session, keys string, clearDelayMs int) error            { return ErrTmuxRemoved }
func (t *Tmux) SendKeysDelayed(session, keys string, delayMs int) error                 { return ErrTmuxRemoved }
func (t *Tmux) SendKeysDelayedDebounced(session, keys string, preDelayMs, debounceMs int) error { return ErrTmuxRemoved }

// --- Nudge ---

func (t *Tmux) NudgeSession(session, message string) error { return ErrTmuxRemoved }
func (t *Tmux) NudgePane(pane, message string) error       { return ErrTmuxRemoved }

// --- Capture ---

func (t *Tmux) CapturePane(session string, lines int) (string, error)      { return "", ErrTmuxRemoved }
func (t *Tmux) CapturePaneAll(session string) (string, error)              { return "", ErrTmuxRemoved }
func (t *Tmux) CapturePaneLines(session string, lines int) ([]string, error) { return nil, ErrTmuxRemoved }
func (t *Tmux) CaptureDeadPaneOutput(session string, lines int) string     { return "" }

// --- Pane state ---

func (t *Tmux) IsPaneDead(session string) (bool, error)       { return false, ErrTmuxRemoved }
func (t *Tmux) GetPaneExitStatus(session string) string        { return "" }
func (t *Tmux) GetPaneCommand(session string) (string, error)  { return "", ErrTmuxRemoved }
func (t *Tmux) GetPaneID(session string) (string, error)       { return "", ErrTmuxRemoved }
func (t *Tmux) ListAllPaneIDs(session string) ([]string, error) { return nil, ErrTmuxRemoved }
func (t *Tmux) GetPaneWorkDir(session string) (string, error)  { return "", ErrTmuxRemoved }
func (t *Tmux) GetPanePID(session string) (string, error)      { return "", ErrTmuxRemoved }
func (t *Tmux) ListPanePIDs(session string) ([]string, error)  { return nil, ErrTmuxRemoved }
func (t *Tmux) GetSessionName(pane string) (string, error)     { return "", ErrTmuxRemoved }
func (t *Tmux) SetPaneDiedHook(session, agentID string) error  { return ErrTmuxRemoved }
func (t *Tmux) SetRemainOnExit(pane string, on bool) error     { return ErrTmuxRemoved }

// --- Environment ---

func (t *Tmux) SetEnvironment(session, key, value string) error           { return ErrTmuxRemoved }
func (t *Tmux) GetEnvironment(session, key string) (string, error)        { return "", ErrTmuxRemoved }
func (t *Tmux) GetAllEnvironment(session string) (map[string]string, error) { return nil, ErrTmuxRemoved }

// --- Agent detection ---

func (t *Tmux) IsAgentRunning(session string, expectedPaneCommands ...string) bool { return false }
func (t *Tmux) IsRuntimeRunning(session string, processNames []string) bool        { return false }
func (t *Tmux) IsAgentAlive(session string) bool                                   { return false }

// --- Wait ---

func (t *Tmux) WaitForAgentReady(session string, timeout time.Duration) error              { return ErrTmuxRemoved }
func (t *Tmux) WaitForCommand(session string, excludeCommands []string, timeout time.Duration) error { return ErrTmuxRemoved }
func (t *Tmux) WaitForShellReady(session string, timeout time.Duration) error              { return ErrTmuxRemoved }

func (t *Tmux) WaitForRuntimeReady(session string, rc *config.RuntimeConfig, timeout time.Duration) error {
	return ErrTmuxRemoved
}

// --- Attach ---

func (t *Tmux) AttachSession(session string) error     { return ErrTmuxRemoved }
func (t *Tmux) SwitchClient(targetSession string) error { return ErrTmuxRemoved }

// --- Notifications ---

func (t *Tmux) DisplayMessage(session, message string, durationMs int) error { return ErrTmuxRemoved }
func (t *Tmux) DisplayMessageDefault(session, message string) error          { return ErrTmuxRemoved }
func (t *Tmux) SendNotificationBanner(session, from, subject string) error   { return ErrTmuxRemoved }

// --- Appearance ---

func (t *Tmux) AcceptBypassPermissionsWarning(session string) error { return ErrTmuxRemoved }
func (t *Tmux) GetOption(session, option string) (string, error)   { return "", ErrTmuxRemoved }
func (t *Tmux) ApplyTheme(session string, theme Theme) error       { return ErrTmuxRemoved }
func (t *Tmux) SetStatusFormat(session, rig, worker, role string) error { return ErrTmuxRemoved }
func (t *Tmux) SetDynamicStatus(session string) error              { return ErrTmuxRemoved }
func (t *Tmux) ConfigureGasTownSession(session string, theme Theme, rig, worker, role string) error {
	return ErrTmuxRemoved
}
func (t *Tmux) EnableMouseMode(session string) error      { return ErrTmuxRemoved }
func (t *Tmux) SetMailClickBinding(session string) error   { return ErrTmuxRemoved }

// --- Window management ---

func (t *Tmux) SelectWindow(session string, index int) error                    { return ErrTmuxRemoved }
func (t *Tmux) SelectWindowByTarget(target string) error                         { return ErrTmuxRemoved }
func (t *Tmux) ListWindowNames(session string) ([]string, error)                 { return nil, ErrTmuxRemoved }
func (t *Tmux) NewWindow(session, windowName, workDir, command string) error     { return ErrTmuxRemoved }

// --- Pane management ---

func (t *Tmux) RespawnPane(pane, command string) error                       { return ErrTmuxRemoved }
func (t *Tmux) RespawnPaneWithWorkDir(pane, workDir, command string) error   { return ErrTmuxRemoved }
func (t *Tmux) ClearHistory(pane string) error                               { return ErrTmuxRemoved }

// --- Key bindings ---

func (t *Tmux) SetCrewCycleBindings(session string) error { return ErrTmuxRemoved }
func (t *Tmux) SetTownCycleBindings(session string) error  { return ErrTmuxRemoved }
func (t *Tmux) SetCycleBindings(session string) error      { return ErrTmuxRemoved }
func (t *Tmux) SetFeedBinding(session string) error        { return ErrTmuxRemoved }
func (t *Tmux) SetAgentsBinding(session string) error      { return ErrTmuxRemoved }

// --- Session rename ---

func (t *Tmux) RenameSession(oldName, newName string) error { return ErrTmuxRemoved }

// --- Wake ---

func (t *Tmux) WakePane(target string)              {}
func (t *Tmux) WakePaneIfDetached(target string)    {}

// --- Global options ---

func (t *Tmux) SetExitEmpty(on bool) error { return ErrTmuxRemoved }

// --- Search ---

func (t *Tmux) FindSessionByWorkDir(targetDir string, processNames []string) ([]string, error) {
	return nil, ErrTmuxRemoved
}

// --- Cleanup ---

func (t *Tmux) CleanupOrphanedSessions() (cleaned int, err error) { return 0, ErrTmuxRemoved }
