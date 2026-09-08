package rline

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xo/usql/rline/readline"
)

// IO implementations that must support presetting the pending input line
// (used by \alias).
var (
	_ LineEditor = (*Rline)(nil)
	_ LineEditor = (*tuiRline)(nil)
)

// syncBuffer is a mutex-guarded bytes.Buffer: the readline engine goroutine
// renders into it while the test polls its contents.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func TestRlineSetLine(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinW.Close()
	out := &syncBuffer{}
	l, err := readline.NewEx(&readline.Config{
		Stdin:          stdinR,
		Stdout:         out,
		Stderr:         io.Discard,
		FuncIsTerminal: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	r := &Rline{Inst: l, Out: out, Err: io.Discard, Int: true}
	// cursor at the $ placeholder ("select * from " is 14 runes)
	r.SetLine([]rune("select * from $tablename"), 14)

	type result struct {
		line []rune
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := l.Operation.Runes()
		ch <- result{line, err}
	}()
	// wait for the preset line to render, then submit it
	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Contains(out.Bytes(), []byte("select * from $tablename")) {
		if time.Now().After(deadline) {
			t.Fatal("preset line was not rendered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case re := <-ch:
		if re.err != nil {
			t.Fatalf("runes: %v", re.err)
		}
		if string(re.line) != "select * from $tablename" {
			t.Fatalf("got %q, want %q", string(re.line), "select * from $tablename")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for line")
	}
}

func TestSetLineClampsPos(t *testing.T) {
	tr := newTUI(strings.NewReader(""), io.Discard, io.Discard, "", nil)
	tr.SetLine([]rune("abc"), 99)
	if tr.pendPos != 3 {
		t.Fatalf("pendPos got %d, want 3", tr.pendPos)
	}
	tr.SetLine([]rune("abc"), -1)
	if tr.pendPos != 3 {
		t.Fatalf("pendPos got %d, want 3", tr.pendPos)
	}
}
