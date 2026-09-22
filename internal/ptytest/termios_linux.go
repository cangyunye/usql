//go:build linux

package ptytest

import "golang.org/x/sys/unix"

// termiosFlags reports the ECHO, ICANON and ISIG bits of the line
// discipline on fd.
func termiosFlags(fd int) (echo, icanon, isig bool, err error) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return false, false, false, err
	}
	return t.Lflag&unix.ECHO != 0, t.Lflag&unix.ICANON != 0, t.Lflag&unix.ISIG != 0, nil
}
