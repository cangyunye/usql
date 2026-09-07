//go:build chart

package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/xo/usql/metacmd/charts"
)

func TestRenderChartSVG(t *testing.T) {
	opts := `{"xAxis":{"type":"category","data":["a","b","c"]},"yAxis":{"type":"value"},"series":[{"type":"bar","data":[1,2,3]}]}`
	res, err := renderChartSVG(context.Background(), charts.ChartConfig{W: 400, H: 300}, opts)
	if err != nil {
		t.Fatalf("renderChartSVG failed: %v", err)
	}
	if !strings.Contains(res, "<svg") {
		t.Fatalf("expected SVG output, got: %.120s", res)
	}
}
