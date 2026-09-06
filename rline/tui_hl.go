package rline

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xo/usql/uitheme"
)

// hlDebounce bounds how often the (comparatively expensive) syntax highlight
// filter runs while typing: the previous render stays up until the trailing
// tick recomputes it.
const hlDebounce = 80 * time.Millisecond

// hlMsg triggers the deferred re-highlight armed by refreshHighlight.
type hlMsg struct{}

// ansiRE matches ANSI SGR escape sequences.
var ansiRE = regexp.MustCompile(`\x1b[[0-9]+([:;][0-9]+)*m`)

// highlight returns the display form of the current line: the engine's
// output filter (syntax highlighting) applied to the buffer, matching
// readline's Config.Output semantics — a display transform that does not
// change the logical content. It is cached by buffer content and debounced:
// while typing inside the debounce window, the previous render stays up
// until the trailing tick recomputes it.
func (m *lineModel) highlight(plain string) string {
	if m.t.outFn == nil {
		return plain
	}
	if plain == m.hlIn {
		return m.hlOut
	}
	if !m.hlPend && time.Since(m.hlAt) >= hlDebounce {
		m.computeHL()
		return m.hlOut
	}
	return m.hlOut // a stale (at most hlDebounce old) render
}

// computeHL runs the output filter over the current buffer.
func (m *lineModel) computeHL() {
	m.hlIn = string(m.ed.buf)
	m.hlAt = time.Now()
	m.hlOut = m.t.outFn(m.hlIn)
}

// refreshHighlight arms the trailing re-highlight when the buffer changed
// inside the debounce window.
func (m *lineModel) refreshHighlight() tea.Cmd {
	if m.t.outFn == nil || m.hlPend || string(m.ed.buf) == m.hlIn {
		return nil
	}
	if time.Since(m.hlAt) < hlDebounce {
		m.hlPend = true
		return tea.Tick(hlDebounce, func(time.Time) tea.Msg { return hlMsg{} })
	}
	return nil
}

// lineStyled renders buf with the block cursor overlaid at rune index idx on
// styled text s (the debounced highlight). When s does not carry the same
// printable content as buf — a stale highlight after a rapid edit, or tab
// expansion by the filter — it falls back to the plain buffer.
func (m *lineModel) lineStyled(s string, buf []rune, idx int) string {
	if styledRuneLen(s) != len(buf) {
		s = string(buf)
	}
	before, at, after := splitStyled(s, idx)
	// re-emit the SGR state the cursor cell's reset interrupted, so the
	// tail keeps its styling
	return before + uitheme.Current().Selected.Render(at) + lastSGR(before) + after
}

// splitStyled splits s (possibly containing ANSI escape sequences) at
// printable rune index i: the text before it, the rune itself, and the
// remainder. Escape sequences stay attached to the printable rune that
// follows them.
func splitStyled(s string, i int) (before, at, after string) {
	var b, pending strings.Builder
	n, rs := 0, []rune(s)
	for j := 0; j < len(rs); j++ {
		if rs[j] == '\x1b' {
			end := j + 1
			if end < len(rs) && rs[end] == '[' {
				end++ // consume '['
				for end < len(rs) && (rs[end] < '@' || rs[end] > '~') {
					end++
				}
				if end < len(rs) {
					end++ // final byte
				}
			}
			pending.WriteString(string(rs[j:end]))
			j = end - 1
			continue
		}
		if n == i {
			return b.String(), string(rs[j]), pending.String() + string(rs[j+1:])
		}
		b.WriteString(pending.String())
		pending.Reset()
		b.WriteRune(rs[j])
		n++
	}
	return b.String() + pending.String(), "", ""
}

// styledRuneLen counts printable runes in s, skipping ANSI escape sequences.
func styledRuneLen(s string) int {
	n := 0
	for j := 0; j < len(s); {
		if s[j] == '\x1b' {
			j++
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < '@' || s[j] > '~') {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			continue
		}
		_, size := utf8.DecodeRuneInString(s[j:])
		n++
		j += size
	}
	return n
}

// lastSGR returns the SGR sequences in effect at the end of s (those since
// the last reset), for re-emitting after an interrupting style.
func lastSGR(s string) string {
	if i := strings.LastIndex(s, "\x1b[0m"); i != -1 {
		s = s[i+4:]
	}
	return strings.Join(ansiRE.FindAllString(s, -1), "")
}
