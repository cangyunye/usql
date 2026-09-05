// Package rline provides a readline implementation for usql.
package rline

import (
	"errors"
	"io"
	"os"

	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"

	"github.com/xo/usql/charset"
	"github.com/xo/usql/rline/readline"
)

var (
	// ErrInterrupt is the interrupt error.
	ErrInterrupt = readline.ErrInterrupt
	// ErrPasswordNotAvailable is the password not available error.
	ErrPasswordNotAvailable = errors.New("password not available")
)

// IO is the common input/output interface.
type IO interface {
	// Next returns the next line of runes (excluding '\n') from the input.
	Next() ([]rune, error)
	// Close closes the IO.
	Close() error
	// Stdout is the IO's standard out.
	Stdout() io.Writer
	// Stderr is the IO's standard error out.
	Stderr() io.Writer
	// Interactive determines if the IO is an interactive terminal.
	Interactive() bool
	// Cygwin determines if the IO is a Cygwin interactive terminal.
	Cygwin() bool
	// Prompt sets the prompt for the next interactive line read.
	Prompt(string)
	// Completer sets the auto-completer.
	Completer(readline.AutoCompleter)
	// Save saves a line of history.
	Save(string) error
	// Password prompts for a password.
	Password(string) (string, error)
	// SetOutput sets the output filter func.
	SetOutput(func(string) string)
}

// Rline provides a type compatible with the IO interface.
type Rline struct {
	Inst *readline.Instance
	N    func() ([]rune, error)
	C    func() error
	Out  io.Writer
	Err  io.Writer
	Int  bool
	Cyg  bool
	P    func(string)
	A    func(readline.AutoCompleter)
	S    func(string) error
	Pw   func(string) (string, error)
}

// Next returns the next line of runes (excluding '\n') from the input.
func (l *Rline) Next() ([]rune, error) {
	if l.N != nil {
		return l.N()
	}
	return nil, io.EOF
}

// Close closes the IO.
func (l *Rline) Close() error {
	if l.C != nil {
		return l.C()
	}
	return nil
}

// Stdout is the IO's standard out.
func (l *Rline) Stdout() io.Writer {
	return l.Out
}

// Stderr is the IO's standard error out.
func (l *Rline) Stderr() io.Writer {
	return l.Err
}

// Interactive determines if the IO is an interactive terminal.
func (l *Rline) Interactive() bool {
	return l.Int
}

// Cygwin determines if the IO is a Cygwin interactive terminal.
func (l *Rline) Cygwin() bool {
	return l.Cyg
}

// Prompt sets the prompt for the next interactive line read.
func (l *Rline) Prompt(s string) {
	if l.P != nil {
		l.P(s)
	}
}

// Completer sets the auto-completer.
func (l *Rline) Completer(a readline.AutoCompleter) {
	if l.A != nil {
		l.A(a)
	}
}

// Save saves a line of history.
func (l *Rline) Save(s string) error {
	if l.S != nil {
		return l.S(s)
	}
	return nil
}

// Password prompts for a password.
func (l *Rline) Password(prompt string) (string, error) {
	if l.Pw != nil {
		return l.Pw(prompt)
	}
	return "", ErrPasswordNotAvailable
}

// SetOutput sets the output format func.
func (l *Rline) SetOutput(f func(string) string) {
	l.Inst.Config.Output = f
}

// New creates a new readline input/output handler.
func New(interactive, cygwin, forceNonInteractive bool, out, histfile string) (IO, error) {
	var closers []func() error
	// configure stdin
	var stdin io.ReadCloser
	switch {
	case forceNonInteractive:
		interactive, cygwin = false, false
	case cygwin:
		stdin = os.Stdin
	default:
		stdin = readline.Stdin
	}
	// configure stdout
	var stdout io.WriteCloser
	switch {
	case out != "":
		var err error
		stdout, err = os.OpenFile(out, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		closers = append(closers, stdout.Close)
		interactive = false
	case cygwin:
		stdout = os.Stdout
	default:
		stdout = readline.Stdout
	}
	// configure stderr
	var stderr io.Writer = os.Stderr
	if !cygwin {
		stderr = readline.Stderr
	}
	// transcode console output to the terminal character set, when the
	// locale requests a non-UTF-8 encoding (e.g. zh_CN.GBK); file and pipe
	// output (-o) is left as UTF-8
	if out == "" {
		if enc := charset.OutputEncoding(); enc != nil {
			stdout = newEncodedWriter(stdout, enc)
			stderr = newEncodedWriter(stderr, enc)
		}
	}
	if interactive {
		// wrap it with cancelable stdin
		stdin = readline.NewCancelableStdin(stdin)
	}
	// create readline instance
	l, err := readline.NewEx(&readline.Config{
		HistoryFile:            histfile,
		DisableAutoSaveHistory: true,
		InterruptPrompt:        "^C",
		HistorySearchFold:      true,
		Stdin:                  stdin,
		Stdout:                 stdout,
		Stderr:                 stderr,
		// typing-time candidate menu (display-only); TAB still completes
		LiveComplete: interactive || cygwin,
		FuncIsTerminal: func() bool {
			return interactive || cygwin
		},
		FuncFilterInputRune: func(r rune) (rune, bool) {
			if r == readline.CharCtrlZ {
				return r, false
			}
			return r, true
		},
	})
	if err != nil {
		return nil, err
	}
	closers = append(closers, l.Close)
	n := l.Operation.Runes
	pw := func(prompt string) (string, error) {
		buf, err := l.ReadPassword(prompt)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	if forceNonInteractive {
		n, pw = nil, nil
	}
	return &Rline{
		Inst: l,
		N:    n,
		C: func() error {
			for _, f := range closers {
				_ = f()
			}
			return nil
		},
		Out: stdout,
		Err: stderr,
		Int: interactive || cygwin,
		Cyg: cygwin,
		P:   l.SetPrompt,
		A: func(a readline.AutoCompleter) {
			cfg := l.Config.Clone()
			cfg.AutoComplete = a
			// let the completer re-render the live menu when background
			// queries land
			if lc, ok := a.(readline.LiveAutoCompleter); ok {
				lc.SetLiveKick(l.Operation.LiveKick)
			}
			l.SetConfig(cfg)
		},
		S:  l.SaveHistory,
		Pw: pw,
	}, nil
}

// newEncodedWriter wraps w with a UTF-8 to enc transcoding writer. The
// returned writer's Close is a no-op: ownership of the underlying writer
// stays with the caller (readline handles terminal close, and closing
// transform.Writer would close the underlying terminal).
func newEncodedWriter(w io.Writer, enc encoding.Encoding) io.WriteCloser {
	tw := transform.NewWriter(w, encoding.ReplaceUnsupported(enc.NewEncoder()))
	return &encodedWriter{w: tw}
}

// encodedWriter transcodes UTF-8 writes to a console encoding.
type encodedWriter struct {
	w *transform.Writer
}

// Write satisfies io.Writer.
func (e *encodedWriter) Write(p []byte) (int, error) {
	return e.w.Write(p)
}

// Close satisfies io.WriteCloser without closing the underlying writer.
func (e *encodedWriter) Close() error {
	return nil
}
