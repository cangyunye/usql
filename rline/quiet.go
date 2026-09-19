package rline

import (
	"sync"
)

// Console echo management for the TUI engine.
//
// Terminal feature queries sent around process start (bubbletea's package
// init asks for the background color; shells and shell integrations ask for
// device attributes) are answered by the terminal at unpredictable times.
// When an answer arrives while the line discipline has ECHO set — i.e.
// whenever usql is not inside an interactive read — the response bytes are
// echoed onto the screen as visible garbage (e.g. `1;22;23;24;28;32`) and
// queued as input. Holding ECHO off for the interactive session silences
// the echo; queued answers are consumed by the session response filter
// (see tui_input.go).
//
// Echo is restored for the duration of external interactive commands (\!)
// and at session end.

var quietState struct {
	mu      sync.Mutex
	active  bool // a TUI session is in progress
	held    bool // echo is currently suppressed
	isigOff bool // ISIG is currently suppressed (inside a read)
	fd      int
	saved   *termiosState
}

// BeginConsoleQuiet marks the start of a TUI session and puts the terminal
// into the session's between-reads state: no echo, no canonical buffering,
// ISIG left on so Ctrl-C during a running query still raises SIGINT and
// cancels it. A no-op when stdin is not a terminal.
func BeginConsoleQuiet() {
	quietState.mu.Lock()
	defer quietState.mu.Unlock()
	if quietState.active {
		return
	}
	quietState.active = true
	quietState.fd = int(stdinFd())
	if restore, ok := suppressInteractive(quietState.fd); ok {
		quietState.saved = restore
		quietState.held = true
	}
}

// SuppressReadISIG turns ISIG off for the duration of an interactive read:
// Ctrl-C arrives as a byte the model handles (graceful line clear) instead
// of a signal that would terminate the process.
func SuppressReadISIG() {
	quietState.mu.Lock()
	defer quietState.mu.Unlock()
	if !quietState.active || quietState.isigOff {
		return
	}
	if toggleISIG(quietState.fd, false) {
		quietState.isigOff = true
	}
}

// RestoreReadISIG turns ISIG back on after an interactive read.
func RestoreReadISIG() {
	quietState.mu.Lock()
	defer quietState.mu.Unlock()
	if !quietState.active || !quietState.isigOff {
		return
	}
	if toggleISIG(quietState.fd, true) {
		quietState.isigOff = false
	}
}

// ConsoleShellOut runs fn with the terminal restored to its original state,
// so external interactive commands (\!) see a conventional terminal, and
// re-applies the session state afterwards while the session lasts.
func ConsoleShellOut(fn func()) {
	quietState.mu.Lock()
	resume := quietState.active && quietState.held
	if resume {
		restoreEcho(quietState.fd, quietState.saved)
		quietState.held = false
	}
	quietState.mu.Unlock()
	fn()
	if resume {
		quietState.mu.Lock()
		if restore, ok := suppressInteractive(quietState.fd); ok {
			quietState.saved = restore
			quietState.held = true
		}
		quietState.mu.Unlock()
	}
}

// EndConsoleQuiet restores the original terminal state and ends the
// session.
func EndConsoleQuiet() {
	quietState.mu.Lock()
	defer quietState.mu.Unlock()
	if !quietState.active {
		return
	}
	if quietState.held {
		restoreEcho(quietState.fd, quietState.saved)
	}
	quietState.active, quietState.held, quietState.saved = false, false, nil
}
