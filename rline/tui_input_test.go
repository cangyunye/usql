package rline

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, f io.Reader) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := f.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String()
		}
	}
}

func TestResponseFilterDropsReports(t *testing.T) {
	in := "\x1b[?62;1;22;23;24;28;32;42;52c" + // DA1 answer
		"a" +
		"\x1b[12;40R" + // cursor position answer
		"b" +
		"\x1b]11;rgb:1c1c/1c1c/1c1c\x1b\\" + // OSC background answer
		"c" +
		"\x1b]0;title\x07" + // OSC BEL form
		"d"
	f := newResponseFilter(strings.NewReader(in))
	if got := readAll(t, f); got != "abcd" {
		t.Fatalf("filter = %q, want %q", got, "abcd")
	}
}

func TestResponseFilterPassesKeys(t *testing.T) {
	in := "\x1b[A" + // up
		"\x1b[1;5C" + // ctrl+right
		"\x1b[H" + // home
		"\x1b[5~" + // pgup
		"\x1bx" + // alt+x
		"se" +
		"\x1b[Z" // shift-tab
	f := newResponseFilter(strings.NewReader(in))
	if got := readAll(t, f); got != in {
		t.Fatalf("filter = %q, want passthrough %q", got, in)
	}
}

func TestResponseFilterLoneEscape(t *testing.T) {
	// the Escape key alone must not stall until more input arrives
	f := newResponseFilter(strings.NewReader("\x1b"))
	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err != nil || n != 1 || buf[0] != 0x1b {
		t.Fatalf("lone ESC read = %d bytes (%q), err=%v", n, buf[:n], err)
	}
}

func TestResponseFilterInterleavedReports(t *testing.T) {
	// a second ESC aborts a partial sequence (robustness against malformed
	// interleaving); the bytes of an aborted half-answer are passed — real
	// terminals emit answers atomically, so this shape is synthetic
	in := "x" +
		"\x1b[?62;1;22;23;24;28;32" +
		"\x1b[12;1R" +
		"y"
	f := newResponseFilter(strings.NewReader(in))
	if got := readAll(t, f); got != "xy" {
		t.Fatalf("filter = %q, want %q", got, "xy")
	}
}

func TestResponseFilterRowCapture(t *testing.T) {
	f := newResponseFilter(strings.NewReader("\x1b[12;40R"))
	buf := make([]byte, 64)
	f.Read(buf)
	if row, ok := f.takeRow(); !ok || row != 11 {
		t.Fatalf("takeRow = %d %v, want 11 true", row, ok)
	}
	if _, ok := f.takeRow(); ok {
		t.Fatal("takeRow must clear the row")
	}
	// ctrl+right is a key: no row is registered
	f2 := newResponseFilter(strings.NewReader("\x1b[1;5C"))
	f2.Read(buf)
	if _, ok := f2.takeRow(); ok {
		t.Fatal("a key sequence must not register as a row")
	}
}

func TestResponseFilterRealWorldLog(t *testing.T) {
	// the exact byte sequence captured from a leaking session (see
	// USQL_INPUT_DEBUG): the DA1 answer \x1b[?62;1;22;23;24;28;32;42;52c
	// followed by the cursor-report answer \x1b[73;1R, then Ctrl-C and
	// typing — nothing but the typing may reach the line
	in := "\x1b[?62;1;22;23;24;28;32;42;52c" +
		"\x1b[73;1R" +
		"\x03" +
		"\x1b[73;1R" +
		"\\q"
	f := newResponseFilter(strings.NewReader(in))
	if got := readAll(t, f); got != "\x03\\q" {
		t.Fatalf("filter = %q, want %q", got, "\x03\\q")
	}
	if row, ok := f.takeRow(); !ok || row != 72 {
		t.Fatalf("row = %d %v, want 72 true", row, ok)
	}
}

