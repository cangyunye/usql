//go:build windows || plan9 || js || wasip1 || aix || solaris || illumos

package rline

// Console state management is a no-op on platforms without a POSIX termios
// line discipline (Windows consoles manage echo through their own console
// API, which the TUI engine's raw-mode handling already covers).

func stdinFd() int { return -1 }

type termiosState struct{}

func suppressInteractive(int) (*termiosState, bool) { return nil, false }

func restoreEcho(int, *termiosState) {}

func toggleISIG(int, bool) bool { return false }
