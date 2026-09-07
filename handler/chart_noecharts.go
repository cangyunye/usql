//go:build !chart

package handler

import (
	"context"

	"github.com/xo/usql/metacmd/charts"
	"github.com/xo/usql/text"
)

// chartEnabled reports whether the embedded ECharts JS engine is compiled in.
// The engine (goja plus the embedded echarts.min.js) is excluded from builds
// without the chart build tag to keep the binary small.
const chartEnabled = false

// renderChartSVG is the no-chart fallback; doExecChart returns
// text.ErrChartNotBuilt before reaching it.
func renderChartSVG(_ context.Context, _ charts.ChartConfig, _ string) (string, error) {
	return "", text.ErrChartNotBuilt
}
