package rline

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// tuiRline implements the IO interface on top of bubbletea — the default
// engine for interactive sessions (USQL_INPUT=readline opts back out). Each
// Next() runs a one-shot inline program, so the
// terminal is fully released between reads and usql's normal output
// machinery (tblfmt, pagers, \o, graphics protocols) needs no coordination.
type tuiRline struct {
	in     io.Reader
	raw    io.Writer // the terminal itself: bubbletea renders here
	out    io.Writer // guarded stdout (buffered while a read session runs)
	err    io.Writer // guarded stderr
	gout   *guardedWriter
	gerr   *guardedWriter
	int    bool
	hist   *tuiHistory
	comp   Completer
	outFn  func(string) string
	prom   string
	kickCh chan struct{}
	// respf filters unsolicited terminal reports from the input stream; it
	// is session-scoped, so a report split across two reads is still
	// recognized (a fresh filter per read would forget a held sequence and
	// pass the tail as typed text)
	respf *responseFilter
	// cons is the console for terminal queries (nil when unavailable)
	cons *os.File
	// pendLine/pendPos preset the next read's buffer and cursor (LineEditor)
	pendLine []rune
	pendPos  int
}

// newTUI creates the bubbletea-backed IO implementation. Starting a TUI
// session also puts the terminal into the session's input state, so late
// terminal-feature responses are neither echoed nor surfaced as typed text
// (see quiet.go).
func newTUI(in io.Reader, out, err io.Writer, histfile string, cons *os.File) *tuiRline {
	gout, gerr := &guardedWriter{w: out}, &guardedWriter{w: err}
	BeginConsoleQuiet()
	return &tuiRline{
		in:     in,
		raw:    out,
		out:    gout,
		err:    gerr,
		gout:   gout,
		gerr:   gerr,
		int:    true,
		hist:   loadTUIHistory(histfile),
		kickCh: make(chan struct{}, 1),
		respf:  newResponseFilter(in),
		cons:   cons,
	}
}

// Next reads one line interactively. While the read session runs, writes
// through Stdout/Stderr are buffered and flushed afterwards, so output from
// other goroutines (e.g. the completer's background query logging) cannot
// corrupt the inline rendering.
func (t *tuiRline) Next() ([]rune, error) {
	prompt := t.prom
	if prompt == "" {
		prompt = "> "
	}
	row, stray := t.probeRow()
	m := newLineModel(t, prompt, row)
	// keystrokes typed while output was still printing survive the probe
	// and open the line
	if len(stray) > 0 {
		if t.pendLine != nil {
			runes := bytesToRunes(stray)
			t.pendLine = append(runes, t.pendLine...)
			t.pendPos += len(runes)
		} else {
			m.ed.insertRunes(bytesToRunes(stray))
		}
	}
	if t.pendLine != nil {
		m.ed.buf, m.ed.idx = t.pendLine, t.pendPos
		t.pendLine, t.pendPos = nil, 0
	}
	t.gout.begin()
	t.gerr.begin()
	defer func() {
		t.gout.flush()
		t.gerr.flush()
	}()
	// ISIG off for the duration of the read: Ctrl-C arrives as a byte the
	// model turns into a graceful line clear. Between reads ISIG stays on,
	// so Ctrl-C during a running query raises SIGINT and cancels it.
	SuppressReadISIG()
	defer RestoreReadISIG()
	// hide the live (terminal) cursor: the model renders its own block.
	// The input goes through the session response filter, so late terminal
	// reports (DA1 / cursor-position / OSC answers) never reach the editor.
	prog := tea.NewProgram(m,
		tea.WithInput(t.respf),
		tea.WithOutput(t.raw),
		tea.WithoutSignalHandler(),
	)
	fm, err := prog.Run()
	t.respf.closeSession() // the read loop must not consume past this point
	if err != nil {
		// a killed program (terminal hangup) behaves like EOF
		return nil, io.EOF
	}
	res := fm.(*lineModel)
	if res.interrupted {
		return res.ed.buf, ErrInterrupt
	}
	// Ctrl-D on an empty line is EOF: exit the session. A bare enter
	// finalizes with done as well, but submits the empty line instead.
	if res.done && res.eofRequested {
		return nil, io.EOF
	}
	return res.ed.buf, nil
}

