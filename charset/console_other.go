//go:build !windows

// Non-Windows platforms determine the console encoding from the POSIX
// locale; there is no separate code page API.

package charset

// consoleOutputCharset reports nothing on non-Windows platforms.
func consoleOutputCharset() string { return "" }

// consoleInputCharset reports nothing on non-Windows platforms.
func consoleInputCharset() string { return "" }
