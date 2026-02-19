package rpcserver

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"connectrpc.com/connect"

	beadspkg "github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/mail"
)

func TestContainsAny(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		substrs []string
		want    bool
	}{
		{"match first", "file not found", []string{"not found", "missing"}, true},
		{"match second", "resource is missing", []string{"not found", "missing"}, true},
		{"no match", "everything is fine", []string{"not found", "missing"}, false},
		{"empty substrs", "anything", []string{}, false},
		{"empty string", "", []string{"a"}, false},
		{"empty both", "", []string{}, false},
		{"exact match", "duplicate", []string{"duplicate"}, true},
		{"case sensitive", "NOT FOUND", []string{"not found"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsAny(tt.s, tt.substrs...)
			if got != tt.want {
				t.Errorf("containsAny(%q, %v) = %v, want %v", tt.s, tt.substrs, got, tt.want)
			}
		})
	}
}

func TestTruncateLog(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want string
	}{
		{"short string", "hello", "hello"},
		{"exactly 1000", string(make([]byte, 1000)), string(make([]byte, 1000))},
		{"over 1000", string(make([]byte, 1001)), string(make([]byte, 1000)) + "...(truncated)"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLog(tt.s)
			if tt.name == "over 1000" {
				if len(got) != 1000+len("...(truncated)") {
					t.Errorf("truncateLog len = %d, want %d", len(got), 1000+len("...(truncated)"))
				}
			} else {
				if got != tt.want {
					t.Errorf("truncateLog(%q) = %q, want %q", tt.s, got, tt.want)
				}
			}
		})
	}
}

func TestWithRetryAfter(t *testing.T) {
	t.Run("positive seconds", func(t *testing.T) {
		err := connect.NewError(connect.CodeUnavailable, fmt.Errorf("temp error"))
		result := withRetryAfter(err, 30)
		if result.Meta().Get("Retry-After") != "30" {
			t.Errorf("Retry-After = %q, want %q", result.Meta().Get("Retry-After"), "30")
		}
	})

	t.Run("zero seconds", func(t *testing.T) {
		err := connect.NewError(connect.CodeUnavailable, fmt.Errorf("temp error"))
		result := withRetryAfter(err, 0)
		if result.Meta().Get("Retry-After") != "" {
			t.Errorf("Retry-After should be empty for 0 seconds, got %q", result.Meta().Get("Retry-After"))
		}
	})

	t.Run("negative seconds", func(t *testing.T) {
		err := connect.NewError(connect.CodeUnavailable, fmt.Errorf("temp error"))
		result := withRetryAfter(err, -1)
		if result.Meta().Get("Retry-After") != "" {
			t.Errorf("Retry-After should be empty for negative seconds, got %q", result.Meta().Get("Retry-After"))
		}
	})
}

func TestUnavailableErr(t *testing.T) {
	err := unavailableErr("read config", fmt.Errorf("disk full"), 10)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("code = %v, want CodeUnavailable", connect.CodeOf(err))
	}
	if err.Meta().Get("Retry-After") != "10" {
		t.Errorf("Retry-After = %q, want %q", err.Meta().Get("Retry-After"), "10")
	}
}

func TestInvalidArg(t *testing.T) {
	err := invalidArg("bead_id", "is required")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want CodeInvalidArgument", connect.CodeOf(err))
	}
	if err.Error() != "invalid_argument: bead_id: is required" {
		t.Errorf("message = %q", err.Error())
	}
}

func TestInternalErr(t *testing.T) {
	err := internalErr("process beads", fmt.Errorf("nil pointer"))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("code = %v, want CodeInternal", connect.CodeOf(err))
	}
}