// Close ends the session and restores the terminal echo.
func (t *tuiRline) Close() error {
	EndConsoleQuiet()
	return nil
}

// ClearScreen clears the terminal (screen and scrollback), discards all
// pending input, and resets the input filter. The next read redraws the
// prompt at the top of the screen.
func (t *tuiRline) ClearScreen() {
	fmt.Fprint(t.raw, "\x1b[2J\x1b[3J\x1b[H")
	t.respf.reset()
}

// IsTUI reports the bubbletea engine (optional IO extension used by embedded
// modal views, e.g. the \conns manager).
func (t *tuiRline) IsTUI() bool { return true }

// ConsoleReader is the console input reader (optional IO extension for
// embedded bubbletea programs). It is the response filter, not the raw
// console: the pump owns console reads for the whole session, so an
// embedded program competes for buffered keystrokes with the same
// generation handoff every read session uses — never for raw console
// reads.
func (t *tuiRline) ConsoleReader() io.Reader { return t.respf }

// SetLine satisfies LineEditor: preset the next read's buffer and cursor.
func (t *tuiRline) SetLine(text []rune, pos int) {
	if pos < 0 || pos > len(text) {
		pos = len(text)
	}
	t.pendLine, t.pendPos = append([]rune(nil), text...), pos
}

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

// SwapCompleter satisfies CompleterSwapper: install c and return a func
// restoring the previous completer (re-wiring its kick). Swapping happens
// between reads, so no model ever observes the intermediate state.
func (t *tuiRline) SwapCompleter(c Completer) func() {
	prev := t.comp
	t.Completer(c)
	return func() { t.Completer(prev) }
}

// Save records a line in the history.
func (t *tuiRline) Save(s string) error { return t.hist.Save(s) }

// Password prompts for a password with echo disabled.
func (t *tuiRline) Password(prompt string) (string, error) {
	if !t.int {
		return "", errPasswordNotAvailable
	}
	return readPasswordFiltered(prompt, t.respf, t.raw, t.cons)
}

// SetOutput sets the output filter func. The TUI engine applies it to the
// input line as it is rendered (readline's Config.Output semantics: a
// display transform that does not change the logical content), debounced to
// at most one filter run per hlDebounce while typing — see tui_hl.go.
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

// readPasswordFiltered reads one masked line through the response filter:
// the pump keeps owning console reads, echo and ISIG are switched off on
// the terminal when it can be controlled, and every accepted rune is
// acknowledged with an asterisk. Without console control the same line is
// read unmasked.
func readPasswordFiltered(prompt string, f *responseFilter, out io.Writer, cons *os.File) (string, error) {
	mask := false
	if saved, ok := suppressInteractive(int(cons.Fd())); ok {
		mask = true
		toggleISIG(int(cons.Fd()), false) // ^C arrives as a byte instead of a signal
		defer restoreEcho(int(cons.Fd()), saved)
	}
	fmt.Fprint(out, prompt)
	return readPasswordLine(f, out, mask)
}

// readPasswordLine reads one line through the filter, honoring backspace
// edits and ^C (a byte with ISIG off) either way; masked reads echo an
// asterisk per accepted rune.
func readPasswordLine(f *responseFilter, out io.Writer, mask bool) (string, error) {
	var b []rune
	var pend []byte
	one := make([]byte, 1)
	for {
		if _, err := f.Read(one); err != nil {
			fmt.Fprintln(out)
			return "", err
		}
		c := one[0]
		if len(pend) == 0 {
			switch c {
			case '\r', '\n':
				fmt.Fprintln(out)
				return string(b), nil
			case 0x7f, '\b':
				if len(b) > 0 {
					b = b[:len(b)-1]
					if mask {
						fmt.Fprint(out, "\b \b")
					}
				}
				continue
			case 0x03: // ^C arrives as a byte with ISIG off
				fmt.Fprintln(out)
				return "", ErrInterrupt
			}
		}
		pend = append(pend, c)
		r, size := utf8.DecodeRune(pend)
		if r == utf8.RuneError && size <= 1 && len(pend) < 4 {
			continue // incomplete UTF-8: wait for more bytes
		}
		b = append(b, r)
		pend = pend[:0]
		if mask {
			fmt.Fprint(out, "*")
		}
	}
}

