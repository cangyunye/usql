package uitheme

import (
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/mattn/go-runewidth"
	"github.com/xo/usql/charset"
	"golang.org/x/text/encoding"
)

// ansiRE matches ANSI CSI escape sequences (SGR colors, cursor moves).
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;:]*[A-Za-z]")

var (
	consoleEncOnce sync.Once
	// consoleEncoding caches the console output encoding at first use.
	consoleEncoding encoding.Encoding
)

// cellWidth returns the display width of s on this console.
//
// It is the single width-measurement entry point for interactive output:
// ANSI escape sequences are ignored, characters the console encoding cannot
// represent count as the single-column '?' substitution they will render as
// (charset.ConsolePreview), and width is measured with go-runewidth — the
// same library tblfmt aligns with, including the EastAsianWidth locale fix
// charset applies for GB18030 (and runewidth's own gbk/gb2312 handling), so
// ambiguous-width characters measure identically in both.
func cellWidth(s string) int {
	return runewidth.StringWidth(charset.ConsolePreview(stripANSI(s), ConsoleEncoding()))
}

// ConsoleEncoding returns the cached console output encoding, nil for UTF-8.
func ConsoleEncoding() encoding.Encoding {
	consoleEncOnce.Do(func() { consoleEncoding = charset.OutputEncoding() })
	return consoleEncoding
}

// SetConsoleEncoding overrides the cached console encoding. Tests only.
func SetConsoleEncoding(enc encoding.Encoding) {
	consoleEncOnce.Do(func() {})
	consoleEncoding = enc
}

