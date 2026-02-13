package terminal

// SessionInfo holds metadata about an agent session.
type SessionInfo struct {
	Name         string
	Windows      int
	Created      string
	Attached     bool
	Activity     string
	LastAttached string
}
