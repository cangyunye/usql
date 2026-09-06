package uitheme

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/xo/tblfmt"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// alignFixture is the shared content matrix: pure ASCII, pure hanzi,
// hanzi/ASCII/digit interleave, fullwidth, CJK punctuation, ambiguous-width
// characters (narrow under UTF-8, wide under GBK), emoji (wide, and
// unrepresentable in GBK), and embedded SGR that must not count toward
// width.
var alignFixture = [][]string{
	{"1", "english only"},
	{"2", "用户信息"},
	{"3", "用户user表2024_v2"},
	{"4", "ＡＢＣ１２３，。、"},
	{"5", "±°①ⅡαΩ§"},
	{"6", "😀!x"},
	{"7", "\x1b[31mred\x1b[0m"},
	{"8", "你好😀世界"},
}

// localeEAW captures the runewidth condition at process start: the charset
// package init established it from the machine's locale (runewidth handles
// gbk/gb2312 itself, charset fixes gb18030).
var localeEAW = runewidth.DefaultCondition.EastAsianWidth

func setEAW(v bool) { runewidth.DefaultCondition.EastAsianWidth = v }

// setEAWLocale restores the runewidth condition for this machine's locale.
func setEAWLocale() { runewidth.DefaultCondition.EastAsianWidth = localeEAW }

