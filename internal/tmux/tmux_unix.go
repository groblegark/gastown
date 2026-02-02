//go:build unix

package tmux

import (
	"syscall"
	"time"
)

// killProcessGroup sends SIGTERM then SIGKILL to a process group.
// On Unix, syscall.Kill with a negative PID targets the entire process group.
func killProcessGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
