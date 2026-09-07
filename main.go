// Command usql is the universal command-line interface for SQL databases.
//
//go:debug x509negativeserial=1
package main

//go:generate go run gen.go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xo/usql/charset"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/handler"
	"github.com/xo/usql/internal"
	"github.com/xo/usql/rline"
	"github.com/xo/usql/text"
)

func main() {
	// switch the attached Windows console to UTF-8 before anything writes
	// to it — table borders and CJK text are mojibake on a legacy code page
	// console otherwise; no-op (with a no-op restore) elsewhere. Must run
	// before rline.New reads the console code pages.
	restoreConsole := charset.SetupConsole()
	defer restoreConsole()
	// get available drivers and known build tags
	available, known := drivers.Available(), internal.KnownBuildTags()
	// report if database is supported
	if len(os.Args) == 2 &&
		strings.HasPrefix(os.Args[1], "--has-") &&
		strings.HasSuffix(os.Args[1], "-support") {
		n := os.Args[1][6 : len(os.Args[1])-8]
		if v, ok := known[n]; ok {
			n = v
		}
		var out int
		if _, ok := available[n]; ok {
			out = 1
		}
		fmt.Fprint(os.Stdout, out)
		return
	}
	// run
	if err := New(os.Args).ExecuteContext(context.Background()); err != nil && err != io.EOF && err != rline.ErrInterrupt {
		var he *handler.Error
		if !errors.As(err, &he) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		var e *drivers.Error
		if errors.As(err, &e) && e.Err == text.ErrDriverNotAvailable {
			m := make(map[string]string, len(known))
			for k, v := range known {
				m[v] = k
			}
			tag := e.Driver
			if t, ok := m[tag]; ok {
				tag = t
			}
			rev := "latest"
			if text.CommandVersion == "0.0.0-dev" || strings.Contains(text.CommandVersion, "-") {
				rev = "master"
			}
			fmt.Fprintf(os.Stderr, text.GoInstallHint, tag, rev)
		}
		switch estr := err.Error(); {
		case err == text.ErrWrongNumberOfArguments,
			strings.HasPrefix(estr, "unknown flag:"),
			strings.HasPrefix(estr, "unknown shorthand flag:"),
			strings.HasPrefix(estr, "bad flag syntax:"),
			strings.HasPrefix(estr, "flag needs an argument:"):
			fmt.Fprintln(os.Stderr, text.CommandHelpHint)
		}
		// the error exit bypasses the deferred restore
		restoreConsole()
		os.Exit(1)
	}
}
