package terminal

import (
	"github.com/steveyegge/gastown/internal/tmux"
)

// localTmux is the subset of tmux.Tmux methods used by TmuxBackend.
type localTmux interface {
	HasSession(name string) (bool, error)
	CapturePane(session string, lines int) (string, error)
	CapturePaneAll(session string) (string, error)
	CapturePaneLines(session string, lines int) ([]string, error)
	NudgeSession(session, message string) error
	SendKeysRaw(session, keys string) error
	IsPaneDead(session string) (bool, error)
	SetPaneDiedHook(session, agentID string) error
}

// TmuxBackend wraps a local tmux instance to implement Backend.
// This is the default backend for locally-running agents.
type TmuxBackend struct {
	tmux localTmux
}

// NewTmuxBackend creates a Backend backed by local tmux.
func NewTmuxBackend(t *tmux.Tmux) *TmuxBackend {
	return &TmuxBackend{tmux: t}
}

func (b *TmuxBackend) HasSession(session string) (bool, error) {
	return b.tmux.HasSession(session)
}

func (b *TmuxBackend) CapturePane(session string, lines int) (string, error) {
	return b.tmux.CapturePane(session, lines)
}

func (b *TmuxBackend) CapturePaneAll(session string) (string, error) {
	return b.tmux.CapturePaneAll(session)
}

func (b *TmuxBackend) CapturePaneLines(session string, lines int) ([]string, error) {
	return b.tmux.CapturePaneLines(session, lines)
}

func (b *TmuxBackend) NudgeSession(session string, message string) error {
	return b.tmux.NudgeSession(session, message)
}

func (b *TmuxBackend) SendKeys(session string, keys string) error {
	return b.tmux.SendKeysRaw(session, keys)
}

func (b *TmuxBackend) IsPaneDead(session string) (bool, error) {
	return b.tmux.IsPaneDead(session)
}

func (b *TmuxBackend) SetPaneDiedHook(session, agentID string) error {
	return b.tmux.SetPaneDiedHook(session, agentID)
}
