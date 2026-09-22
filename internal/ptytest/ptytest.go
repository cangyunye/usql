//go:build !windows

// Package ptytest runs a command under a pseudo terminal that behaves like
// a real terminal emulator: capability queries (OSC 10/11 colors, DSR
// cursor position, DA1, XTVERSION) are answered, the cursor position is
// tracked for plausible DSR replies, and scripted keystrokes can be
// injected with human-like pacing. Assertions are made against the
// de-ANSI'd accumulated output.
//
// The point of this package is the class of bugs that only exists across
// interactive sessions — swallowed keystrokes, lost terminal state,
// paging/UI regressions — which unit tests against individual pieces
// cannot see.
//
// Two transports are attempted in order: a pty owned by this process
// (full fidelity, termios checks available), and — on darwin, where
// sandboxed test processes are often denied slave opens — a script(1)
// relay (forkpty via libc; no termios checks).
package ptytest

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Options configures New.
type Options struct {
	// Args is the command line to run (argv[0] is resolved with
	// exec.LookPath).
	Args []string
	// Env is the child's full environment. TERM=xterm-256color is appended
	// when absent.
	Env []string
	// Rows and Cols set the pty window size (default 30x100).
	Rows, Cols int
}

// Terminal is a running command under a pseudo terminal.
type Terminal struct {
	t *testing.T

	mode     string // "pty" or "script"
	master   *os.File
	slave    *os.File
	in       *os.File // keystrokes are written here
	out      *os.File // child output is read here
	outClose func() error
	cmd      *exec.Cmd

	mu     sync.Mutex
	buf    []byte // raw output bytes so far
	curRow int
	curCol int
	rows   int
	cols   int

	exited  bool
	exitErr error
}

var (
	oscRE  = regexp.MustCompile(`\x1b\](1[01]);\?(\x07|\x1b\\)`)
	dsrRE  = regexp.MustCompile(`\x1b\[6n`)
	da1RE  = regexp.MustCompile(`\x1b\[(?:>|=)?c`)
	xterRE = regexp.MustCompile(`\x1b\[>0?q`)
)

// New spawns opts.Args under a pty and starts reading its output. The
// terminal answers capability queries and tracks the cursor. The test is
// skipped when no pty can be obtained (sandboxed environments); everything
// else is fatal.
func New(t *testing.T, opts Options) *Terminal {
	t.Helper()
	rows, cols := opts.Rows, opts.Cols
	if rows == 0 {
		rows = 30
	}
	if cols == 0 {
		cols = 100
	}

	argv := append([]string{}, opts.Args...)
	if _, err := exec.LookPath(argv[0]); err != nil {
		t.Fatalf("ptytest: %v", err)
	}
	env := opts.Env
	if len(env) == 0 {
		env = os.Environ()
	}
	// always override: the test process itself may run with TERM=dumb,
	// which makes the child stall on a "terminal is not fully functional"
	// confirmation
	filtered := env[:0:0]
	for _, v := range env {
		if !strings.HasPrefix(v, "TERM=") {
			filtered = append(filtered, v)
		}
	}
	env = append(filtered, "TERM=xterm-256color")

	tm := &Terminal{
		t:      t,
		rows:   rows,
		cols:   cols,
		curRow: 1,
		curCol: 1,
	}

	master, slave, ptyErr := openPty(rows, cols)
	switch {
	case ptyErr == nil:
		tm.mode = "pty"
		tm.master, tm.slave = master, slave
		tm.in, tm.out = master, master
		tm.cmd = exec.Command(argv[0], argv[1:]...)
		tm.cmd.Stdin = slave
		tm.cmd.Stdout = slave
		tm.cmd.Stderr = slave
		tm.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		tm.cmd.Env = env
	case runtime.GOOS == "darwin":
		// script(1) allocates the pty via libc forkpty, which works where
		// raw ioctls from a sandboxed test process are denied
		tm.mode = "script"
		sh := fmt.Sprintf("stty rows %d cols %d 2>/dev/null; exec \"$@\"", rows, cols)
		scriptArgs := append([]string{"-q", "/dev/null", "/bin/sh", "-c", sh, "sh"}, argv...)
		tm.cmd = exec.Command("/usr/bin/script", scriptArgs...)
		tm.cmd.Env = env
		stdin, inW, err := os.Pipe()
		if err != nil {
			t.Fatalf("ptytest: pipe: %v", err)
		}
		outR, stdout, err := os.Pipe()
		if err != nil {
			t.Fatalf("ptytest: pipe: %v", err)
		}
		tm.cmd.Stdin = stdin
		tm.cmd.Stdout = stdout
		tm.cmd.Stderr = stdout
		tm.in, tm.out = inW, outR
		tm.outClose = stdout.Close
	default:
		t.Skipf("pty unavailable in this environment: %v", ptyErr)
	}

	if err := tm.cmd.Start(); err != nil {
		t.Fatalf("ptytest: start: %v", err)
	}
	if tm.mode == "script" {
		// the child holds the opposite ends; drop ours that were only used
		// for the exec hand-off
		if f := tm.cmd.Stdin.(*os.File); f != tm.in {
			_ = f.Close()
		}
		if f := tm.cmd.Stdout.(*os.File); f != tm.out {
			_ = f.Close()
		}
	}

	done := make(chan error, 1)
	go func() { done <- tm.cmd.Wait() }()
	tm.startReader(done)
	t.Cleanup(tm.KillIfRunning)
	return tm
}

