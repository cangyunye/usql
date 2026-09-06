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

// CellWidth returns the display width of s on this console.
//
// It is the single width-measurement entry point for interactive output:
// ANSI escape sequences are ignored, characters the console encoding cannot
// represent count as the single-column '?' substitution they will render as
// (charset.ConsolePreview), and width is measured with go-runewidth — the
// same library tblfmt aligns with, including the EastAsianWidth locale fix
// charset applies for GB18030 (and runewidth's own gbk/gb2312 handling), so
// ambiguous-width characters measure identically in both.
func CellWidth(s string) int {
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
// widths are computed with CellWidth, so mixed CJK/ASCII content stays
// aligned on UTF-8 and GBK-family consoles alike. Rounded corners are
// degraded to square ones when the console encoding cannot display them.
func (t *Theme) Table(headers []string, rows [][]string) string {
	n := len(headers)
	if n == 0 {
		return ""
	}
	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = CellWidth(h)
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if w := CellWidth(row[i]); w > widths[i] {
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
		pad := w - CellWidth(cell)
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

// PaintTable injects theme SGR sequences into a plain tblfmt "aligned"
// table rendered with unicode linestyle: the header row is bolded, border
// glyphs colored, and data rows alternately zebra-shaded.
//
// Only escape sequences are inserted — no characters are added, removed, or
// rewrapped — so tblfmt's alignment (computed with go-runewidth) is
// preserved exactly. Lines that are not table rows (footers, timings,
// blank lines between result sets) pass through untouched, as do lines
// without unicode column separators (expanded output, errors, notices).
func (t *Theme) PaintTable(out string) string {
	if colorDisabled || !strings.ContainsAny(out, "│┌└├") {
		return out
	}
	p := &tablePainter{t: t}
	var b strings.Builder
	for line := range strings.Lines(out) {
		b.WriteString(p.line(strings.TrimSuffix(line, "\n")))
		b.WriteByte('\n')
	}
	return b.String()
}

// LineWriter returns an io.Writer that paints tblfmt table lines as they
// stream through (see PaintTable), or w unchanged when color is disabled.
// The returned WriteCloser's Close flushes any buffered partial line.
func (t *Theme) LineWriter(w io.Writer) io.WriteCloser {
	if colorDisabled {
		if wc, ok := w.(io.WriteCloser); ok {
			return wc
		}
		return nopCloser{w}
	}
	return &paintWriter{p: &tablePainter{t: t}, w: w}
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
// first line containing '│' and the separator follows it.
type tablePainter struct {
	t *Theme
	// rowState tracks the tblfmt structure: 0 outside/between tables
	// (expecting a header), 1 header seen (expecting data), 2 in data rows.
	rowState int
	dataRow  int
}

// line paints a single output line.
func (p *tablePainter) line(line string) string {
	t := p.t
	switch {
	case isSeparatorLine(line):
		if p.rowState == 0 {
			// separator before any header row: an empty table's border
			p.rowState = 2
		}
		return t.Border.Render(line)
	case strings.ContainsRune(line, '│'):
		kind := rowDataKind
		if p.rowState == 0 {
			kind = headerKind
		}
		out := p.paintRow(line, kind)
		p.rowState = 2
		return out
	default:
		// footers, timings, blank lines between result sets, errors
		p.rowState = 0
		p.dataRow = 0
		return line
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
	var b strings.Builder
	for i, seg := range segs {
		if i != 0 {
			b.WriteString(t.Border.Render("│"))
		}
		switch {
		case kind == headerKind:
			b.WriteString(t.Header.Render(seg))
		case p.dataRow%2 == 1:
			b.WriteString(t.Zebra.Render(seg))
		default:
			b.WriteString(seg)
		}
	}
	if kind == rowDataKind {
		p.dataRow++
	}
	return b.String()
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
