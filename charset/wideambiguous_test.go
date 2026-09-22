package charset

import (
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/xo/tblfmt"
)

// TestWideAmbiguousEnv covers the locale detection that drives the
// interactive line-style default: a gb18030 console charset measures
// ambiguous-width characters as two cells, and RUNEWIDTH_EASTASIAN
// overrides the heuristic in both directions.
func TestWideAmbiguousEnv(t *testing.T) {
	cases := []struct {
		name string
		lc   string
		rw   string
		want bool
	}{
		{"utf8-locale", "zh_CN.UTF-8", "", false},
		{"c-locale", "C.UTF-8", "", false},
		{"gb18030-locale", "zh_CN.GB18030", "", true},
		{"gbk-locale", "zh_CN.GBK", "", false}, // not gb18030: no forcing
		{"env-force-wide", "C.UTF-8", "1", true},
		{"env-force-narrow", "zh_CN.GB18030", "0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.lc)
			t.Setenv("LC_CTYPE", "")
			t.Setenv("LANG", "")
			t.Setenv("RUNEWIDTH_EASTASIAN", tc.rw)
			if got := wideAmbiguous(); got != tc.want {
				t.Fatalf("wideAmbiguous() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLineStyleWidthConstraint is the invariant behind the interactive
// default: when ambiguous characters measure two cells wide, tblfmt
// rejects the unicode line style, so the default must fall back to ascii
// (this was the "invalid line style" failure on CJK locales).
func TestLineStyleWidthConstraint(t *testing.T) {
	wide := runewidth.DefaultCondition.EastAsianWidth

	_, unicodeErr := tblfmt.NewTableEncoder(nil, tblfmt.WithLineStyle(tblfmt.UnicodeLineStyle()))
	_, asciiErr := tblfmt.NewTableEncoder(nil, tblfmt.WithLineStyle(tblfmt.ASCIILineStyle()))

	if wide {
		if unicodeErr == nil {
			t.Fatal("unicode line style accepted under wide ambiguous measuring: tblfmt's width check regressed")
		}
	} else {
		if unicodeErr != nil {
			t.Fatalf("unicode line style rejected under narrow measuring: %v", unicodeErr)
		}
	}
	if asciiErr != nil {
		t.Fatalf("ascii line style rejected: %v (the fallback must always work)", asciiErr)
	}
	// the constraint the interactive default must satisfy either way:
	if wide && !WideAmbiguous() || !wide && WideAmbiguous() {
		t.Fatal("WideAmbiguous() disagrees with runewidth's condition")
	}
}