// KillIfRunning stops the child if it is still running (test failure or
// skip paths otherwise leak it).
func (tm *Terminal) KillIfRunning() {
	tm.mu.Lock()
	exited := tm.exited
	tm.mu.Unlock()
	if exited {
		return
	}
	if tm.cmd.Process != nil {
		_ = tm.cmd.Process.Kill()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tm.mu.Lock()
		exited = tm.exited
		tm.mu.Unlock()
		if exited {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startReader captures output, tracks the cursor and answers capability
// queries until the output hits EOF.
func (tm *Terminal) startReader(done chan error) {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tm.out.Read(buf)
			if n > 0 {
				tm.mu.Lock()
				tm.buf = append(tm.buf, buf[:n]...)
				tm.mu.Unlock()
				tm.feed(buf[:n])
				tm.respondQueries(buf[:n])
			}
			if err != nil {
				break
			}
		}
		err := <-done
		tm.mu.Lock()
		tm.exited = true
		tm.exitErr = err
		tm.mu.Unlock()
	}()
}

// SendKeys types s with a small per-keystroke delay, like a human. Escape
// sequences present in s are written atomically so the child parses them
// as single keys.
func (tm *Terminal) SendKeys(s string) {
	tm.t.Helper()
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			end := i + 1
			if end < len(s) && s[end] == '[' {
				for end < len(s) && (s[end] < '@' || s[end] > '~') {
					end++
				}
			}
			if end < len(s) {
				end++
			}
			tm.write(s[i:end])
			i = end
			time.Sleep(15 * time.Millisecond)
			continue
		}
		_, size := decodeRune(s[i:])
		tm.write(s[i : i+size])
		i += size
		time.Sleep(15 * time.Millisecond)
	}
}

// SendRaw writes s to the pty in one write, no pacing.
func (tm *Terminal) SendRaw(s string) {
	tm.t.Helper()
	tm.write(s)
}

// SendLine types s followed by enter.
func (tm *Terminal) SendLine(s string) {
	tm.t.Helper()
	tm.SendKeys(s + "\r")
}

// Ctrl sends the control character for c (e.g. Ctrl('c') is ^C).
func (tm *Terminal) Ctrl(c byte) {
	tm.t.Helper()
	tm.write(string([]byte{c & 0x1f}))
	time.Sleep(15 * time.Millisecond)
}

func (tm *Terminal) write(s string) {
	if _, err := tm.in.WriteString(s); err != nil {
		tm.t.Fatalf("ptytest: write: %v", err)
	}
}