func TestResponseFilterStartupTailDrop(t *testing.T) {
	// the exact residue from the field log: a DA1 answer tail whose head
	// termenv consumed at init, arriving as its own input chunk
	f := newResponseFilter(strings.NewReader(";42;52c"))
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err == nil && n != 0 {
		t.Fatalf("tail leaked: %q", buf[:n])
	}
	// the tail chunk followed by a cursor report and typing
	f1 := newResponseFilter(&splitReader{parts: []string{";42;52c", "\x1b[73;1R", "select 1;"}})
	buf1 := make([]byte, 64)
	n1, _ := f1.Read(buf1)
	if got := string(buf1[:n1]); got != "select 1;" {
		t.Fatalf("tail+report leaked into: %q", got)
	}
	// real typing is never dropped: digits without a report terminator
	f2 := newResponseFilter(strings.NewReader("1;22"))
	buf2 := make([]byte, 64)
	n2, _ := f2.Read(buf2)
	if got := string(buf2[:n2]); got != "1;22" {
		t.Fatalf("typing = %q, want passthrough 1;22", got)
	}
	// and a full DA1 answer with its head intact is dropped whole
	f3 := newResponseFilter(strings.NewReader("\x1b[?62;1;22;23;24;28;32;42;52cselect"))
	buf3 := make([]byte, 64)
	n3, _ := f3.Read(buf3)
	if got := string(buf3[:n3]); got != "select" {
		t.Fatalf("full answer leaked / typing lost: %q", got)
	}
}

func TestResponseFilterSplitSequence(t *testing.T) {
	// a report split across reads is dropped whole
	f := newResponseFilter(&splitReader{parts: []string{"\x1b[?6", "2;1;c" + "ok"}})
	if got := readAll(t, f); got != "ok" {
		t.Fatalf("filter = %q, want %q", got, "ok")
	}
}

// splitReader yields its parts one Read call at a time.
type splitReader struct {
	parts []string
	i     int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if r.i >= len(r.parts) {
		return 0, io.EOF
	}
	s := r.parts[r.i]
	r.i++
	return copy(p, s), nil
}

// chanReader is a console stand-in: chunks written to the channel are
// delivered one Read at a time, with the channel providing the pacing the
// pump normally gets from a real console.
type chanReader struct {
	c chan []byte
}

func newChanReader() *chanReader { return &chanReader{c: make(chan []byte, 16)} }

func (r *chanReader) Read(p []byte) (int, error) {
	data, ok := <-r.c
	if !ok {
		return 0, io.EOF
	}
	return copy(p, data), nil
}

func (r *chanReader) feed(s string)      { r.c <- []byte(s) }
func (r *chanReader) feedBytes(b []byte) { r.c <- b }

