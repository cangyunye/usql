package rline

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// responseFilter is an input-reader wrapper for the TUI engine that drops
// unsolicited terminal reports: answers to device queries (DA1 "ESC[?...c",
// cursor position "ESC[r;cR") and OSC/DCS replies. The queries are sent
// around process start (bubbletea's package init asks for the background
// color) or by the outer shell; their answers arrive at unpredictable times
// and would otherwise surface as typed text (e.g. "1;22;23;24;28;32").
//
// Everything else passes through untouched: plain keystrokes, and escape
// sequences that are not report finals — arrow keys ("ESC[A", "ESC[1;5A"),
// Home/End/PgUp ("ESC[H", "ESC[5~"), Alt-combos ("ESC x").
//
// The filter is also the session's SINGLE console reader: one pump
// goroutine owns the console read for the whole process and feeds the
// buffer below, while readers (one bubbletea program per Next) only consume
// filtered bytes. Each Next's program is torn down with its read loop still
// parked in a console read — bubbletea's fallback cancelreader cannot
// interrupt that park, so a second program reading the console directly
// would race the parked predecessor for the first keystroke and the loser
// would drop it (the swallowed first character after every query). With the
// pump owning the console, a superseded reader just wakes from the
// condition and exits without touching a byte. The console read deadline
// trick cannot be used instead, because on macOS a non-blocking flag on one
// pty file applies to the device and turns stdout writes into EAGAIN.
type responseFilter struct {
	rd io.Reader // the console: owned exclusively by the pump

	// pump state, guarded by mu; cond is broadcast whenever filtered bytes
	// or EOF arrive and when the active reader generation changes
	mu         sync.Mutex
	cond       *sync.Cond
	in         []byte // raw bytes read from rd, not yet filtered
	out        []byte // filtered bytes not yet delivered
	esc        int    // state: 0 ground, 1 after ESC, 2 CSI, 3 OSC/DCS string, 4 string ESC
	seq        []byte // sequence collected so far (for pass-through decisions)
	eof        bool   // console hit EOF: out will never grow again
	generation int    // bumped per Read: the last caller supersedes parked ones
	closedGen  int    // the latest generation whose read session has ended

	// rowCh carries the row of the last cursor-position report (DSR 6n
	// answer) seen in the stream: the prompt's cursor probe reads its row
	// from here instead of touching the console.
	rowCh chan int
	// pumpStarted gates the single pump goroutine
	pumpStarted sync.Once
	// dbg, when non-nil (USQL_INPUT_DEBUG=<file>), receives every raw
	// input chunk: timestamp, byte count and hex dump
	dbg *os.File
}

// errInputSuperseded is returned to a parked reader when a newer read
// session has taken over the stream. The bubbletea program that owned the
// parked reader is already gone by then, so nobody observes the error.
var errInputSuperseded = errors.New("input reader superseded")

// inputDebugPath names the raw-input log file (diagnostics for terminal
// noise: it shows exactly which bytes arrive, when, and in what chunks).
var inputDebugPath = os.Getenv("USQL_INPUT_DEBUG")

// newResponseFilter wraps rd.
func newResponseFilter(rd io.Reader) *responseFilter {
	f := &responseFilter{
		rd:    rd,
		rowCh: make(chan int, 1),
	}
	f.cond = sync.NewCond(&f.mu)
	if inputDebugPath != "" {
		if w, err := os.OpenFile(inputDebugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			f.dbg = w
		}
	}
	return f
}

// logRaw records one raw input chunk in the debug log.
func (f *responseFilter) logRaw(p []byte) {
	if f.dbg == nil {
		return
	}
	fmt.Fprintf(f.dbg, "%s n=%d % x\n", time.Now().Format("15:04:05.000"), len(p), p)
}

// startPump launches the console reader goroutine, once per session.
func (f *responseFilter) startPump() {
	f.pumpStarted.Do(func() {
		go f.pump()
	})
}

// pump is the single console reader. Filtered bytes wake parked readers via
// cond; the loop never exits until the console does.
func (f *responseFilter) pump() {
	buf := make([]byte, 1024)
	for {
		n, err := f.rd.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.logRaw(buf[:n])
			f.in = append(f.in, buf[:n]...)
			f.filter()
			f.cond.Broadcast()
			f.mu.Unlock()
		}
		if err != nil {
			f.mu.Lock()
			f.eof = true
			f.cond.Broadcast()
			f.mu.Unlock()
			return
		}
	}
}

// reset clears all held input state: queued bytes, a partially consumed
// sequence, and captured reports. Called when the user explicitly clears
// the screen, so stale startup residue can never resurface.
func (f *responseFilter) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.in = f.in[:0]
	f.out = f.out[:0]
	f.esc = 0
	f.seq = f.seq[:0]
	for {
		select {
		case <-f.rowCh:
			continue
		default:
		}
		break
	}
}

// takeRow returns the row of the last cursor-position report and clears it.
func (f *responseFilter) takeRow() (int, bool) {
	select {
	case row := <-f.rowCh:
		return row, true
	default:
		return -1, false
	}
}

// waitRow blocks for a cursor-position report up to d.
func (f *responseFilter) waitRow(d time.Duration) (int, bool) {
	select {
	case row := <-f.rowCh:
		return row, true
	case <-time.After(d):
		return -1, false
	}
}