func TestCmdExecErr(t *testing.T) {
	t.Run("context canceled", func(t *testing.T) {
		err := cmdExecErr("fetch", context.Canceled, nil)
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("code = %v, want CodeCanceled", connect.CodeOf(err))
		}
	})

	t.Run("context deadline exceeded", func(t *testing.T) {
		err := cmdExecErr("fetch", context.DeadlineExceeded, nil)
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("code = %v, want CodeCanceled", connect.CodeOf(err))
		}
	})

	t.Run("not found in output", func(t *testing.T) {
		exitErr := &exec.ExitError{}
		// We can't easily construct an ExitError with a code, so test the non-exit path
		err := cmdExecErr("fetch", exitErr, []byte("resource not found"))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("permission denied in output", func(t *testing.T) {
		exitErr := &exec.ExitError{}
		err := cmdExecErr("write", exitErr, []byte("Permission Denied for user"))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("code = %v, want CodePermissionDenied", connect.CodeOf(err))
		}
	})

	t.Run("already exists in output", func(t *testing.T) {
		exitErr := &exec.ExitError{}
		err := cmdExecErr("create", exitErr, []byte("bead already exists"))
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("code = %v, want CodeAlreadyExists", connect.CodeOf(err))
		}
	})

	t.Run("generic non-exit error", func(t *testing.T) {
		err := cmdExecErr("run", fmt.Errorf("command not found"), nil)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("code = %v, want CodeUnavailable", connect.CodeOf(err))
		}
		if err.Meta().Get("Retry-After") != "5" {
			t.Errorf("Retry-After = %q, want %q", err.Meta().Get("Retry-After"), "5")
		}
	})
}

func TestClassifyErr(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := classifyErr("op", nil); got != nil {
			t.Errorf("classifyErr(nil) = %v, want nil", got)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		err := classifyErr("op", context.Canceled)
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("code = %v, want CodeCanceled", connect.CodeOf(err))
		}
	})

	t.Run("context deadline", func(t *testing.T) {
		err := classifyErr("op", context.DeadlineExceeded)
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("code = %v, want CodeCanceled", connect.CodeOf(err))
		}
	})

	t.Run("beads not found", func(t *testing.T) {
		err := classifyErr("op", beadspkg.ErrNotFound)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("mail message not found", func(t *testing.T) {
		err := classifyErr("op", mail.ErrMessageNotFound)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("beads not installed", func(t *testing.T) {
		err := classifyErr("op", beadspkg.ErrNotInstalled)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("code = %v, want CodeUnavailable", connect.CodeOf(err))
		}
	})

	t.Run("not found pattern", func(t *testing.T) {
		err := classifyErr("op", errors.New("widget does not exist"))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("already exists pattern", func(t *testing.T) {
		err := classifyErr("op", errors.New("entry already exists"))
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("code = %v, want CodeAlreadyExists", connect.CodeOf(err))
		}
	})

	t.Run("permission denied pattern", func(t *testing.T) {
		err := classifyErr("op", errors.New("action forbidden"))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("code = %v, want CodePermissionDenied", connect.CodeOf(err))
		}
	})

	t.Run("invalid argument pattern", func(t *testing.T) {
		err := classifyErr("op", errors.New("invalid parameter value"))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want CodeInvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("default internal error", func(t *testing.T) {
		err := classifyErr("op", errors.New("something went wrong"))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("code = %v, want CodeInternal", connect.CodeOf(err))
		}
	})
}

func TestNotFoundOrInternal(t *testing.T) {
	t.Run("beads not found sentinel", func(t *testing.T) {
		err := notFoundOrInternal("show", beadspkg.ErrNotFound)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("mail not found sentinel", func(t *testing.T) {
		err := notFoundOrInternal("get", mail.ErrMessageNotFound)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("not found pattern", func(t *testing.T) {
		err := notFoundOrInternal("show", errors.New("bead not found"))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("code = %v, want CodeNotFound", connect.CodeOf(err))
		}
	})

	t.Run("default internal", func(t *testing.T) {
		err := notFoundOrInternal("show", errors.New("database locked"))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("code = %v, want CodeInternal", connect.CodeOf(err))
		}
	})
}