// TestResponseFilterGenerationHandoff is the regression test for the
// swallowed-first-character bug: a parked reader of an ended session must
// never consume the keystrokes that belong to the next session.
func TestResponseFilterGenerationHandoff(t *testing.T) {
	cr := newChanReader()
	f := newResponseFilter(cr)
	f.startPump()

	type res struct {
		n   int
		s   string
		err error
	}
	res1 := make(chan res, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := f.Read(buf)
		res1 <- res{n, string(buf[:n]), err}
	}()
	time.Sleep(50 * time.Millisecond) // let session 1 park

	done2 := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := f.Read(buf)
		if err != nil {
			done2 <- "\x00err:" + err.Error()
			return
		}
		done2 <- string(buf[:n])
	}()
	time.Sleep(50 * time.Millisecond) // let session 2 park too

	cr.feed("abc") // the keystrokes: they belong to session 2

	select {
	case r := <-res1:
		if r.err == nil || !strings.Contains(r.err.Error(), "superseded") {
			t.Fatalf("session 1: want superseded error, got n=%d s=%q err=%v", r.n, r.s, r.err)
		}
		if r.s != "" {
			t.Fatalf("session 1 consumed bytes: %q", r.s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session 1 reader never woke")
	}

	select {
	case got := <-done2:
		if got != "abc" {
			t.Fatalf("session 2: got %q, want %q", got, "abc")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session 2 reader never got the keystrokes")
	}
}

// TestResponseFilterCloseSessionKeepsBytes: bytes typed while a query runs
// (after closeSession ended the read session) must still be delivered to
// the next session.
func TestResponseFilterCloseSessionKeepsBytes(t *testing.T) {
	cr := newChanReader()
	f := newResponseFilter(cr)
	f.startPump()

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := f.Read(buf)
		if err != nil {
			done <- "\x00err:" + err.Error()
			return
		}
		done <- string(buf[:n])
	}()
	time.Sleep(50 * time.Millisecond)

	f.closeSession() // the line was submitted; the query runs
	cr.feed("SELECT 1;")

	select {
	case got := <-done:
		// the pre-close parked reader must NOT have consumed the bytes
		if strings.Contains(got, "SELECT") {
			t.Fatalf("ended session consumed queued bytes: %q", got)
		}
		if !strings.HasPrefix(got, "\x00err:") {
			t.Fatalf("expected a superseded error, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader never woke")
	}

	if got := drainReadString(f); got != "SELECT 1;" {
		t.Fatalf("next session: got %q, want %q", got, "SELECT 1;")
	}
}

func drainReadString(f *responseFilter) string {
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil {
		return "\x00err:" + err.Error()
	}
	return string(buf[:n])
}

// TestResponseFilterCJKSplit: a multibyte rune split across console reads
// passes through whole.
func TestResponseFilterCJKSplit(t *testing.T) {
	cr := newChanReader()
	f := newResponseFilter(cr)
	f.startPump()
	ni := []byte("你") // E4 BD A0
	go func() {
		cr.feedBytes(ni[:1])
		time.Sleep(20 * time.Millisecond)
		cr.feedBytes(ni[1:])
	}()

	buf := make([]byte, 64)
	var got strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for got.Len() < 3 && time.Now().Before(deadline) {
		n, err := f.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got.Write(buf[:n])
	}
	if got.String() != "你" {
		t.Fatalf("got %q, want %q", got.String(), "你")
	}
}

// TestReadPasswordFilteredFallback: without a controllable console the
// masked reader degrades to unmasked, and byte handling is exact
// (backspace, enter).
func TestReadPasswordFilteredFallback(t *testing.T) {
	cr := newChanReader()
	f := newResponseFilter(cr)
	f.startPump()
	cons, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(cons.Name())
	cons.Close() // regular file: the termios ioctls fail, fallback path

	var out strings.Builder
	errc := make(chan error, 1)
	res := make(chan string, 1)
	go func() {
		pw, err := readPasswordFiltered("pass: ", f, &out, cons)
		errc <- err
		res <- pw
	}()
	time.Sleep(50 * time.Millisecond)

	cr.feed("ab\x7fc\r") // "ab", backspace, "c", enter → "ac"
	if err := <-errc; err != nil {
		t.Fatalf("readPasswordFiltered: %v", err)
	}
	if pw := <-res; pw != "ac" {
		t.Fatalf("password: got %q, want %q", pw, "ac")
	}
	if o := out.String(); !strings.Contains(o, "pass: ") {
		t.Fatalf("prompt missing: %q", o)
	}
}

// TestReadPasswordFilteredInterrupt: ^C (a byte with ISIG off) aborts the
// prompt with ErrInterrupt.
func TestReadPasswordFilteredInterrupt(t *testing.T) {
	cr := newChanReader()
	f := newResponseFilter(cr)
	f.startPump()
	cons, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(cons.Name())
	cons.Close()

	errc := make(chan error, 1)
	go func() {
		_, err := readPasswordFiltered("pass: ", f, io.Discard, cons)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cr.feedBytes([]byte{0x03}) // ^C with ISIG off

	if err := <-errc; err != ErrInterrupt {
		t.Fatalf("got %v, want ErrInterrupt", err)
	}
}
