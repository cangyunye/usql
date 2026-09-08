package rline

import (
	"bufio"
	"io"
	"strings"
)

// plainReader is the non-interactive IO: plain line-at-a-time input with no
// terminal handling, replacing the readline engine's non-terminal read path.
// It serves piped/scripted stdin (-c, -f, pipes, -o); interactive sessions
// use the readline or bubbletea engines. (usql fork)
type plainReader struct {
	in    *bufio.Reader
	stdin io.Reader
	out   io.Writer
	err   io.Writer
	// noInput marks -c/-f runs: stdin is never consumed (matching the
	// previous path's nil Next) and passwords are unavailable.
	noInput bool
	// closers run on Close; New registers the -o output file here, which
	// previously ran through the readline instance's close chain.
	closers []func() error
}

// newPlain creates the non-interactive IO. When noInput is set, in is never
// read.
func newPlain(in io.Reader, out, err io.Writer, noInput bool) *plainReader {
	return &plainReader{
		in:      bufio.NewReader(in),
		stdin:   in,
		out:     out,
		err:     err,
		noInput: noInput,
	}
}

// Next returns the next line as runes, without the trailing newline. A
// final line without a newline is returned before EOF, like the previous
// readline-based path.
func (p *plainReader) Next() ([]rune, error) {
	if p.noInput {
		return nil, io.EOF
	}
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return nil, io.EOF
	}
	return []rune(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")), nil
}

// Close releases registered resources (the -o output file).
func (p *plainReader) Close() error {
	var err error
	for _, f := range p.closers {
		if e := f(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// Stdout is the standard out.
func (p *plainReader) Stdout() io.Writer { return p.out }

// Stderr is the standard error out.
func (p *plainReader) Stderr() io.Writer { return p.err }

// Interactive reports non-interactive.
func (p *plainReader) Interactive() bool { return false }

// Cygwin reports non-cygwin.
func (p *plainReader) Cygwin() bool { return false }

// Prompt is a no-op: nothing is echoed.
func (p *plainReader) Prompt(string) {}

// Completer is a no-op: there is no interactive completion.
func (p *plainReader) Completer(Completer) {}

// Save is a no-op: the handler only records history for interactive
// sessions, so this is never reached in practice.
func (p *plainReader) Save(string) error { return nil }

// Password prompts for a password. On -c/-f runs it is unavailable; on
// piped input it reads a plain line (echo cannot be restored on a pipe).
func (p *plainReader) Password(prompt string) (string, error) {
	if p.noInput {
		return "", ErrPasswordNotAvailable
	}
	return readPassword(prompt, p.stdin, p.out)
}

// SetOutput is a no-op: there is no input line to highlight.
func (p *plainReader) SetOutput(func(string) string) {}

// LineEditor is not implemented: there is no pending line to preset.
