package rline

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// tuiRline implements the IO interface on top of bubbletea, selected with
// USQL_INPUT=tui. Each Next() runs a one-shot inline program, so the
// terminal is fully released between reads and usql's normal output
// machinery (tblfmt, pagers, \o, graphics protocols) needs no coordination.
type tuiRline struct {
	in     io.Reader
	out    io.Writer
	err    io.Writer
	int    bool
	hist   *tuiHistory
	comp   Completer
	outFn  func(string) string
	prom   string
	kickCh chan struct{}
}

// newTUI creates the bubbletea-backed IO implementation.
func newTUI(in io.Reader, out, err io.Writer, histfile string) *tuiRline {
	return &tuiRline{
		in:     in,
		out:    out,
		err:    err,
		int:    true,
		hist:   loadTUIHistory(histfile),
		kickCh: make(chan struct{}, 1),
	}
}

// Next reads one line interactively.
func (t *tuiRline) Next() ([]rune, error) {
	prompt := t.prom
	if prompt == "" {
		prompt = "> "
	}
	m := newLineModel(t, prompt)
	// hide the live (terminal) cursor: the model renders its own block
	prog := tea.NewProgram(m,
		tea.WithInput(t.in),
		tea.WithOutput(t.out),
		tea.WithoutSignalHandler(),
	)
	fm, err := prog.Run()
	if err != nil {
		// a killed program (terminal hangup) behaves like EOF
		return nil, io.EOF
	}
	res := fm.(*lineModel)
	if res.interrupted {
		return res.ed.buf, ErrInterrupt
	}
	return res.ed.buf, nil
}

// Close is a no-op: every read owns its terminal session.
func (t *tuiRline) Close() error { return nil }

// Stdout is the standard out.
func (t *tuiRline) Stdout() io.Writer { return t.out }

// Stderr is the standard error out.
func (t *tuiRline) Stderr() io.Writer { return t.err }

// Interactive reports interactive mode.
func (t *tuiRline) Interactive() bool { return t.int }

// Cygwin reports cygwin mode; the TUI engine does not use the cygwin path.
func (t *tuiRline) Cygwin() bool { return false }

// Prompt sets the prompt for the next interactive line read.
func (t *tuiRline) Prompt(s string) { t.prom = s }

// Completer sets the auto-completer and wires the asynchronous re-render
// kick.
func (t *tuiRline) Completer(c Completer) {
	t.comp = c
	if lc, ok := c.(LiveCompleter); ok {
		lc.SetLiveKick(func() {
			select {
			case t.kickCh <- struct{}{}:
			default:
			}
		})
	}
}

// Save records a line in the history.
func (t *tuiRline) Save(s string) error { return t.hist.Save(s) }

// Password prompts for a password with echo disabled.
func (t *tuiRline) Password(prompt string) (string, error) {
	if !t.int {
		return "", ErrPasswordNotAvailable
	}
	return readPassword(prompt, t.in, t.out)
}

// SetOutput sets the output filter func. The TUI engine applies it to the
// completed line when echoing it for the history-visible output; per-
// keystroke re-highlighting is handled by the model's View directly.
func (t *tuiRline) SetOutput(f func(string) string) { t.outFn = f }

// readPassword reads a masked line from the terminal. Echo is disabled via
// x/term when the input is the console; the fallback is a plain read.
func readPassword(prompt string, in io.Reader, out io.Writer) (string, error) {
	if f, ok := in.(*os.File); ok {
		fmt.Fprint(out, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	fmt.Fprint(out, prompt)
	rd := bufio.NewReader(in)
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}

// inputMode selects the TUI engine: USQL_INPUT=tui opts in for interactive
// sessions.
func inputMode(interactive, forceNonInteractive bool) bool {
	if !interactive || forceNonInteractive {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("USQL_INPUT"))) {
	case "tui", "bubbletea", "1", "true", "on":
		return true
	}
	return false
}
