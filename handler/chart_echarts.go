//go:build chart

package handler

import (
	"context"

	"github.com/xo/echartsgoja"
	"github.com/xo/usql/metacmd/charts"
)

// chartEnabled reports whether the embedded ECharts JS engine is compiled in.
const chartEnabled = true

// renderChartSVG renders ECharts options JSON as an SVG document, by running
// the embedded ECharts scripts on the goja JS runtime.
func renderChartSVG(ctx context.Context, cfg charts.ChartConfig, opts string) (string, error) {
	echarts := echartsgoja.New(echartsgoja.WithWidthHeight(cfg.W, cfg.H))
	return echarts.RenderOptions(ctx, opts)
}
