//go:build windows

package charset

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestSetupConsole(t *testing.T) {
	origOut, err := windows.GetConsoleOutputCP()
	if err != nil {
		t.Skip("no console attached")
	}
	origIn, inErr := windows.GetConsoleCP()

	restore := SetupConsole()
	if cp, err := windows.GetConsoleOutputCP(); err == nil && cp != utf8CodePage {
		t.Errorf("output code page = %d, want %d", cp, utf8CodePage)
	}
	if inErr == nil {
		if cp, err := windows.GetConsoleCP(); err == nil && cp != utf8CodePage {
			t.Errorf("input code page = %d, want %d", cp, utf8CodePage)
		}
	}
	// the UTF-8 console must not be transcoded, whatever the locale says
	t.Setenv("LANG", "zh_CN.GBK")
	if enc := OutputEncoding(); enc != nil {
		t.Error("OutputEncoding() != nil on a UTF-8 console with a GBK locale")
	}

	restore()
	restore() // must be idempotent
	if cp, err := windows.GetConsoleOutputCP(); err == nil && cp != origOut {
		t.Errorf("output code page = %d after restore, want %d", cp, origOut)
	}
	if inErr == nil {
		if cp, err := windows.GetConsoleCP(); err == nil && cp != origIn {
			t.Errorf("input code page = %d after restore, want %d", cp, origIn)
		}
	}
}
