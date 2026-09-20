package rline

import (
	"bytes"
	"io"
	"os"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// terminalWriter is the shape terminal libraries (bubbletea's term.File)
// probe to recognize a console; the encoded writer must satisfy it or the
// renderer sizes itself with width 0 and repaints stack instead of
// overwriting the prompt line.
type terminalWriter interface {
	io.ReadWriteCloser
	Fd() uintptr
}

var _ terminalWriter = (*encodedWriter)(nil)

func TestEncodedWriterExposesFd(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ew := newEncodedWriter(f, simplifiedchinese.GBK)
	if tw, ok := ew.(terminalWriter); !ok {
		t.Fatalf("encodedWriter does not satisfy the terminal probe interface")
	} else if tw.Fd() != f.Fd() {
		t.Fatalf("Fd() = %d, want the wrapped file's %d", tw.Fd(), f.Fd())
	}
}

func TestEncodedWriterTranscodes(t *testing.T) {
	var buf bytes.Buffer
	ew := newEncodedWriter(&buf, simplifiedchinese.GBK)
	if _, err := io.WriteString(ew, "a中b"); err != nil {
		t.Fatal(err)
	}
	// GBK for 中 is D6 D0; ASCII passes through
	if got := buf.String(); string(got) != "a\xd6\xd0b" {
		t.Fatalf("transcoded % x, want a d6 d0 b", got)
	}
	// a non-file wrapper still works, with a zero Fd and no reads
	ew2 := newEncodedWriter(&buf, simplifiedchinese.GBK)
	if tw, ok := ew2.(terminalWriter); ok && tw.Fd() != 0 {
		t.Fatalf("Fd() = %d for a non-file wrapper, want 0", tw.Fd())
	}
}