// setColor overrides the color gate; under `go test` (no terminal) the
// theme is disabled by default and lipgloss's auto-detected profile is
// Ascii, so styling must be forced here too.
func setColor(enabled bool) func() {
	prev := colorDisabled
	colorDisabled = !enabled
	if enabled {
		lipgloss.SetColorProfile(termenv.ANSI256)
	} else {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	return func() {
		colorDisabled = prev
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

func TestCellWidth(t *testing.T) {
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"用户", 4},
		{"用户user表2024_v2", 17},
		{"ＡＢＣ", 6},
		{"±①", 2}, // ambiguous-width: narrow when EastAsianWidth is off
		{"😀", 2},
		{"\x1b[31mabc\x1b[0m", 3}, // SGR sequences are not displayable
	}
	for _, c := range cases {
		if got := CellWidth(c.s); got != c.want {
			t.Errorf("CellWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestCellWidthHonorsEastAsianWidth(t *testing.T) {
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	setEAW(true)
	defer setEAWLocale()
	// GBK-family locales measure ambiguous-width characters as two columns
	if got := CellWidth("±①α"); got != 6 {
		t.Errorf("CellWidth(±①α) with EastAsianWidth = %d, want 6", got)
	}
}

func TestCellWidthGBKConsole(t *testing.T) {
	SetConsoleEncoding(simplifiedchinese.GBK)
	defer SetConsoleEncoding(nil)
	// the emoji is unrepresentable in GBK: the console renders '?' (width 1)
	if got := CellWidth("😀x"); got != 2 {
		t.Errorf("CellWidth(😀x) GBK console = %d, want 2", got)
	}
	// hanzi survive transcoding at two columns each
	if got := CellWidth("你好"); got != 4 {
		t.Errorf("CellWidth(你好) GBK console = %d, want 4", got)
	}
}

// assertTableAligned renders the fixture as a themed table and verifies the
// alignment invariants on the current console:
//
//  1. every header/data row has the same display width — this must hold on
//     every console and under both ambiguous-width conventions, because
//     CellWidth is what the padding math uses;
//  2. every horizontal border has exactly one ─ glyph per measured column
//     unit (+2 padding) — a glyph-count check, independent of locale;
//  3. when box-drawing glyphs and content measure in the same unit
//     (EastAsianWidth off, the common modern-terminal convention), the
//     entire table — borders included — has one uniform display width.
func assertTableAligned(t *testing.T, label string) {
	t.Helper()
	out := Current().Table([]string{"id", "名称"}, alignFixture)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(alignFixture)+4 {
		t.Fatalf("[%s] line count = %d, want %d", label, len(lines), len(alignFixture)+4)
	}
	want := CellWidth(lines[1])
	for i, line := range lines {
		if !strings.ContainsRune(line, '│') {
			continue
		}
		if got := CellWidth(line); got != want {
			t.Errorf("[%s] row %d width = %d, want %d (line %q)", label, i, got, want, line)
		}
	}
	assertBorders(t, label, lines)
	if !runewidth.DefaultCondition.EastAsianWidth {
		want := CellWidth(lines[0])
		for i, line := range lines {
			if got := CellWidth(line); got != want {
				t.Errorf("[%s] full line %d width = %d, want %d (line %q)", label, i, got, want, line)
			}
		}
	}
}

// assertBorders checks that each ─ run in a horizontal border has exactly
// as many glyphs as the corresponding column's measured width (+2 padding).
func assertBorders(t *testing.T, label string, lines []string) {
	t.Helper()
	// column widths (with padding) from the first data row, parsed from
	// the plain rendering so SGR styling does not pollute the split
	var widths []int
	for _, line := range lines {
		if strings.ContainsRune(line, '│') {
			for _, seg := range strings.Split(strings.Trim(stripANSI(line), "│"), "│") {
				widths = append(widths, CellWidth(seg))
			}
			break
		}
	}
	for _, line := range lines {
		if !strings.ContainsRune(line, '─') {
			continue
		}
		line = stripANSI(line)
		var runs []int
		n := 0
		for _, r := range line {
			switch r {
			case '─':
				n++
			case '┬', '┼', '┴':
				runs = append(runs, n)
				n = 0
			}
		}
		runs = append(runs, n)
		if len(runs) != len(widths) {
			t.Errorf("[%s] border %q has %d column runs, want %d", label, line, len(runs), len(widths))
			continue
		}
		for i, run := range runs {
			if run != widths[i] {
				t.Errorf("[%s] border %q run %d = %d glyphs, want %d",
					label, line, i, run, widths[i])
			}
		}
	}
}

func TestTableAlignedUTF8Console(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	assertTableAligned(t, "utf-8")
}

func TestTableAlignedGBKConsole(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(simplifiedchinese.GBK)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	assertTableAligned(t, "gbk")
}

func TestTableAlignedGBKConsoleEastAsianWidth(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(simplifiedchinese.GBK)
	defer SetConsoleEncoding(nil)
	setEAW(true)
	defer setEAWLocale()
	// row-to-row alignment and border units hold under the wide-ambiguous
	// convention; full-line equality is skipped by design (see
	// assertTableAligned): on terminals that render box drawing wide, the
	// uniform-width invariant cannot hold for mixed ASCII/CJK content.
	assertTableAligned(t, "gbk-eaw")
}

func TestTableAlignedNoColor(t *testing.T) {
	defer setColor(false)()
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	assertTableAligned(t, "nocolor")
}

// fakeResultSet feeds fixed rows through the real tblfmt encoder.
type fakeResultSet struct {
	pos  int
	cols []string
	vals [][]any
	err  error
}

func (f *fakeResultSet) Columns() ([]string, error) { return f.cols, nil }
func (f *fakeResultSet) Next() bool                 { return f.pos < len(f.vals) }
func (f *fakeResultSet) Close() error               { return nil }
func (f *fakeResultSet) Err() error                 { return f.err }
func (f *fakeResultSet) NextResultSet() bool        { return false }

func (f *fakeResultSet) Scan(dst ...any) error {
	if f.pos >= len(f.vals) {
		return errors.New("no rows")
	}
	for i := range dst {
		if d, ok := dst[i].(*any); ok {
			*d = f.vals[f.pos][i]
		}
	}
	f.pos++
	return nil
}

func TestPaintTablePreservesContentAndAligns(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	// tblfmt refuses unicode line styles when EastAsianWidth is on (its
	// line-style validation requires width-1 glyphs) — a pre-existing
	// tblfmt limitation, so force the narrow convention here
	setEAW(false)
	defer setEAWLocale()
	plain := tblfmtString(t, &fakeResultSet{
		cols: []string{"id", "名称"},
		vals: [][]any{
			{"1", "english only"},
			{"2", "用户user表2024_v2"},
			{"3", "你好😀世界"},
			{"4", nil},
		},
	})
	if !strings.Contains(plain, "│") {
		t.Fatalf("tblfmt did not render a unicode table:\n%s", plain)
	}
	th := Current()
	painted := th.PaintTable(plain)
	// painting must not add, remove, or rewrap any character
	if got := stripANSI(painted); got != plain {
		t.Errorf("painted content changed:\nwant %q\ngot  %q", plain, got)
	}
	// painting must preserve every line's display width exactly (only SGR
	// sequences are inserted; tblfmt's own row padding is its long-standing
	// behavior and is not re-aligned here)
	plainLines := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	paintedLines := strings.Split(strings.TrimSuffix(painted, "\n"), "\n")
	if len(plainLines) != len(paintedLines) {
		t.Fatalf("line count changed: plain %d, painted %d", len(plainLines), len(paintedLines))
	}
	for i := range plainLines {
		if w1, w2 := CellWidth(plainLines[i]), CellWidth(paintedLines[i]); w1 != w2 {
			t.Errorf("line %d width changed by painting: %d -> %d", i, w1, w2)
		}
	}
	// header must be bolded, and data rows zebra-shaded
	if !strings.Contains(painted, "\x1b[1m") {
		t.Error("header row was not styled bold")
	}
	if !strings.Contains(painted, "\x1b[48;5;235m") {
		t.Error("no zebra background applied")
	}
}

func TestPaintTableGBKConsoleAligns(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(simplifiedchinese.GBK)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	plain := tblfmtString(t, &fakeResultSet{
		cols: []string{"id", "名称"},
		vals: [][]any{
			{"1", "用户user表2024_v2"},
			{"2", "你好😀世界"},
			{"3", "ＡＢＣ±①"},
		},
	})
	painted := Current().PaintTable(plain)
	plainLines := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	paintedLines := strings.Split(strings.TrimSuffix(painted, "\n"), "\n")
	for i := range plainLines {
		if w1, w2 := CellWidth(plainLines[i]), CellWidth(paintedLines[i]); w1 != w2 {
			t.Errorf("GBK console: line %d width changed by painting: %d -> %d", i, w1, w2)
		}
	}
	// the emoji row must measure exactly as if the emoji were the 1-wide
	// '?' the GBK console will render
	for _, line := range paintedLines {
		if strings.Contains(line, "😀") {
			if w, want := CellWidth(line), CellWidth(strings.ReplaceAll(line, "😀", "?")); w != want {
				t.Errorf("GBK console: emoji row width = %d, want %d (as rendered with '?')", w, want)
			}
		}
	}
}

func TestPaintTablePassthrough(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	th := Current()
	// non-table text passes through untouched
	for _, s := range []string{"(4 rows)\n", "Time: 1.234 ms\n", "plain text", ""} {
		if got := th.PaintTable(s); got != s {
			t.Errorf("PaintTable(%q) = %q, want unchanged", s, got)
		}
	}
	// ascii linestyle tables are not painted (the '│' keying would be wrong)
	ascii := "| a | b |\n|---|---|\n"
	if got := th.PaintTable(ascii); got != ascii {
		t.Errorf("ascii table painted: %q", got)
	}
}

func TestLineWriterMatchesPaintTable(t *testing.T) {
	defer setColor(true)()
	SetConsoleEncoding(nil)
	defer SetConsoleEncoding(nil)
	setEAW(false)
	defer setEAWLocale()
	plain := tblfmtString(t, &fakeResultSet{
		cols: []string{"id", "名称"},
		vals: [][]any{
			{"1", "english only"},
			{"2", "用户user表2024_v2"},
			{"3", "你好😀世界"},
		},
	})
	want := Current().PaintTable(plain)
	// stream the same output through LineWriter in awkward chunk splits
	// (including mid-rune and mid-line boundaries)
	for _, chunk := range []int{1, 3, 7, 13} {
		var buf bytes.Buffer
		lw := Current().LineWriter(&buf)
		for i := 0; i < len(plain); i += chunk {
			end := i + chunk
			if end > len(plain) {
				end = len(plain)
			}
			if _, err := lw.Write([]byte(plain[i:end])); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		if err := lw.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if got := buf.String(); got != want {
			t.Errorf("chunk %d: streaming output != PaintTable:\nwant %q\ngot  %q", chunk, want, got)
		}
	}
}

// tblfmtString renders rs with the aligned+unicode params the colored table
// path is keyed on.
func tblfmtString(t *testing.T, rs tblfmt.ResultSet) string {
	t.Helper()
	var buf bytes.Buffer
	params := map[string]string{
		"format":                   "aligned",
		"linestyle":                "unicode",
		"border":                   "1",
		"footer":                   "on",
		"null":                     "",
		"tuples_only":              "off",
		"unicode_border_linestyle": "single",
	}
	if err := tblfmt.EncodeAll(&buf, rs, params); err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	return buf.String()
}