// closeSession ends the current read session: its parked reader (the
// previous bubbletea program's read loop, which bubbletea cannot interrupt)
// wakes and exits WITHOUT consuming — bytes queued meanwhile stay buffered
// for the next session. Called when Next() has returned its line, before
// the query executes and before the next session opens.
func (f *responseFilter) closeSession() {
	f.mu.Lock()
	f.closedGen = f.generation
	f.cond.Broadcast()
	f.mu.Unlock()
}

// Read satisfies io.Reader. It never touches the console — the pump does —
// and blocks on cond until filtered bytes (or EOF) arrive. Entering bumps
// the generation: a parked predecessor (the previous read session's
// bubbletea program, which bubbletea cannot interrupt) wakes, sees it is
// stale, and exits without consuming a byte.
func (f *responseFilter) Read(p []byte) (int, error) {
	f.startPump()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.cond.Broadcast()
	gen := f.generation
	for {
		// a session that has ended, or been superseded by a newer one, must
		// never take bytes: its consumer (bubbletea's event loop) is gone
		// and would drop them
		if f.closedGen >= gen || f.generation != gen {
			return 0, errInputSuperseded
		}
		if len(f.out) > 0 {
			n := copy(p, f.out)
			f.out = f.out[n:]
			return n, nil
		}
		if f.eof {
			return 0, io.EOF
		}
		f.cond.Wait()
	}
}

// isReportTail reports whether the buffer is entirely a headless report
// tail: digit/semicolon/parameter groups with no leading ESC, ending in the
// c or R final. Keystrokes arrive per key and cannot match this shape.
func isReportTail(b []byte) bool {
	if len(b) == 0 || b[0] == 0x1b || b[0] == '[' {
		return false
	}
	semi := 0
	digits := 0
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == ';' || c == '?':
			semi++
		case (c == 'c' || c == 'R') && c == b[len(b)-1]:
		default:
			return false
		}
	}
	return semi >= 2 && digits >= 3
}

// filter consumes f.in into f.out. The pump owns the console and calls it
// with mu held; state persists across chunks, so a report split across two
// reads is still recognized.
func (f *responseFilter) filter() {
	// Startup residue: when the background-color / device-attribute
	// answers interleave badly at process start, termenv's init-time read
	// can stop mid-answer, leaving a headless tail (bare ";42;52c") in the
	// input. Such a chunk — digits and semicolons only, ending in c or R —
	// is unrecognizable by the sequence state machine, so drop it whole:
	// keystrokes arrive one key per chunk and can never take this shape.
	if isReportTail(f.in) {
		f.in = f.in[:0]
	}
	for len(f.in) > 0 {
		b := f.in[0]
		f.in = f.in[1:]
		switch f.esc {
		case 0: // ground
			if b == 0x1b {
				f.esc = 1
				f.seq = append(f.seq[:0], b)
				continue
			}
			f.out = append(f.out, b)
		case 1: // after ESC
			switch b {
			case '[':
				f.esc = 2
				f.seq = append(f.seq, b)
			case ']', 'P':
				// OSC or DCS string: drop through its terminator
				f.esc = 3
				f.seq = f.seq[:0]
			default:
				// two-byte escape (Alt-combo): pass through
				f.esc = 0
				f.out = append(f.out, f.seq...)
				f.out = append(f.out, b)
				f.seq = f.seq[:0]
			}
		case 2: // CSI body, up to the final byte
			if b == 0x1b {
				// a new sequence starts before this one ended (answers can
				// interleave at the reader): abort the partial silently
				f.esc = 1
				f.seq = f.seq[:1]
				continue
			}
			f.seq = append(f.seq, b)
			if b >= 0x40 && b <= 0x7e {
				params := f.seq[2 : len(f.seq)-1]
				final := b
				// report finals with parameters are terminal answers;
				// parameterless controls and every other final are keys
				report := false
				for _, pc := range params {
					if pc >= '0' && pc <= '9' || pc == '?' {
						report = true
						break
					}
				}
				if (final == 'c' || final == 'R') && report {
					// drop the answer; capture cursor-position answers —
					// the prompt probe reads its row from here
					if final == 'R' {
						if row, ok := parseCursorRow(f.seq); ok {
							select {
							case f.rowCh <- row:
							default:
							}
						}
					}
				} else {
					f.out = append(f.out, f.seq...)
				}
				f.esc = 0
				f.seq = f.seq[:0]
			}
		case 3: // OSC/DCS string body
			switch b {
			case 0x07:
				// BEL terminator: dropped with the string
				f.esc = 0
				f.seq = f.seq[:0]
			case 0x1b:
				f.esc = 4
			}
		case 4: // ESC inside a string: ST (ESC \) closes it
			if b == '\\' {
				f.esc = 0
				f.seq = f.seq[:0]
			} else {
				// not ST: give up and pass what was collected
				f.esc = 0
				f.out = append(f.out, f.seq...)
				f.out = append(f.out, 0x1b, b)
				f.seq = f.seq[:0]
			}
		}
	}
	// a lone ESC with nothing following is the Escape key, not a sequence:
	// deliver it instead of stalling until the next keystroke
	if f.esc == 1 && len(f.in) == 0 {
		f.esc = 0
		f.out = append(f.out, f.seq...)
		f.seq = f.seq[:0]
	}
}
