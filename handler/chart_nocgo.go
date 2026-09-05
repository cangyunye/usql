//go:build !cgo

package handler

import (
	"fmt"
	"image/color"
	"io"

	"github.com/kenshaw/rasterm"
)

// renderChartImage is the no-cgo fallback: terminal image output needs the
// resvg cgo binding, so it reports how to get the SVG instead.
func renderChartImage(w io.Writer, _ rasterm.TermType, _ string, _ color.Color) error {
	return fmt.Errorf("terminal chart image output requires a cgo build; use \\chart ... file= to write the SVG to a file")
}
