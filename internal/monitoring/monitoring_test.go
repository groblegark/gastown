package monitoring

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// types.go — AgentStatus constants
// ---------------------------------------------------------------------------

func TestAgentStatusConstants(t *testing.T) {
	tests := []struct {
		status AgentStatus
		want   string
	}{
		{StatusAvailable, "available"},
		{StatusWorking, "working"},
		{StatusThinking, "thinking"},
		{StatusBlocked, "blocked"},
		{StatusWaiting, "waiting"},
		{StatusReviewing, "reviewing"},
		{StatusIdle, "idle"},
		{StatusPaused, "paused"},
		{StatusError, "error"},
		{StatusOffline, "offline"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("status constant %q != %q", tt.status, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// types.go — IsHealthy
// ---------------------------------------------------------------------------

func TestIsHealthy(t *testing.T) {
	tests := []struct {
		status AgentStatus
		want   bool
	}{
		{StatusAvailable, true},
		{StatusWorking, true},
		{StatusThinking, true},
		{StatusReviewing, true},
		{StatusBlocked, false},
		{StatusWaiting, false},
		{StatusIdle, false},
		{StatusPaused, false},
		{StatusError, false},
		{StatusOffline, false},
		{AgentStatus("unknown"), false},
		{AgentStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsHealthy(); got != tt.want {
				t.Errorf("AgentStatus(%q).IsHealthy() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// types.go — NeedsAttention
// ---------------------------------------------------------------------------

func TestNeedsAttention(t *testing.T) {
	tests := []struct {
		status AgentStatus
		want   bool
	}{
		{StatusBlocked, true},
		{StatusError, true},
		{StatusIdle, true},
		{StatusAvailable, false},
		{StatusWorking, false},
		{StatusThinking, false},
		{StatusReviewing, false},
		{StatusWaiting, false},
		{StatusPaused, false},
		{StatusOffline, false},
		{AgentStatus("unknown"), false},
		{AgentStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.NeedsAttention(); got != tt.want {
				t.Errorf("AgentStatus(%q).NeedsAttention() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// types.go — IsHealthy and NeedsAttention are mutually exclusive
// ---------------------------------------------------------------------------

func TestHealthyAndAttentionMutuallyExclusive(t *testing.T) {
	allStatuses := []AgentStatus{
		StatusAvailable, StatusWorking, StatusThinking, StatusBlocked,
		StatusWaiting, StatusReviewing, StatusIdle, StatusPaused,
		StatusError, StatusOffline,
	}
	for _, s := range allStatuses {
		if s.IsHealthy() && s.NeedsAttention() {
			t.Errorf("AgentStatus(%q) is both healthy and needs attention", s)
		}
	}
}

// ---------------------------------------------------------------------------
// types.go — StatusSource constants
// ---------------------------------------------------------------------------

func TestStatusSourceConstants(t *testing.T) {
	tests := []struct {
		source StatusSource
		want   string
	}{
		{SourceBossOverride, "boss"},
		{SourceSelfReported, "self"},
		{SourceInferred, "inferred"},
	}
	for _, tt := range tests {
		if string(tt.source) != tt.want {
			t.Errorf("StatusSource constant %q != %q", tt.source, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// types.go — StatusReport.Duration
// ---------------------------------------------------------------------------

func TestStatusReportDuration(t *testing.T) {
	t.Run("zero Since returns zero duration", func(t *testing.T) {
		r := StatusReport{}
		if d := r.Duration(); d != 0 {
			t.Errorf("Duration() with zero Since = %v, want 0", d)
		}
	})

	t.Run("past Since returns positive duration", func(t *testing.T) {
		r := StatusReport{
			Since: time.Now().Add(-5 * time.Second),
		}
		d := r.Duration()
		if d < 4*time.Second || d > 6*time.Second {
			t.Errorf("Duration() = %v, want ~5s", d)
		}
	})

	t.Run("recent Since returns small duration", func(t *testing.T) {
		r := StatusReport{
			Since: time.Now(),
		}
		d := r.Duration()
		if d < 0 || d > time.Second {
			t.Errorf("Duration() = %v, want ~0", d)
		}
	})
}

// ---------------------------------------------------------------------------
// types.go — StatusReport field population
// ---------------------------------------------------------------------------

func TestStatusReportFields(t *testing.T) {
	now := time.Now()
	r := StatusReport{
		AgentID:      "agent-1",
		Status:       StatusWorking,
		Source:       SourceSelfReported,
		Since:        now,
		Message:      "compiling",
		LastActivity: now,
	}
	if r.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", r.AgentID, "agent-1")
	}
	if r.Status != StatusWorking {
		t.Errorf("Status = %q, want %q", r.Status, StatusWorking)
	}
	if r.Source != SourceSelfReported {
		t.Errorf("Source = %q, want %q", r.Source, SourceSelfReported)
	}
	if r.Message != "compiling" {
		t.Errorf("Message = %q, want %q", r.Message, "compiling")
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — empty input
// ---------------------------------------------------------------------------

func TestDetectEmptyInput(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []string{"", "  ", "\t", "\n", "  \n\t  "}
	for _, input := range tests {
		if got := reg.Detect(input); got != "" {
			t.Errorf("Detect(%q) = %q, want empty string", input, got)
		}
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — non-matching non-empty returns StatusWorking
// ---------------------------------------------------------------------------

func TestDetectNonMatchingReturnsWorking(t *testing.T) {
	reg := NewPatternRegistry()
	inputs := []string{
		"hello world",
		"running tests",
		"compiling code",
		"ls -la",
		"git commit -m 'fix'",
		"12345",
	}
	for _, input := range inputs {
		if got := reg.Detect(input); got != StatusWorking {
			t.Errorf("Detect(%q) = %q, want %q", input, got, StatusWorking)
		}
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — error patterns
// ---------------------------------------------------------------------------

func TestDetectErrorPatterns(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []struct {
		name  string
		input string
	}{
		{"error keyword", "error: something failed"},
		{"Error capitalized", "Error: connection refused"},
		{"fatal keyword", "fatal: could not read"},
		{"panic keyword", "panic: runtime error"},
		{"crash keyword", "crash detected in module"},
		{"segfault keyword", "segfault at address 0x0"},
		{"hook error", "hook error: pre-commit failed"},
		{"Hook Error capitalized", "Hook Error found"},
		{"error at start", "error in pipeline"},
		{"error at end", "got an error"},
		{"ERROR uppercase", "ERROR: disk full"},
		{"FATAL uppercase", "FATAL: out of memory"},
		{"PANIC uppercase", "PANIC in goroutine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != StatusError {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, StatusError)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — blocked patterns
// ---------------------------------------------------------------------------

func TestDetectBlockedPatterns(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []struct {
		name  string
		input string
	}{
		{"BLOCKED: prefix", "BLOCKED: need database credentials"},
		{"blocked: lowercase", "blocked: waiting on API key"},
		{"waiting for approval", "waiting for approval from team lead"},
		{"waiting for review", "waiting for review of PR #42"},
		{"waiting for merge", "waiting for merge to main"},
		{"waiting for response", "waiting for response from upstream"},
		{"blocked by", "blocked by issue #123"},
		{"Blocked By capitalized", "Blocked By dependency on module X"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != StatusBlocked {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, StatusBlocked)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — waiting patterns
// ---------------------------------------------------------------------------

func TestDetectWaitingPatterns(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []struct {
		name  string
		input string
	}{
		{"waiting for human", "waiting for human input"},
		{"waiting for user", "waiting for user confirmation"},
		{"waiting for input", "waiting for input on terminal"},
		{"waiting for decision", "waiting for decision on architecture"},
		{"decision point", "decision point: should we refactor?"},
		{"Decision Point caps", "Decision Point reached"},
		{"awaiting response", "awaiting response from operator"},
		{"awaiting feedback", "awaiting feedback on design doc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != StatusWaiting {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, StatusWaiting)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — thinking patterns
// ---------------------------------------------------------------------------

func TestDetectThinkingPatterns(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []struct {
		name  string
		input string
	}{
		{"thinking...", "thinking..."},
		{"Thinking...", "Thinking..."},
		{"space thinking...", "agent is thinking..."},
		{"analyzing", "analyzing the codebase"},
		{"Analyzing caps", "Analyzing data patterns"},
		{"processing", "processing request"},
		{"computing", "computing diff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != StatusThinking {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, StatusThinking)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — reviewing patterns
// ---------------------------------------------------------------------------

func TestDetectReviewingPatterns(t *testing.T) {
	reg := NewPatternRegistry()
	tests := []struct {
		name  string
		input string
	}{
		{"reviewing code", "reviewing code in main.go"},
		{"reviewing changes", "reviewing changes from PR"},
		{"reviewing PR", "reviewing PR #42"},
		{"reviewing pull request", "reviewing pull request for feature"},
		{"reviewing diff", "reviewing diff output"},
		{"code review", "code review in progress"},
		{"Code Review caps", "Code Review completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != StatusReviewing {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, StatusReviewing)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — PatternRegistry.Detect — priority ordering (error wins)
// ---------------------------------------------------------------------------

func TestDetectPriorityOrdering(t *testing.T) {
	reg := NewPatternRegistry()

	// Error patterns have highest priority; even if "reviewing" is present,
	// an "error" keyword should win.
	t.Run("error beats reviewing", func(t *testing.T) {
		input := "error while reviewing code"
		if got := reg.Detect(input); got != StatusError {
			t.Errorf("Detect(%q) = %q, want %q (error takes priority)", input, got, StatusError)
		}
	})

	t.Run("error beats blocked", func(t *testing.T) {
		input := "fatal: BLOCKED: cannot proceed"
		if got := reg.Detect(input); got != StatusError {
			t.Errorf("Detect(%q) = %q, want %q (error takes priority)", input, got, StatusError)
		}
	})
}

// ---------------------------------------------------------------------------
// detector.go — AddPattern — valid regex
// ---------------------------------------------------------------------------

func TestAddPatternValid(t *testing.T) {
	reg := NewPatternRegistry()
	err := reg.AddPattern(`(?i)deploying`, StatusWorking)
	if err != nil {
		t.Fatalf("AddPattern returned unexpected error: %v", err)
	}

	// The custom pattern should now match
	if got := reg.Detect("deploying to staging"); got != StatusWorking {
		t.Errorf("Detect after AddPattern = %q, want %q", got, StatusWorking)
	}
}

// ---------------------------------------------------------------------------
// detector.go — AddPattern — invalid regex
// ---------------------------------------------------------------------------

func TestAddPatternInvalidRegex(t *testing.T) {
	reg := NewPatternRegistry()
	err := reg.AddPattern(`(?P<invalid`, StatusError)
	if err == nil {
		t.Error("AddPattern with invalid regex should return error")
	}
}

// ---------------------------------------------------------------------------
// detector.go — AddPattern — custom pattern matches
// ---------------------------------------------------------------------------

func TestAddPatternCustomMatches(t *testing.T) {
	reg := NewPatternRegistry()
	err := reg.AddPattern(`(?i)compiling`, StatusThinking)
	if err != nil {
		t.Fatalf("AddPattern returned unexpected error: %v", err)
	}

	// "compiling" would normally be StatusWorking (no default match), now it maps to Thinking
	if got := reg.Detect("compiling main package"); got != StatusThinking {
		t.Errorf("Detect(%q) = %q, want %q", "compiling main package", got, StatusThinking)
	}
}

// ---------------------------------------------------------------------------
// detector.go — AddPattern appends to end (default patterns still match first)
// ---------------------------------------------------------------------------

func TestAddPatternDoesNotOverrideDefaults(t *testing.T) {
	reg := NewPatternRegistry()
	// Add a pattern that would match "error" text, but map it to StatusWorking.
	// Since default "error" pattern comes first, it should still win.
	err := reg.AddPattern(`(?i)error`, StatusWorking)
	if err != nil {
		t.Fatalf("AddPattern returned unexpected error: %v", err)
	}

	if got := reg.Detect("error: something broke"); got != StatusError {
		t.Errorf("Detect = %q, want %q (default pattern should take priority)", got, StatusError)
	}
}

// ---------------------------------------------------------------------------
// detector.go — NewPatternRegistry returns non-nil with patterns
// ---------------------------------------------------------------------------

func TestNewPatternRegistry(t *testing.T) {
	reg := NewPatternRegistry()
	if reg == nil {
		t.Fatal("NewPatternRegistry returned nil")
	}
	if len(reg.patterns) == 0 {
		t.Error("NewPatternRegistry has no default patterns")
	}
}

// ---------------------------------------------------------------------------
// idle.go — IdleLevel.String
// ---------------------------------------------------------------------------

func TestIdleLevelString(t *testing.T) {
	tests := []struct {
		level IdleLevel
		want  string
	}{
		{IdleLevelActive, "active"},
		{IdleLevelIdle, "idle"},
		{IdleLevelStale, "stale"},
		{IdleLevelStuck, "stuck"},
		{IdleLevel(99), "unknown"},
		{IdleLevel(-1), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("IdleLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// idle.go — NewIdleDetector defaults
// ---------------------------------------------------------------------------

func TestNewIdleDetectorDefaults(t *testing.T) {
	d := NewIdleDetector()
	if d == nil {
		t.Fatal("NewIdleDetector returned nil")
	}
	if d.idleTimeout != DefaultIdleTimeout {
		t.Errorf("idleTimeout = %v, want %v", d.idleTimeout, DefaultIdleTimeout)
	}
	if d.staleTimeout != DefaultStaleTimeout {
		t.Errorf("staleTimeout = %v, want %v", d.staleTimeout, DefaultStaleTimeout)
	}
	if d.stuckTimeout != DefaultStuckTimeout {
		t.Errorf("stuckTimeout = %v, want %v", d.stuckTimeout, DefaultStuckTimeout)
	}
}

// ---------------------------------------------------------------------------
// idle.go — NewIdleDetector with options
// ---------------------------------------------------------------------------

func TestNewIdleDetectorWithOptions(t *testing.T) {
	d := NewIdleDetector(
		WithIdleTimeout(30*time.Second),
		WithStaleTimeout(1*time.Minute),
		WithStuckTimeout(3*time.Minute),
	)
	if d.idleTimeout != 30*time.Second {
		t.Errorf("idleTimeout = %v, want 30s", d.idleTimeout)
	}
	if d.staleTimeout != 1*time.Minute {
		t.Errorf("staleTimeout = %v, want 1m", d.staleTimeout)
	}
	if d.stuckTimeout != 3*time.Minute {
		t.Errorf("stuckTimeout = %v, want 3m", d.stuckTimeout)
	}
}

// ---------------------------------------------------------------------------
// idle.go — NewIdleDetector with partial options
// ---------------------------------------------------------------------------

func TestNewIdleDetectorPartialOptions(t *testing.T) {
	d := NewIdleDetector(WithIdleTimeout(45 * time.Second))
	if d.idleTimeout != 45*time.Second {
		t.Errorf("idleTimeout = %v, want 45s", d.idleTimeout)
	}
	// Other timeouts should remain at defaults
	if d.staleTimeout != DefaultStaleTimeout {
		t.Errorf("staleTimeout = %v, want default %v", d.staleTimeout, DefaultStaleTimeout)
	}
	if d.stuckTimeout != DefaultStuckTimeout {
		t.Errorf("stuckTimeout = %v, want default %v", d.stuckTimeout, DefaultStuckTimeout)
	}
}

// ---------------------------------------------------------------------------
// idle.go — Classify with default timeouts
// ---------------------------------------------------------------------------

func TestClassifyDefaultTimeouts(t *testing.T) {
	d := NewIdleDetector()

	tests := []struct {
		name         string
		lastActivity time.Time
		want         IdleLevel
	}{
		{"just now", time.Now(), IdleLevelActive},
		{"10 seconds ago", time.Now().Add(-10 * time.Second), IdleLevelActive},
		{"1 minute ago", time.Now().Add(-1 * time.Minute), IdleLevelActive},
		{"2 minutes 30 seconds ago", time.Now().Add(-2*time.Minute - 30*time.Second), IdleLevelIdle},
		{"3 minutes ago", time.Now().Add(-3 * time.Minute), IdleLevelIdle},
		{"5 minutes 30 seconds ago", time.Now().Add(-5*time.Minute - 30*time.Second), IdleLevelStale},
		{"10 minutes ago", time.Now().Add(-10 * time.Minute), IdleLevelStale},
		{"15 minutes ago", time.Now().Add(-15 * time.Minute), IdleLevelStuck},
		{"20 minutes ago", time.Now().Add(-20 * time.Minute), IdleLevelStuck},
		{"1 hour ago", time.Now().Add(-1 * time.Hour), IdleLevelStuck},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Classify(tt.lastActivity); got != tt.want {
				t.Errorf("Classify(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// idle.go — Classify zero time returns Stuck
// ---------------------------------------------------------------------------

func TestClassifyZeroTime(t *testing.T) {
	d := NewIdleDetector()
	if got := d.Classify(time.Time{}); got != IdleLevelStuck {
		t.Errorf("Classify(zero time) = %v, want %v", got, IdleLevelStuck)
	}
}

// ---------------------------------------------------------------------------
// idle.go — Classify future time (clock skew) returns Active
// ---------------------------------------------------------------------------

func TestClassifyFutureTime(t *testing.T) {
	d := NewIdleDetector()
	future := time.Now().Add(10 * time.Minute)
	if got := d.Classify(future); got != IdleLevelActive {
		t.Errorf("Classify(future) = %v, want %v", got, IdleLevelActive)
	}
}

// ---------------------------------------------------------------------------
// idle.go — Classify boundary conditions
// ---------------------------------------------------------------------------

func TestClassifyBoundaryConditions(t *testing.T) {
	d := NewIdleDetector(
		WithIdleTimeout(10*time.Second),
		WithStaleTimeout(20*time.Second),
		WithStuckTimeout(30*time.Second),
	)

	tests := []struct {
		name    string
		elapsed time.Duration
		want    IdleLevel
	}{
		{"just under idle", 9 * time.Second, IdleLevelActive},
		{"exactly idle", 10 * time.Second, IdleLevelIdle},
		{"just over idle", 11 * time.Second, IdleLevelIdle},
		{"just under stale", 19 * time.Second, IdleLevelIdle},
		{"exactly stale", 20 * time.Second, IdleLevelStale},
		{"just over stale", 21 * time.Second, IdleLevelStale},
		{"just under stuck", 29 * time.Second, IdleLevelStale},
		{"exactly stuck", 30 * time.Second, IdleLevelStuck},
		{"just over stuck", 31 * time.Second, IdleLevelStuck},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastActivity := time.Now().Add(-tt.elapsed)
			if got := d.Classify(lastActivity); got != tt.want {
				t.Errorf("Classify(-%v) = %v, want %v", tt.elapsed, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// idle.go — Classify with custom timeouts
// ---------------------------------------------------------------------------

func TestClassifyCustomTimeouts(t *testing.T) {
	d := NewIdleDetector(
		WithIdleTimeout(5*time.Second),
		WithStaleTimeout(10*time.Second),
		WithStuckTimeout(20*time.Second),
	)

	tests := []struct {
		name    string
		elapsed time.Duration
		want    IdleLevel
	}{
		{"active within 5s", 3 * time.Second, IdleLevelActive},
		{"idle at 7s", 7 * time.Second, IdleLevelIdle},
		{"stale at 12s", 12 * time.Second, IdleLevelStale},
		{"stuck at 25s", 25 * time.Second, IdleLevelStuck},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastActivity := time.Now().Add(-tt.elapsed)
			if got := d.Classify(lastActivity); got != tt.want {
				t.Errorf("Classify(-%v) = %v, want %v", tt.elapsed, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// idle.go — InferStatus mapping
// ---------------------------------------------------------------------------

func TestInferStatus(t *testing.T) {
	d := NewIdleDetector(
		WithIdleTimeout(5*time.Second),
		WithStaleTimeout(10*time.Second),
		WithStuckTimeout(20*time.Second),
	)

	tests := []struct {
		name         string
		lastActivity time.Time
		want         AgentStatus
	}{
		{"active -> working", time.Now(), StatusWorking},
		{"idle -> idle", time.Now().Add(-7 * time.Second), StatusIdle},
		{"stale -> idle", time.Now().Add(-12 * time.Second), StatusIdle},
		{"stuck -> error", time.Now().Add(-25 * time.Second), StatusError},
		{"zero time -> error (stuck)", time.Time{}, StatusError},
		{"future -> working (active)", time.Now().Add(10 * time.Minute), StatusWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.InferStatus(tt.lastActivity); got != tt.want {
				t.Errorf("InferStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// idle.go — InferStatus with default detector
// ---------------------------------------------------------------------------

func TestInferStatusDefaults(t *testing.T) {
	d := NewIdleDetector()

	t.Run("recent activity is working", func(t *testing.T) {
		if got := d.InferStatus(time.Now()); got != StatusWorking {
			t.Errorf("InferStatus(now) = %q, want %q", got, StatusWorking)
		}
	})

	t.Run("3 minutes idle", func(t *testing.T) {
		if got := d.InferStatus(time.Now().Add(-3 * time.Minute)); got != StatusIdle {
			t.Errorf("InferStatus(-3m) = %q, want %q", got, StatusIdle)
		}
	})

	t.Run("7 minutes stale maps to idle", func(t *testing.T) {
		if got := d.InferStatus(time.Now().Add(-7 * time.Minute)); got != StatusIdle {
			t.Errorf("InferStatus(-7m) = %q, want %q", got, StatusIdle)
		}
	})

	t.Run("20 minutes stuck maps to error", func(t *testing.T) {
		if got := d.InferStatus(time.Now().Add(-20 * time.Minute)); got != StatusError {
			t.Errorf("InferStatus(-20m) = %q, want %q", got, StatusError)
		}
	})
}

// ---------------------------------------------------------------------------
// idle.go — default timeout constants
// ---------------------------------------------------------------------------

func TestDefaultTimeoutConstants(t *testing.T) {
	if DefaultIdleTimeout != 2*time.Minute {
		t.Errorf("DefaultIdleTimeout = %v, want 2m", DefaultIdleTimeout)
	}
	if DefaultStaleTimeout != 5*time.Minute {
		t.Errorf("DefaultStaleTimeout = %v, want 5m", DefaultStaleTimeout)
	}
	if DefaultStuckTimeout != 15*time.Minute {
		t.Errorf("DefaultStuckTimeout = %v, want 15m", DefaultStuckTimeout)
	}
}

// ---------------------------------------------------------------------------
// detector.go — Detect handles leading/trailing whitespace in patterns
// ---------------------------------------------------------------------------

func TestDetectWhitespaceHandling(t *testing.T) {
	reg := NewPatternRegistry()

	// Strings with leading/trailing whitespace that contain pattern matches
	tests := []struct {
		name  string
		input string
		want  AgentStatus
	}{
		{"error with leading spaces", "   error: something failed   ", StatusError},
		{"blocked with tabs", "\tBLOCKED: waiting\t", StatusBlocked},
		{"thinking with newlines", "\nthinking...\n", StatusThinking},
		{"reviewing with mixed whitespace", "  \t reviewing code  \n", StatusReviewing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != tt.want {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detector.go — Detect does not false positive on substrings
// ---------------------------------------------------------------------------

func TestDetectNoFalsePositivesOnSubstrings(t *testing.T) {
	reg := NewPatternRegistry()

	// Words that contain "error" etc. as substrings but shouldn't match
	// because the regex requires word boundaries
	tests := []struct {
		name  string
		input string
		want  AgentStatus
	}{
		// "terrorism" contains "error" as substring but has different letters around it,
		// however the regex checks for whitespace/start/end boundaries
		{"mirrored has no error", "mirrored output", StatusWorking},
		{"error-free (hyphenated)", "this is errorfree text", StatusWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reg.Detect(tt.input); got != tt.want {
				t.Errorf("Detect(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration: StatusReport with InferStatus
// ---------------------------------------------------------------------------

func TestStatusReportWithInferredStatus(t *testing.T) {
	d := NewIdleDetector()
	lastActivity := time.Now().Add(-3 * time.Minute)
	status := d.InferStatus(lastActivity)

	report := StatusReport{
		AgentID:      "agent-42",
		Status:       status,
		Source:       SourceInferred,
		Since:        lastActivity,
		LastActivity: lastActivity,
	}

	if report.Status != StatusIdle {
		t.Errorf("report.Status = %q, want %q", report.Status, StatusIdle)
	}
	if report.Source != SourceInferred {
		t.Errorf("report.Source = %q, want %q", report.Source, SourceInferred)
	}
	if !report.Status.NeedsAttention() {
		t.Error("idle status should need attention")
	}
	if report.Duration() < 3*time.Minute {
		t.Errorf("Duration() = %v, want >= 3m", report.Duration())
	}
}

// ---------------------------------------------------------------------------
// Integration: PatternRegistry with StatusReport
// ---------------------------------------------------------------------------

func TestPatternRegistryWithStatusReport(t *testing.T) {
	reg := NewPatternRegistry()
	status := reg.Detect("analyzing codebase structure")

	report := StatusReport{
		AgentID: "agent-7",
		Status:  status,
		Source:  SourceInferred,
		Since:   time.Now(),
		Message: "analyzing codebase structure",
	}

	if report.Status != StatusThinking {
		t.Errorf("report.Status = %q, want %q", report.Status, StatusThinking)
	}
	if !report.Status.IsHealthy() {
		t.Error("thinking status should be healthy")
	}
}
