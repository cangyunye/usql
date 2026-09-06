// Package uitheme provides usql's lipgloss-based color theme for
// interactive output, and the single source of truth for console
// display-width measurement (CellWidth).
//
// The theme is disabled automatically when color cannot be shown (NO_COLOR,
// a color level below basic, or a non-terminal stderr); disabled styles
// render plain text, so callers can apply styles unconditionally.
package uitheme

import (
	"os"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"github.com/xo/terminfo"
)

// Theme holds the styled elements of usql's interactive output.
type Theme struct {
	// Error styles the "error:" label, ErrorMsg the message itself.
	Error, ErrorMsg lipgloss.Style
	// Accent styles emphasized text (names, versions, headings).
	Accent lipgloss.Style
	// Dim styles secondary information (timings, status lines).
	Dim lipgloss.Style
	// Success and Warn style affirmative and warning status text.
	Success, Warn lipgloss.Style
	// Header styles table header rows, Border colors table borders, Zebra
	// is the even-row background, and Null styles NULL cells.
	Header, Border, Zebra, Null lipgloss.Style
	// Selected styles the highlighted row of an interactive list.
	Selected lipgloss.Style
}

var (
	once sync.Once
	// theme is the process-wide theme, built on first use.
	theme *Theme
	// colorDisabled reports whether styling must render as plain text.
	colorDisabled bool
)

// Current returns the process theme. It is safe for concurrent use; the
// first call performs color capability detection.
func Current() *Theme {
	once.Do(detect)
	return theme
}

// detect performs the single color-capability check: NO_COLOR, the terminfo
// color level, and whether stderr is a terminal (errors are the one output
// that must never surprise a pipe or file).
func detect() {
	theme = newTheme()
	enabled := !noColorEnv() && stderrIsTerminal()
	if enabled {
		if level, _ := terminfo.ColorLevelFromEnv(); level < terminfo.ColorLevelBasic {
			enabled = false
		}
	}
	if !enabled {
		colorDisabled = true
		// every style below renders as plain text under the Ascii profile
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// noColorEnv mirrors env's NO_COLOR handling.
func noColorEnv() bool {
	s, ok := os.LookupEnv("NO_COLOR")
	return ok && s != "0" && s != "false" && s != "off"
}

// stderrIsTerminal reports whether stderr is an interactive terminal.
func stderrIsTerminal() bool {
	return isatty.IsTerminal(os.Stderr.Fd())
}

// Enabled reports whether color output is active.
func Enabled() bool {
	Current()
	return !colorDisabled
}

func newTheme() *Theme {
	return &Theme{
		Error:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		ErrorMsg: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		Accent:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		Dim:      lipgloss.NewStyle().Faint(true),
		Success:  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Warn:     lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Header:   lipgloss.NewStyle().Bold(true),
		Border:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Zebra:    lipgloss.NewStyle().Background(lipgloss.Color("235")),
		Null:     lipgloss.NewStyle().Faint(true),
		Selected: lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("236")),
	}
}
