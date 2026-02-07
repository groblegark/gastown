// Package sling provides the core sling operations for work dispatch in Gas Town.
// Both the CLI (internal/cmd) and RPC server (internal/rpcserver) use this package
// to avoid duplicating sling logic.
package sling

import "io"

// SlingOptions contains parameters for slinging a bead to a target.
type SlingOptions struct {
	// Required
	BeadID   string
	Target   string
	TownRoot string

	// Optional behavior
	Args          string
	Subject       string
	Message       string
	Create        bool
	Force         bool
	NoConvoy      bool
	Convoy        string
	NoMerge       bool
	MergeStrategy string // "direct", "mr", "local"
	Owned         bool
	Account       string
	Agent         string
	HookRawBead   bool
	DryRun        bool
	Vars          []string // extra formula variables

	// Dependencies (injected by caller)
	// ResolveTarget resolves a target string to (agentID, pane, workDir).
	// Required for targets that are existing agents (not rigs, dogs, or crew).
	// CLI provides tmux-based resolution; RPC can provide nil if only targeting rigs.
	ResolveTarget ResolveTargetFunc
	// ResolveSelf resolves the current agent's identity.
	// Required when Target is empty (sling to self).
	ResolveSelf ResolveSelfFunc
	// Output is where progress messages are written.
	// CLI uses os.Stdout; RPC uses io.Discard.
	Output io.Writer
}

// SlingResult contains the structured result of a sling operation.
type SlingResult struct {
	BeadID         string
	TargetAgent    string
	ConvoyID       string
	PolecatSpawned bool
	PolecatName    string
	BeadTitle      string
	ConvoyCreated  bool
	// TargetPane is set when the target has a tmux pane (for CLI nudging).
	TargetPane string
}

// FormulaOptions contains parameters for formula slinging.
type FormulaOptions struct {
	// Required
	Formula  string
	TownRoot string

	// Optional
	Target  string
	OnBead  string
	Vars    []string // key=value pairs
	Args    string
	Subject string
	Message string
	Create  bool
	Force   bool
	Account string
	Agent   string
	DryRun  bool

	// Dependencies
	ResolveTarget ResolveTargetFunc
	ResolveSelf   ResolveSelfFunc
	Output        io.Writer
}

// FormulaResult contains the structured result of a formula sling.
type FormulaResult struct {
	WispID         string
	TargetAgent    string
	BeadID         string
	ConvoyID       string
	PolecatSpawned bool
	PolecatName    string
	TargetPane     string
}

// BatchOptions contains parameters for batch slinging.
type BatchOptions struct {
	// Required
	BeadIDs  []string
	Rig      string
	TownRoot string

	// Optional
	Args          string
	Message       string
	Force         bool
	Account       string
	Agent         string
	Create        bool
	NoConvoy      bool
	Convoy        string
	MergeStrategy string
	Owned         bool
	Vars          []string
	DryRun        bool

	// Dependencies
	Output io.Writer
}

// BatchResult contains the structured result of a batch sling.
type BatchResult struct {
	Results      []*BatchSlingResult
	ConvoyID     string
	SuccessCount int32
	FailureCount int32
}

// BatchSlingResult contains the result for a single bead in a batch.
type BatchSlingResult struct {
	BeadID      string
	Success     bool
	Error       string
	TargetAgent string
	PolecatName string
}

// UnslingOptions contains parameters for unslinging.
type UnslingOptions struct {
	BeadID   string
	Force    bool
	TownRoot string
}

// UnslingResult contains the structured result of an unsling operation.
type UnslingResult struct {
	BeadID        string
	PreviousAgent string
	WasIncomplete bool
}

// SpawnOptions contains options for spawning a polecat via sling.
type SpawnOptions struct {
	Force    bool
	Account  string
	Create   bool
	HookBead string
	Agent    string
}

// SpawnResult contains info about a spawned polecat.
type SpawnResult struct {
	RigName     string
	PolecatName string
	ClonePath   string
	SessionName string
	Pane        string
	// Internal fields for deferred session start
	Account string
	Agent   string
}

// AgentID returns the agent identifier (e.g., "gastown/polecats/Toast").
func (s *SpawnResult) AgentID() string {
	return s.RigName + "/polecats/" + s.PolecatName
}

// FormulaOnBeadResult contains the result of instantiating a formula on a bead.
type FormulaOnBeadResult struct {
	WispRootID string // The wisp root ID (compound root after bonding)
	BeadToHook string // The bead ID to hook (BASE bead, not wisp)
}

// BeadInfo holds status and assignee for a bead.
type BeadInfo struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
}

// ConvoyOptions holds optional settings for convoy creation.
type ConvoyOptions struct {
	Owned         bool
	MergeStrategy string
}

// DogDispatchOptions contains options for dispatching work to a dog.
type DogDispatchOptions struct {
	Create            bool
	WorkDesc          string
	DelaySessionStart bool
}

// DogDispatchResult contains information about a dog dispatch.
type DogDispatchResult struct {
	DogName string
	AgentID string
	Pane    string
	Spawned bool
}

// OjDispatchResult contains info about a dispatched OJ sling job.
type OjDispatchResult struct {
	JobID       string
	PolecatName string
	AgentID     string
}

// ResolveTargetFunc resolves a target string to agent ID, pane, and work directory.
type ResolveTargetFunc func(target string) (agentID, pane, workDir string, err error)

// ResolveSelfFunc resolves the current agent's identity.
type ResolveSelfFunc func() (agentID, pane, workDir string, err error)
