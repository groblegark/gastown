package hooks

// Event represents a Claude Code hook event type.
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventPreCompact       Event = "PreCompact"
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventStop             Event = "Stop"
)

// AllEvents returns all known hook event types in execution order.
func AllEvents() []Event {
	return []Event{
		EventSessionStart,
		EventPreCompact,
		EventUserPromptSubmit,
		EventPreToolUse,
		EventPostToolUse,
		EventStop,
	}
}

// IsValid returns true if the event is a known Claude Code hook type.
func (e Event) IsValid() bool {
	for _, valid := range AllEvents() {
		if e == valid {
			return true
		}
	}
	return false
}

// HookResult captures the outcome of executing a single hook command.
type HookResult struct {
	Command  string `json:"command"`
	Matcher  string `json:"matcher,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"` // Non-exec errors (e.g. timeout, command not found)
	Success  bool   `json:"success"`
}
