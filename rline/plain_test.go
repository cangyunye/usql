package rline

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// nextAll drains the reader through the IO interface.
func nextAll(t *testing.T, p IO) []string {
	t.Helper()
	var got []string
	for {
		r, err := p.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, string(r))
	}
}

func TestPlainReaderLines(t *testing.T) {
	p := newPlain(strings.NewReader("select 1;\nselect 2;\n"), io.Discard, io.Discard, false)
	got := nextAll(t, p)
	want := []string{"select 1;", "select 2;"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestPlainReaderPartialLine(t *testing.T) {
	p := newPlain(strings.NewReader("no trailing newline"), io.Discard, io.Discard, false)
	got := nextAll(t, p)
	if !reflect.DeepEqual(got, []string{"no trailing newline"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestPlainReaderCRLF(t *testing.T) {
	p := newPlain(strings.NewReader("a\r\nb\r\n"), io.Discard, io.Discard, false)
	got := nextAll(t, p)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("lines = %q", got)
	}
}

// TestPlainReaderNoInput covers -c/-f runs: stdin is never read, and the
// password prompt is unavailable, matching the previous readline-based path
// (nil Next/Password funcs).
func TestPlainReaderNoInput(t *testing.T) {
	p := newPlain(strings.NewReader("ignored"), io.Discard, io.Discard, true)
	if _, err := p.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next = %v, want io.EOF", err)
	}
	if _, err := p.Password("pw:"); !errors.Is(err, ErrPasswordNotAvailable) {
		t.Fatalf("Password = %v, want ErrPasswordNotAvailable", err)
	}
}

func TestPlainReaderNoops(t *testing.T) {
	p := newPlain(strings.NewReader("x\n"), io.Discard, io.Discard, false)
	p.Prompt("> ")
	p.Completer(nil)
	p.SetOutput(func(s string) string { return s })
	if p.Interactive() || p.Cygwin() {
		t.Fatal("plainReader is never interactive")
	}
	if err := p.Save("select 1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if p.Stdout() != io.Discard || p.Stderr() != io.Discard {
		t.Fatal("Stdout/Stderr must return the configured writers")
	}
}

// TestPlainReaderCloseRunsCovers verifies Close releases registered
// resources: the -o output file closer previously ran through the readline
// instance's close chain, which plainReader replaces.
func TestPlainReaderCloseRunsClosers(t *testing.T) {
	p := newPlain(strings.NewReader(""), io.Discard, io.Discard, false)
	ran := false
	p.closers = append(p.closers, func() error {
		ran = true
		return nil
	})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !ran {
		t.Fatal("Close must run the registered closers")
	}
}

// TestNewNonInteractivePlain verifies the decoupling itself: non-interactive
// runs (pipes, -o) no longer construct a readline instance.
func TestNewNonInteractivePlain(t *testing.T) {
	io, err := New(false, false, false, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := io.(*plainReader); !ok {
		t.Fatalf("non-interactive New returned %T, want *plainReader", io)
	}
}
