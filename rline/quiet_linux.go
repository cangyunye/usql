//go:build linux

package rline

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinFd returns the fd of the interactive input.
func stdinFd() int {
	return int(os.Stdin.Fd())
}

// termiosState is the saved terminal state.
type termiosState = unix.Termios

// suppressInteractive puts the terminal into the TUI session's between-reads
// state: ECHO off (terminal answers arrive silently) and ICANON off
// (keystrokes delivered immediately — the input wrapper is not an *os.File,
// so bubbletea does not manage the termios itself). ISIG stays on, so
// Ctrl-C during a running query raises SIGINT and cancels it. Output
// processing stays on.
func suppressInteractive(fd int) (*termiosState, bool) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, false
	}
	saved := *t
	t.Lflag &^= unix.ECHO | unix.ICANON
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		return nil, false
	}
	return &saved, true
}

// toggleISIG sets or clears the ISIG flag.
func toggleISIG(fd int, on bool) bool {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return false
	}
	if on {
		t.Lflag |= unix.ISIG
	} else {
		t.Lflag &^= unix.ISIG
	}
	return unix.IoctlSetTermios(fd, unix.TCSETS, t) == nil
}

// restoreEcho reinstates a saved terminal state.
func restoreEcho(fd int, s *termiosState) {
	if s == nil {
		return
	}
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, s)
}
