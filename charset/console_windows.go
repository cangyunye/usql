//go:build windows

// Windows console code page handling. conhost reports the active code
// pages via GetConsoleOutputCP (text written to the console) and
// GetConsoleCP (keyboard input). zh-CN systems default both to cp936
// (GBK); cp54936 selects GB18030. When usql runs redirected (no console),
// the calls return 0 and the POSIX locale handling applies.
//
// Rather than transcoding UTF-8 output down to a legacy code page — which
// cannot pass through readline's ANSIWriter, a rune-oriented writer that
// assumes UTF-8 — SetupConsole switches the console itself to UTF-8 and
// leaves the transcoding path as a fallback for when the switch is not
// possible.

package charset

import (
	"sync"

	runewidth "github.com/mattn/go-runewidth"
	"golang.org/x/sys/windows"
)

// utf8CodePage is the Windows console code page for UTF-8.
const utf8CodePage = 65001

// SetupConsole switches the attached console's input and output code pages
// to UTF-8, so usql's UTF-8 output — box-drawing table borders, CJK text —
// renders correctly on a legacy code page console (zh-CN systems default
// to cp936/GBK), and returns a func restoring the original code pages.
//
// The switch is what makes Windows output correct: every writer in the
// pipeline (rline's ANSIWriter, error paths printing straight to
// os.Stdout/os.Stderr, cobra's help output) emits UTF-8, and the console
// decodes it as UTF-8. It also makes GetConsoleOutputCP report 65001, so
// the GBK-family transcoding in OutputEncoding/ConsoleInputEncoding
// disables itself; those remain the fallback for POSIX GBK locales and for
// the no-console case.
//
// When no console is attached (redirected, GUI, service) the code pages
// are left alone and the returned func is a no-op. The returned func is
// safe to call more than once.
func SetupConsole() func() {
	outCP, outErr := windows.GetConsoleOutputCP()
	inCP, inErr := windows.GetConsoleCP()
	var restores []func()
	if outErr == nil && outCP != utf8CodePage {
		if err := windows.SetConsoleOutputCP(utf8CodePage); err == nil {
			restores = append(restores, func() { _ = windows.SetConsoleOutputCP(outCP) })
		}
	}
	if inErr == nil && inCP != utf8CodePage {
		if err := windows.SetConsoleCP(utf8CodePage); err == nil {
			restores = append(restores, func() { _ = windows.SetConsoleCP(inCP) })
		}
	}
	if len(restores) == 0 {
		return func() {}
	}
	// the console now displays UTF-8: undo the init-time EastAsianWidth
	// override a GB18030 console applied (init runs before main), since
	// ambiguous-width runes are one column wide on a UTF-8 console
	runewidth.DefaultCondition.EastAsianWidth = false
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, restore := range restores {
				restore()
			}
		})
	}
}

// consoleOutputCharset reports the charset implied by the console output
// code page ("" when redirected or non-GBK family, "utf-8" for 65001 so a
// UTF-8 console is not overridden by the POSIX locale fallback).
func consoleOutputCharset() string {
	cp, err := windows.GetConsoleOutputCP()
	if err != nil {
		return ""
	}
	return codePageCharset(cp)
}

// consoleInputCharset reports the charset implied by the console input code
// page: "" when redirected (no console), "utf-8" for a console with any
// other input code page (bytes pass through unchanged — the least-wrong
// decoding for single-byte pages, and exact for 65001), or the GBK-family
// charset name.
func consoleInputCharset() string {
	cp, err := windows.GetConsoleCP()
	if err != nil || cp == 0 {
		return ""
	}
	switch cp {
	case 936:
		return "cp936"
	case 54936:
		return "gb18030"
	}
	return "utf-8"
}

// codePageCharset maps a console code page to a charset name.
func codePageCharset(cp uint32) string {
	switch cp {
	case 936:
		return "cp936"
	case 54936:
		return "gb18030"
	case utf8CodePage:
		return "utf-8"
	}
	return ""
}
