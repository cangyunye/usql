//go:build ignore

// ptysmoke drives usql (or any command) under a real PTY, answering the
// terminal capability queries terminals normally respond to (OSC 10/11
// color queries, DSR cursor position, DA1), injecting a scripted key
// sequence, and capturing the raw output. bubbletea/termenv swallow keys or
// hang on dumb PTYs without these responses.
//
// Usage:
//
//	go run ptysmoke.go [-timeout 30s] [-out FILE] [-rows 30] [-cols 100] \
//	    -keys 'select 1;\r:500,quit\r' -- ./usql sqlite:///tmp/t.db
//
// The -keys script is a comma-separated list of steps:
//
//	text        typed with a small per-key delay (UTF-8)
//	:N          pause N milliseconds
//	\r \t \e    enter, tab, escape
//	\x7f        backspace
//	\x01..\x1a  control characters (e.g. \x12 = Ctrl-R, \x13 = Ctrl-S)
//	\x1b[A..D   arrows; \x1b[1;5C ctrl-right, etc. (raw escape sequences)
//	\u4f60      a unicode rune (e.g. for GBK locale smoke tests)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

type winsize struct {
	row, col, x, y uint16
}

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// openpty returns a new pseudoterminal's master and slave, sized rows×cols.
func openpty(rows, cols int) (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var n uint32
	if err := ioctl(int(master.Fd()), syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		master.Close()
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(int(master.Fd()), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	ws := winsize{row: uint16(rows), col: uint16(cols)}
	_ = ioctl(int(master.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
	return master, slave, nil
}

var (
	oscRE  = regexp.MustCompile(`\x1b\](1[01]);\?(\x07|\x1b\\)`)
	dsrRE  = regexp.MustCompile(`\x1b\[6n`)
	da1RE  = regexp.MustCompile(`\x1b\[(?:>|=)?c`)
	xterRE = regexp.MustCompile(`\x1b\[>0?q`)
)

// term is a minimal terminal emulator: enough cursor state to answer DSR
// position queries plausibly (bubbletea's inline renderer uses relative
// moves, which is what the tracking covers).
type term struct {
	rows, cols int
	row, col   int
}

func (t *term) move(n, d int) {
	t.row += n * d
	if t.row < 1 {
		t.row = 1
	}
	if t.row > t.rows {
		t.row = t.rows
	}
}

// feed updates the cursor state from child output bytes.
func (t *term) feed(out []byte) {
	for i := 0; i < len(out); i++ {
		b := out[i]
		switch {
		case b == '\n':
			if t.row < t.rows {
				t.row++
			}
		case b == '\r':
			t.col = 1
		case b == '\b':
			if t.col > 1 {
				t.col--
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
					if v, err := strconv.Atoi(params); err == nil {
						n = v
					}
				}
				switch final {
				case 'A':
					t.move(-n, 1)
				case 'B':
					t.move(n, 1)
				case 'C':
					t.col += n
				case 'D':
					t.col -= n
					if t.col < 1 {
						t.col = 1
					}
				case 'H', 'f':
					parts := strings.SplitN(params, ";", 2)
					if r, err := strconv.Atoi(parts[0]); err == nil && r > 0 {
						t.row = min(r, t.rows)
					}
					if len(parts) == 2 {
						if c, err := strconv.Atoi(parts[1]); err == nil && c > 0 {
							t.col = c
						}
					}
				}
			}
			i = j
		case b >= ' ':
			t.col++
			if t.col > t.cols {
				t.col = 1
				if t.row < t.rows {
					t.row++
				}
			}
		}
	}
}

// respond answers terminal capability queries with a plausible xterm-256
// profile: dark background, basic DA1, and the tracked cursor position.
func (t *term) respond(master *os.File, out []byte) {
	m := oscRE.FindSubmatch(out)
	if m != nil {
		// OSC 10 is the foreground color, 11 the background
		var rgb string
		if string(m[1]) == "10" {
			rgb = "rgb:ffff/ffff/ffff"
		} else {
			rgb = "rgb:0000/0000/0000"
		}
		fmt.Fprintf(master, "\x1b]%s;%s\x1b\\", m[1], rgb)
	}
	if dsrRE.Match(out) {
		fmt.Fprintf(master, "\x1b[%d;%dR", t.row, t.col)
	}
	if da1RE.Match(out) {
		fmt.Fprint(master, "\x1b[?1;2c")
	}
	if xterRE.Match(out) {
		fmt.Fprint(master, "\x1bP>|xterm(370)\x1b\\")
	}
}

// unescape resolves \r, \t, \e and \xHH escapes in a key step.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'e':
			b.WriteByte(0x1b)
			i++
		case 'x':
			if i+3 < len(s) {
				if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 3
					continue
				}
			}
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// keyScript expands a -keys script into (data, pause) steps.
func keyScript(s string) []func(*os.File) {
	var steps []func(*os.File)
	for _, item := range strings.Split(s, ",") {
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, ":") {
			var ms int
			fmt.Sscanf(item, ":%d", &ms)
			d := time.Duration(ms) * time.Millisecond
			steps = append(steps, func(*os.File) { time.Sleep(d) })
			continue
		}
		steps = append(steps, keys(unescape(item)))
	}
	return steps
}

// keys returns a step typing s rune by rune; escape sequences are written
// atomically so the terminal parses them as single keys.
func keys(s string) func(*os.File) {
	return func(master *os.File) {
		for j := 0; j < len(s); {
			if s[j] == '\x1b' {
				end := j + 1
				if end < len(s) && s[end] == '[' {
					end++
					for end < len(s) && (s[end] < '@' || s[end] > '~') {
						end++
					}
					if end < len(s) {
						end++
					}
				}
				master.WriteString(s[j:end])
				j = end
				time.Sleep(20 * time.Millisecond)
				continue
			}
			r, size := utf8.DecodeRuneInString(s[j:])
			master.WriteString(string(r))
			j += size
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func main() {
	timeout := flag.Duration("timeout", 30*time.Second, "overall timeout")
	out := flag.String("out", "", "write raw output to FILE (else stdout)")
	rows := flag.Int("rows", 30, "pty rows")
	cols := flag.Int("cols", 100, "pty cols")
	keysArg := flag.String("keys", "", "key script (see package comment)")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("usage: ptysmoke [-keys SCRIPT] -- COMMAND [ARGS...]")
	}

	master, slave, err := openpty(*rows, *cols)
	if err != nil {
		log.Fatal(err)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	m := master

	var w *os.File = os.Stdout
	if *out != "" {
		if w, err = os.Create(*out); err != nil {
			log.Fatal(err)
		}
		defer w.Close()
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// reader: capture output, track the cursor, answer capability queries
	go func() {
		buf := make([]byte, 4096)
		tm := &term{rows: *rows, cols: *cols, row: 1, col: 1}
		for {
			n, err := m.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				tm.feed(buf[:n])
				tm.respond(m, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// inject keys, then wait for the child (or timeout)
	time.Sleep(500 * time.Millisecond)
	for _, step := range keyScript(*keysArg) {
		step(m)
	}
	select {
	case err := <-done:
		if err != nil {
			log.Printf("child exited: %v", err)
		}
	case <-time.After(*timeout):
		log.Printf("timeout after %v; killing", *timeout)
		cmd.Process.Signal(syscall.SIGKILL)
	}
	time.Sleep(200 * time.Millisecond)
}