func decodeRune(s string) (rune, int) {
	for size := 1; size <= len(s) && size <= 4; size++ {
		r := []rune(s[:size])
		if len(r) == 1 && r[0] != 0xFFFD {
			return r[0], size
		}
		if size == 4 {
			return r[0], size
		}
	}
	return 0xFFFD, 1
}

// WaitFor blocks until the de-ANSI'd accumulated output matches re, and
// returns the full de-ANSI'd text at that point.
func (tm *Terminal) WaitFor(re string, d time.Duration) string {
	tm.t.Helper()
	rx, err := regexp.Compile(re)
	if err != nil {
		tm.t.Fatalf("ptytest: bad pattern %q: %v", re, err)
	}
	deadline := time.Now().Add(d)
	for {
		text := tm.Text()
		if rx.MatchString(text) {
			return text
		}
		tm.mu.Lock()
		exited := tm.exited
		tm.mu.Unlock()
		if exited {
			tm.t.Fatalf("ptytest: child exited while waiting for %q\ntext:\n%s", re, Tail(text, 1500))
		}
		if time.Now().After(deadline) {
			raw := string(tm.buf)
			tm.t.Fatalf("ptytest: timeout waiting for %q\ntext-tail:\n%s\nraw-tail:\n%q\ncounts: name-=%d rows150=%d error=%d",
				re, Tail(text, 600), Tail(raw, 600),
				strings.Count(raw, "name-"), strings.Count(raw, "(150 rows)"), strings.Count(raw, "error:"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Text returns the accumulated output with ANSI escape sequences stripped
// (control bytes other than \n and \t are dropped too).
func (tm *Terminal) Text() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return StripANSI(string(tm.buf))
}

// Raw returns the raw accumulated output bytes.
func (tm *Terminal) Raw() []byte {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return append([]byte(nil), tm.buf...)
}

// Count returns how many times sub appears in the de-ANSI'd output.
func (tm *Terminal) Count(sub string) int {
	return strings.Count(tm.Text(), sub)
}

// WaitExit waits for the child to exit and returns its error (nil on
// success).
func (tm *Terminal) WaitExit(d time.Duration) error {
	tm.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		tm.mu.Lock()
		exited, err := tm.exited, tm.exitErr
		tm.mu.Unlock()
		if exited {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("ptytest: timeout waiting for child exit")
}

// HasSlaveTermios reports whether SlaveTermiosFlags can read the pty's
// line discipline (direct pty mode only).
func (tm *Terminal) HasSlaveTermios() bool { return tm.mode == "pty" && tm.slave != nil }

// SlaveTermiosFlags reports the ECHO, ICANON and ISIG bits of the pty's
// line discipline.
func (tm *Terminal) SlaveTermiosFlags() (echo, icanon, isig bool, err error) {
	if !tm.HasSlaveTermios() {
		return false, false, false, fmt.Errorf("no slave termios in %s mode", tm.mode)
	}
	return termiosFlags(int(tm.slave.Fd()))
}

// Tail returns the last n bytes of s (for error reports).
func Tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// StripANSI removes ANSI escape sequences and control bytes from s,
// keeping \n and \t.
func StripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			if i+1 < len(s) && s[i+1] == ']' {
				// OSC string: skip through BEL or ST
				j := i + 2
				for j < len(s) && s[j] != 0x07 && !(j+1 < len(s) && s[j] == 0x1b && s[j+1] == '\\') {
					j++
				}
				if j < len(s) {
					if s[j] == 0x07 {
						j++
					} else {
						j += 2
					}
				}
				i = j
				continue
			}
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] < '@' || s[j] > '~') {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			i += 2
		case c == '\n' || c == '\t':
			b.WriteByte(c)
			i++
		case c >= ' ' && c != 0x7f:
			b.WriteByte(c)
			i++
		default:
			i++
		}
	}
	return b.String()
}

// feed updates the tracked cursor from child output bytes (enough state to
// answer DSR position queries plausibly).
func (tm *Terminal) feed(out []byte) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for i := 0; i < len(out); i++ {
		b := out[i]
		switch {
		case b == '\n':
			if tm.curRow < tm.rows {
				tm.curRow++
			}
		case b == '\r':
			tm.curCol = 1
		case b == '\b':
			if tm.curCol > 1 {
				tm.curCol--
			}
		case b == 0x1b && i+1 < len(out) && out[i+1] == '[':
			j := i + 2
			start := j
			for j < len(out) && (out[j] < '@' || out[j] > '~') {
				j++
			}
			if j < len(out) {
				final, params := out[j], string(out[start:j])
				n := 1
				if params != "" && !strings.Contains(params, ";") {
					if v, err := fmt.Sscanf(params, "%d", &n); err != nil || v == 0 {
						n = 1
					}
				}
				switch final {
				case 'A':
					tm.moveCursor(-n, 1)
				case 'B':
					tm.moveCursor(n, 1)
				case 'C':
					tm.curCol += n
				case 'D':
					tm.curCol -= n
					if tm.curCol < 1 {
						tm.curCol = 1
					}
				case 'H', 'f':
					parts := strings.SplitN(params, ";", 2)
					var rr, cc int
					if _, err := fmt.Sscanf(params, "%d;%d", &rr, &cc); err == nil {
						tm.curRow = clamp(rr, 1, tm.rows)
						tm.curCol = clamp(cc, 1, tm.cols)
					} else if _, err := fmt.Sscanf(parts[0], "%d", &rr); err == nil {
						tm.curRow = clamp(rr, 1, tm.rows)
					}
				}
			}
			i = j
		case b >= ' ':
			tm.curCol++
			if tm.curCol > tm.cols {
				tm.curCol = 1
				if tm.curRow < tm.rows {
					tm.curRow++
				}
			}
		}
	}
}

