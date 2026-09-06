//go:build windows

// Windows console code page detection: conhost reports the active code
// pages via GetConsoleOutputCP (text written to the console) and
// GetConsoleCP (keyboard input). zh-CN systems default both to cp936
// (GBK); cp54936 selects GB18030. When usql runs redirected (no console),
// the calls return 0 and the POSIX locale handling applies.

package charset

import "golang.org/x/sys/windows"

// consoleOutputCharset reports the charset implied by the console output
// code page ("" when redirected or non-GBK family).
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
	}
	return ""
}
