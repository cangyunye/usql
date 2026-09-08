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

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// tuiRline implements the IO interface on top of bubbletea, selected with
// USQL_INPUT=tui. Each Next() runs a one-shot inline program, so the
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
	// cons is the console for terminal queries (nil when unavailable)
	cons *os.File
	// pendLine/pendPos preset the next read's buffer and cursor (LineEditor)
	pendLine []rune
	pendPos  int
}

// newTUI creates the bubbletea-backed IO implementation.
func newTUI(in io.Reader, out, err io.Writer, histfile string, cons *os.File) *tuiRline {
	gout, gerr := &guardedWriter{w: out}, &guardedWriter{w: err}
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
	m := newLineModel(t, prompt, cursorRow(t.cons, t.raw))
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
	// hide the live (terminal) cursor: the model renders its own block
	prog := tea.NewProgram(m,
		tea.WithInput(t.in),
		tea.WithOutput(t.raw),
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

// IsTUI reports the bubbletea engine (optional IO extension used by embedded
// modal views, e.g. the \conns manager).
func (t *tuiRline) IsTUI() bool { return true }

// ConsoleReader is the console input reader (optional IO extension for
// embedded bubbletea programs).
func (t *tuiRline) ConsoleReader() io.Reader { return t.in }

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
		return "", ErrPasswordNotAvailable
	}
	return readPassword(prompt, t.in, t.raw)
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

// inputMode selects the TUI engine: USQL_INPUT=tui opts in for interactive
// sessions, except on terminals that cannot serve it (see selectEngine).
func inputMode(interactive, forceNonInteractive, cygwin bool) bool {
	return selectEngine(interactive, forceNonInteractive, cygwin,
		os.Getenv("USQL_INPUT"), os.Getenv("TERM"))
}

// selectEngine is inputMode's decision core (env values passed in for
// testability): the TUI engine needs raw mode and VT rendering, so a cygwin
// pty (pipe stdin) or dumb terminal falls back to readline even when
// explicitly requested.
func selectEngine(interactive, forceNonInteractive, cygwin bool, input, term string) bool {
	if !interactive || forceNonInteractive || cygwin {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "tui", "bubbletea", "1", "true", "on":
		if strings.EqualFold(strings.TrimSpace(term), "dumb") {
			return false
		}
		return true
	}
	return false
}

// rowRE matches a DSR cursor position report.
var rowRE = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

// cursorRow reports the 0-based terminal row of the cursor, or -1 when it
// cannot be determined: the console is queried (DSR 6n) in raw mode for the
// duration of the probe, giving the read its on-screen position for the
// candidate menu's above/below placement decision.
func cursorRow(cons *os.File, out io.Writer) int {
	if cons == nil {
		return -1
	}
	fd := int(cons.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return -1
	}
	defer term.Restore(fd, old)
	// the runtime's stdin file object does not support deadlines; a fresh
	// description of the same console does
	rd, err := os.OpenFile("/dev/stdin", os.O_RDONLY, 0)
	if err != nil {
		return -1
	}
	defer rd.Close()
	if err := rd.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		return -1
	}
	fmt.Fprint(out, "\x1b[6n")
	var resp []byte
	buf := make([]byte, 32)
	for !rowRE.Match(resp) {
		n, err := rd.Read(buf)
		resp = append(resp, buf[:n]...)
		if err != nil || len(resp) > 128 {
			break
		}
	}
	row, ok := parseCursorRow(resp)
	if !ok {
		return -1
	}
	return row
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
