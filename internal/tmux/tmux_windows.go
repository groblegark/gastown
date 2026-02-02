//go:build windows

package tmux

// killProcessGroup is a no-op on Windows.
// tmux is not available on Windows, so this code path won't be reached.
// This stub exists to allow cross-platform compilation.
func killProcessGroup(pgid int) {
	// No-op on Windows - tmux is not supported
}