// stripANSI removes ANSI CSI escape sequences from s.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// Table renders headers and rows as a bordered table with theme styling:
// theme-colored borders, bold headers, and zebra-shaded even rows. Column
// widths are computed with cellWidth, so mixed CJK/ASCII content stays
// aligned on UTF-8 and GBK-family consoles alike. Rounded corners are
// degraded to square ones when the console encoding cannot display them.
func (t *Theme) Table(headers []string, rows [][]string) string {
	n := len(headers)
	if n == 0 {
		return ""
	}
	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = cellWidth(h)
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if w := cellWidth(row[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	rs := borderRunes()
	var b strings.Builder
	writeBorder(&b, t, rs.top, widths)
	writeRow(&b, t, rs, headers, widths, rowHeader, -1)
	writeBorder(&b, t, rs.mid, widths)
	for i, row := range rows {
		writeRow(&b, t, rs, row, widths, rowData, i)
	}
	writeBorder(&b, t, rs.bot, widths)
	return b.String()
}

// rowKind is the kind of a table row to render.
type rowKind int

const (
	rowHeader rowKind = iota
	rowData
)

// borderRunes returns the border glyph set: rounded corners on UTF-8
// consoles, square ones elsewhere (GBK cannot display U+256D-2570, and the
// transcoding writer would replace them with '?').
func borderRunes() struct{ top, mid, bot [4]rune } {
	corners := [...]rune{'╭', '╮', '╰', '╯'}
	if ConsoleEncoding() != nil {
		corners = [...]rune{'┌', '┐', '└', '┘'}
	}
	return struct{ top, mid, bot [4]rune }{
		top: [4]rune{corners[0], '─', '┬', corners[1]},
		mid: [4]rune{'├', '─', '┼', '┤'},
		bot: [4]rune{corners[2], '─', '┴', corners[3]},
	}
}

// writeBorder writes one horizontal border line (top, separator, or bottom).
func writeBorder(b *strings.Builder, t *Theme, rs [4]rune, widths []int) {
	line := make([]rune, 0, sum(widths)+len(widths)+1)
	line = append(line, rs[0])
	for i, w := range widths {
		if i != 0 {
			line = append(line, rs[2])
		}
		line = append(line, []rune(strings.Repeat(string(rs[1]), w+2))...)
	}
	line = append(line, rs[3])
	b.WriteString(t.Border.Render(string(line)))
	b.WriteByte('\n')
}

// writeRow writes a single header or data row.
func writeRow(b *strings.Builder, t *Theme, rs struct{ top, mid, bot [4]rune }, row []string, widths []int, kind rowKind, zebra int) {
	style := t.Header
	if kind == rowData && zebra%2 == 1 {
		style = t.Zebra
	}
	b.WriteString(t.Border.Render("│"))
	for i, w := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		pad := w - cellWidth(cell)
		if pad < 0 {
			pad = 0
		}
		b.WriteByte(' ')
		b.WriteString(style.Render(cell))
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteByte(' ')
		b.WriteString(t.Border.Render("│"))
	}
	b.WriteByte('\n')
}

func sum(widths []int) int {
	n := 0
	for _, w := range widths {
		n += w
	}
	return n
}

// TablePaint configures the painting of streamed aligned tables: a
// per-table column kinds provider for value colors (nil paints no value
// colors), and the null marker tblfmt renders for SQL NULL cells ("" for
// blank), which gets the faint Null style instead of a value color.
type TablePaint struct {
	Kinds func() []Kind
	Null  string
}

// paintTable injects theme SGR sequences into a plain tblfmt "aligned"
// table rendered with unicode linestyle: the header row is bolded, border
// glyphs colored, data rows alternately zebra-shaded, and data cells
// foreground-colored by their column's Kind (see TablePaint).
//
// Only escape sequences are inserted — no characters are added, removed, or
// rewrapped — so tblfmt's alignment (computed with go-runewidth) is
// preserved exactly. Lines that are not table rows (footers, timings,
// blank lines between result sets) pass through untouched, as do lines
// without unicode column separators (expanded output, errors, notices).
func (t *Theme) paintTable(out string, paint TablePaint) string {
	if colorDisabled || !strings.ContainsAny(out, "│┌└├") {
		return out
	}
	p := &tablePainter{t: t, paint: paint}
	var b strings.Builder
	for line := range strings.Lines(out) {
		b.WriteString(p.line(strings.TrimSuffix(line, "\n")))
		b.WriteByte('\n')
	}
	return b.String()
}

// LineWriter returns an io.Writer that paints tblfmt table lines as they
// stream through (see paintTable), or w unchanged when color is disabled.
// The returned WriteCloser's Close flushes any buffered partial line.
func (t *Theme) LineWriter(w io.Writer, paint TablePaint) io.WriteCloser {
	if colorDisabled {
		if wc, ok := w.(io.WriteCloser); ok {
			return wc
		}
		return nopCloser{w}
	}
	return &paintWriter{p: &tablePainter{t: t, paint: paint}, w: w}
}

// nopCloser wraps w with a no-op Close.
type nopCloser struct {
	w io.Writer
}

// Write satisfies io.Writer.
func (c nopCloser) Write(p []byte) (int, error) {
	return c.w.Write(p)
}

// Close satisfies io.WriteCloser.
func (nopCloser) Close() error {
	return nil
}

// paintWriter paints complete lines written by tblfmt before passing them
// through to the underlying writer.
type paintWriter struct {
	p    *tablePainter
	w    io.Writer
	pend string // incomplete trailing line
}

// Write satisfies io.Writer.
func (pw *paintWriter) Write(p []byte) (int, error) {
	buf := pw.pend + string(p)
	pw.pend = ""
	for {
		i := strings.IndexByte(buf, '\n')
		if i < 0 {
			pw.pend = buf
			break
		}
		if _, err := io.WriteString(pw.w, pw.p.line(buf[:i])+"\n"); err != nil {
			return len(p), err
		}
		buf = buf[i+1:]
	}
	return len(p), nil
}

// Close flushes any buffered partial line.
func (pw *paintWriter) Close() error {
	if pw.pend != "" {
		_, err := io.WriteString(pw.w, pw.p.line(pw.pend)+"\n")
		pw.pend = ""
		return err
	}
	return nil
}

// tablePainter tracks tblfmt table structure across lines to paint rows.
// tblfmt's aligned format draws tables two ways: with border >= 2 the rows
// are framed by leading and trailing '│' and separators start with a corner
// glyph; with the default border = 1 there are no outer borders and the
// separator is a bare "───┼───" run. In both shapes the header row is the
// first line containing '│' and the separator follows it (a framed
// separator preceding it, when border >= 2).
type tablePainter struct {
	t *Theme
	// rowState tracks the tblfmt structure: 0 outside/between tables
	// (expecting a header), 1 inside a framed table after its opening
	// separator (expecting the header), 2 past the header (data rows).
	rowState int
	dataRow  int
	// paint holds the value-coloring configuration; paint.Kinds is
	// consulted once per table (on the header or first data row, whichever
	// comes first) because the painter runs in the same goroutine that
	// streams the result set, after tblfmt has advanced to it.
	paint       TablePaint
	kinds       []Kind
	kindsLoaded bool
}

// line paints a single output line.
func (p *tablePainter) line(line string) string {
	t := p.t
	switch {
	case isSeparatorLine(line):
		if p.rowState == 0 {
			if isBorderedSeparator(line) {
				// a framed separator (border >= 2) opens the table: the
				// header row follows it
				p.rowState = 1
			} else {
				// bare separator before any header row: an empty or
				// header-less table's border
				p.rowState = 2
			}
		}
		return t.Border.Render(line)
	case strings.ContainsRune(line, '│'):
		kind := rowDataKind
		if p.rowState == 0 || p.rowState == 1 {
			kind = headerKind
		}
		p.loadKinds()
		out := p.paintRow(line, kind)
		p.rowState = 2
		return out
	default:
		// footers, timings, blank lines between result sets, errors
		p.rowState = 0
		p.dataRow = 0
		p.kinds, p.kindsLoaded = nil, false
		return line
	}
}

// loadKinds fetches the column kinds for the table being entered, once.
func (p *tablePainter) loadKinds() {
	if p.kindsLoaded {
		return
	}
	p.kindsLoaded = true
	if p.paint.Kinds != nil {
		p.kinds = p.paint.Kinds()
	}
}

const (
	headerKind = iota
	rowDataKind
)

// paintRow colors one header or data row, splitting on the column separator
// glyph '│' (which cannot appear in data: data pipes are ASCII '|').
func (p *tablePainter) paintRow(line string, kind int) string {
	t := p.t
	segs := strings.Split(line, "│")
	// with border >= 2 rows are framed by a leading separator, so the first
	// split segment is empty and column indexes shift by one
	framed := strings.HasPrefix(line, "│")
	var b strings.Builder
	for i, seg := range segs {
		if i != 0 {
			b.WriteString(t.Border.Render("│"))
		}
		if kind == headerKind {
			b.WriteString(t.Header.Render(seg))
			continue
		}
		col := i
		if framed {
			col = i - 1
		}
		b.WriteString(p.dataCell(seg, col))
	}
	if kind == rowDataKind {
		p.dataRow++
	}
	return b.String()
}

// dataCell styles one data-row cell: the value color of the column's kind,
// plain for binary and unknown kinds, and the faint Null style for cells
// rendering SQL NULL — blank cells, or those equal to the configured null
// marker (an empty-string value is indistinguishable from NULL after
// rendering) — with the zebra background of odd rows wrapped around it.
func (p *tablePainter) dataCell(seg string, col int) string {
	t := p.t
	var inner string
	trimmed := strings.TrimSpace(seg)
	switch {
	case trimmed == "", p.paint.Null != "" && trimmed == p.paint.Null:
		inner = t.Null.Render(seg)
	default:
		if k := p.kindAt(col); k != KindUnknown && k != KindBinary {
			inner = t.Values[k].Render(seg)
		} else {
			inner = seg
		}
	}
	if p.dataRow%2 == 1 {
		return t.Zebra.Render(inner)
	}
	return inner
}

// kindAt returns the kind of column col, KindUnknown when out of range.
func (p *tablePainter) kindAt(col int) Kind {
	if col < 0 || col >= len(p.kinds) {
		return KindUnknown
	}
	return p.kinds[col]
}

// isBorderedSeparator reports whether a separator line carries corner or tee
// glyphs, i.e. frames a border >= 2 table rather than being a bare border=1
// column separator.
func isBorderedSeparator(line string) bool {
	return strings.ContainsAny(line, "┌┐└┘├┤┬┴┼")
}

// isSeparatorLine reports whether line is a horizontal border: only
// box-drawing glyphs (and, with border >= 2, the corner/tee glyphs), with
// at least one '─'. Border = 1 separators have no corner glyphs at all.
func isSeparatorLine(line string) bool {
	hasRun := false
	for _, r := range line {
		switch r {
		case '─':
			hasRun = true
		case '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼', ' ', '\t':
		default:
			return false
		}
	}
	return hasRun
}
