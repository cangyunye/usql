package rline

import (
	"os"
	"strconv"
	"time"
)

// Completion UI knobs, shared by the readline and TUI engines.
//
//	USQL_INPUT            interactive input engine: bubbletea (default)
//	                      or readline (aliases: plain, classic, off)
//	USQL_COMPLETION_ROWS  maximum candidate rows shown at once (default 10);
//	                      further candidates stay reachable by scrolling
//	                      (arrows / PgUp / PgDn) instead of flooding the
//	                      screen with a large catalog
//	USQL_COMPLETION_DELAY typing-time completion debounce in milliseconds
//	                      (default 150): the candidate menu fires once the
//	                      user pauses typing, so fast typing and pastes do
//	                      not trigger a metadata query per rune
const (
	defaultCompletionRows  = 10
	minCompletionRows      = 3
	maxCompletionRows      = 100
	defaultCompletionDelay = 150 * time.Millisecond
	minCompletionDelay     = 30 * time.Millisecond
	maxCompletionDelay     = 2 * time.Second
)

// completionRows returns the configured candidate menu height.
func completionRows() int {
	if v := os.Getenv("USQL_COMPLETION_ROWS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			switch {
			case n < minCompletionRows:
				return minCompletionRows
			case n > maxCompletionRows:
				return maxCompletionRows
			default:
				return n
			}
		}
	}
	return defaultCompletionRows
}

// completionDelay returns the typing-time completion debounce.
func completionDelay() time.Duration {
	if v := os.Getenv("USQL_COMPLETION_DELAY"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			d := time.Duration(ms) * time.Millisecond
			switch {
			case d < minCompletionDelay:
				return minCompletionDelay
			case d > maxCompletionDelay:
				return maxCompletionDelay
			default:
				return d
			}
		}
	}
	return defaultCompletionDelay
}
