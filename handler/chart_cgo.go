//go:build cgo

package handler

import (
	"image/color"
	"io"

	"github.com/kenshaw/rasterm"
	"github.com/xo/resvg"
)

// renderChartImage rasterizes an ECharts SVG document and encodes it into the
// terminal's graphics protocol. Requires a cgo build (resvg is a binding to
// Rust's libresvg).
func renderChartImage(w io.Writer, typ rasterm.TermType, svg string, background color.Color) error {
	img, err := resvg.Render([]byte(svg), resvg.WithBackground(background))
	if err != nil {
		return err
	}
	return typ.Encode(w, img)
}
