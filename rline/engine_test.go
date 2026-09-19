package rline

import "testing"

func TestSelectEngine(t *testing.T) {
	tests := []struct {
		name                string
		interactive         bool
		forceNonInteractive bool
		cygwin              bool
		input               string
		term                string
		want                bool
	}{
		{name: "default is tui", interactive: true, want: true},
		{name: "tui opts in", interactive: true, input: "tui", want: true},
		{name: "tui aliases opt in", interactive: true, input: "BUBBLETEA", want: true},
		{name: "tui numeric opt in", interactive: true, input: "1", want: true},
		{name: "non-interactive never tui", interactive: false, want: false},
		{name: "forced non-interactive never tui", interactive: true, forceNonInteractive: true, want: false},
		{name: "explicit readline opts out", interactive: true, input: "readline", want: false},
		{name: "plain opts out", interactive: true, input: "plain", want: false},
		{name: "off opts out", interactive: true, input: "off", want: false},
		// unknown values keep the TUI default
		{name: "unknown value stays tui", interactive: true, input: "ed", want: true},
		// the safety net: terminals that cannot serve the TUI engine fall
		// back to readline
		{name: "cygwin falls back", interactive: true, cygwin: true, want: false},
		{name: "dumb terminal falls back", interactive: true, term: "dumb", want: false},
		{name: "dumb terminal is case-insensitive", interactive: true, term: "DUMB", want: false},
		{name: "unset TERM is fine (windows)", interactive: true, term: "", want: true},
		{name: "normal TERM is fine", interactive: true, term: "xterm-256color", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectEngine(tc.interactive, tc.forceNonInteractive, tc.cygwin, tc.input, tc.term)
			if got != tc.want {
				t.Fatalf("selectEngine(%v,%v,%v,input=%q,term=%q) = %v, want %v",
					tc.interactive, tc.forceNonInteractive, tc.cygwin, tc.input, tc.term, got, tc.want)
			}
		})
	}
}

func TestParseCursorRow(t *testing.T) {
	tests := []struct {
		name string
		resp string
		want int
		ok   bool
	}{
		{name: "plain report", resp: "\x1b[5;1R", want: 4, ok: true},
		{name: "report with leading noise", resp: "garbage\x1b[12;40R", want: 11, ok: true},
		{name: "multiple reports take the last", resp: "\x1b[1;1R\x1b[9;3R", want: 8, ok: true},
		{name: "no report", resp: "nothing here", want: -1, ok: false},
		{name: "empty", resp: "", want: -1, ok: false},
		{name: "row zero is invalid", resp: "\x1b[0;1R", want: -1, ok: false},
		{name: "non-numeric row", resp: "\x1b[ab;1R", want: -1, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCursorRow([]byte(tc.resp))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseCursorRow(%q) = (%d, %v), want (%d, %v)",
					tc.resp, got, ok, tc.want, tc.ok)
			}
		})
	}
}