// inputMode selects the TUI engine (the default for interactive sessions);
// see selectEngine for the fallbacks.
func inputMode(interactive, forceNonInteractive, cygwin bool) bool {
	return selectEngine(interactive, forceNonInteractive, cygwin,
		os.Getenv("USQL_INPUT"), os.Getenv("TERM"))
}

// selectEngine is inputMode's decision core (env values passed in for
// testability): the TUI engine is the default for interactive sessions —
// USQL_INPUT=readline (or plain/off/0/false) opts back to the classic
// readline engine. The TUI engine needs raw mode and VT rendering, so a
// cygwin pty (pipe stdin) or dumb terminal falls back to readline.
func selectEngine(interactive, forceNonInteractive, cygwin bool, input, term string) bool {
	if !interactive || forceNonInteractive || cygwin {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(term), "dumb") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "readline", "plain", "classic", "0", "false", "off":
		return false
	}
	return true
}

// rowRE matches a DSR cursor position report.
var rowRE = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

// bytesToRunes decodes utf-8 input bytes, dropping non-printables: the
// probe's stray bytes are keystrokes typed during output, and control or
// escape bytes among them are not line content.
func bytesToRunes(b []byte) []rune {
	var rs []rune
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		if r < 0x20 && r != '\t' {
			i += size
			continue
		}
		rs = append(rs, r)
		i += size
	}
	return rs
}

// probeRow asks the terminal for its cursor row and returns the 0-based
// answer. The answer arrives through the response filter's row channel —
// the pump owns the console, so the probe never reads it directly; bytes
// typed while the probe waits stay buffered and open the line as normal
// keystrokes.
func (t *tuiRline) probeRow() (int, []byte) {
	if t.respf == nil {
		return -1, nil
	}
	t.respf.takeRow()
	fmt.Fprint(t.raw, "\x1b[6n")
	// real terminals answer within milliseconds; a conservative quarter
	// second covers slow links, and an unknown row only shrinks the
	// completion menu cap
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		row, ok := t.respf.waitRow(time.Until(deadline))
		if ok || !time.Now().Before(deadline) {
			return row, nil
		}
	}
}

// parseCursorRow extracts the 0-based row from a DSR 6n response (or a
// stream of them, taking the last): the final "\x1b[<row>;<col>R" report
// with row >= 1. (usql fork)
func parseCursorRow(resp []byte) (int, bool) {
	m := rowRE.FindAllSubmatch(resp, -1)
	if len(m) == 0 {
		return -1, false
	}
	row, err := strconv.Atoi(string(m[len(m)-1][1]))
	if err != nil || row < 1 {
		return -1, false
	}
	return row - 1, true
}

// guardedWriter buffers writes while a read session runs (begin/flush
// around each Next), so output from other goroutines lands after the
// rendered line instead of corrupting it. Outside sessions writes pass
// through unchanged.
type guardedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	active bool
	buf    []byte
}

// Write satisfies io.Writer.
func (g *guardedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active {
		return g.w.Write(p)
	}
	g.buf = append(g.buf, p...)
	return len(p), nil
}

// begin starts buffering.
func (g *guardedWriter) begin() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = true
}

// flush writes any buffered output and resumes pass-through.
func (g *guardedWriter) flush() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.buf) > 0 {
		_, _ = g.w.Write(g.buf)
		g.buf = nil
	}
	g.active = false
}