func (tm *Terminal) moveCursor(n, d int) {
	tm.curRow += n * d
	if tm.curRow < 1 {
		tm.curRow = 1
	}
	if tm.curRow > tm.rows {
		tm.curRow = tm.rows
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// respondQueries writes a real emulator's answers to the capability
// queries contained in out.
func (tm *Terminal) respondQueries(out []byte) {
	tm.mu.Lock()
	row, col := tm.curRow, tm.curCol
	tm.mu.Unlock()
	var reply []byte
	if m := oscRE.FindSubmatch(out); m != nil {
		rgb := "rgb:0000/0000/0000"
		if string(m[1]) == "10" {
			rgb = "rgb:ffff/ffff/ffff"
		}
		reply = append(reply, []byte(fmt.Sprintf("\x1b]%s;%s\x1b\\", m[1], rgb))...)
	}
	if dsrRE.Match(out) {
		reply = append(reply, []byte(fmt.Sprintf("\x1b[%d;%dR", row, col))...)
	}
	if da1RE.Match(out) {
		reply = append(reply, []byte("\x1b[?1;2c")...)
	}
	if xterRE.Match(out) {
		reply = append(reply, []byte("\x1bP>|xterm(370)\x1b\\")...)
	}
	if len(reply) > 0 {
		_, _ = tm.in.WriteString(string(reply))
	}
}

func openPty(rows, cols int) (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var slavePath string
	if runtime.GOOS == "darwin" {
		// darwin: TIOCPTYGNAME hands back the slave path; raw ioctl number
		// since the syscall constant only exists on darwin builds
		const tiocptygname = 0x40807453
		var buf [128]byte
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
			tiocptygname, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
			master.Close()
			return nil, nil, e
		}
		for i, c := range buf {
			if c == 0 {
				slavePath = string(buf[:i])
				break
			}
		}
		if slavePath == "" {
			master.Close()
			return nil, nil, fmt.Errorf("pty name query returned no path")
		}
	} else {
		const (
			tiocgptn   = 0x80045430
			tiocsptlck = 0x40045431
		)
		var n uint32
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
			master.Close()
			return nil, nil, e
		}
		var unlock int32
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
			master.Close()
			return nil, nil, e
		}
		slavePath = fmt.Sprintf("/dev/pts/%d", n)
	}
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	ws := struct{ row, col, x, y uint16 }{uint16(rows), uint16(cols), 0, 0}
	syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))
	return master, slave, nil
}
