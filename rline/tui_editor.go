package rline

import (
	"strings"
	"unicode"

	"github.com/xo/usql/uitheme"
)

// editor is the TUI engine's line buffer: runes plus a cursor, with an
// emacs-style kill ring. It is engine-agnostic and fully unit-testable.
type editor struct {
	buf      []rune
	idx      int
	killRing [][]rune
	lastYank bool // previous command was a yank (yank-pop)
	lastKill bool // previous command was a kill (ring append)
}

// insertRunes inserts rs at the cursor.
func (e *editor) insertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	e.buf = append(e.buf, make([]rune, len(rs))...)
	copy(e.buf[e.idx+len(rs):], e.buf[e.idx:])
	copy(e.buf[e.idx:], rs)
	e.idx += len(rs)
	e.lastKill, e.lastYank = false, false
}

// insert inserts r at the cursor.
func (e *editor) insert(r rune) { e.insertRunes([]rune{r}) }

// line returns the current buffer contents.
func (e *editor) line() []rune { return e.buf }

// atEnd reports whether the cursor sits at the end of the line.
func (e *editor) atEnd() bool { return e.idx == len(e.buf) }

// empty reports whether the buffer is empty.
func (e *editor) empty() bool { return len(e.buf) == 0 }

// reset replaces the buffer contents.
func (e *editor) reset(rs []rune) {
	e.buf = append(e.buf[:0], rs...)
	e.idx = len(e.buf)
	e.lastKill, e.lastYank = false, false
}

// backspace deletes the rune before the cursor.
func (e *editor) backspace() bool {
	if e.idx == 0 {
		return false
	}
	e.buf = append(e.buf[:e.idx-1], e.buf[e.idx:]...)
	e.idx--
	e.lastKill, e.lastYank = false, false
	return true
}

// delete deletes the rune under the cursor.
func (e *editor) delete() bool {
	if e.idx == len(e.buf) {
		return false
	}
	e.buf = append(e.buf[:e.idx], e.buf[e.idx+1:]...)
	e.lastKill, e.lastYank = false, false
	return true
}

// moveStart moves to the start of the line.
func (e *editor) moveStart() { e.idx = 0; e.lastKill, e.lastYank = false, false }

// moveEnd moves to the end of the line.
func (e *editor) moveEnd() { e.idx = len(e.buf); e.lastKill, e.lastYank = false, false }

// moveLeft moves one rune left.
func (e *editor) moveLeft() bool {
	if e.idx == 0 {
		return false
	}
	e.idx--
	e.lastKill, e.lastYank = false, false
	return true
}

// moveRight moves one rune right.
func (e *editor) moveRight() bool {
	if e.idx == len(e.buf) {
		return false
	}
	e.idx++
	e.lastKill, e.lastYank = false, false
	return true
}

// wordStart returns the index of the start of the word before i.
func (e *editor) wordStart(i int) int {
	for i > 0 && unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	return i
}

// wordEnd returns the index of the end of the word after i.
func (e *editor) wordEnd(i int) int {
	for i < len(e.buf) && unicode.IsSpace(e.buf[i]) {
		i++
	}
	for i < len(e.buf) && !unicode.IsSpace(e.buf[i]) {
		i++
	}
	return i
}

// moveWordLeft moves to the start of the previous word.
func (e *editor) moveWordLeft() bool {
	i := e.wordStart(e.idx)
	if i == e.idx {
		return false
	}
	e.idx = i
	e.lastKill, e.lastYank = false, false
	return true
}

// moveWordRight moves to the end of the next word.
func (e *editor) moveWordRight() bool {
	i := e.wordEnd(e.idx)
	if i == e.idx {
		return false
	}
	e.idx = i
	e.lastKill, e.lastYank = false, false
	return true
}

// kill cuts the runes between the cursor and to, pushing them onto the kill
// ring. Consecutive kills append to the front entry.
func (e *editor) kill(to int) {
	lo, hi := e.idx, to
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo == hi {
		return
	}
	dead := append([]rune(nil), e.buf[lo:hi]...)
	e.buf = append(e.buf[:lo], e.buf[hi:]...)
	e.idx = lo
	if e.lastKill && len(e.killRing) > 0 {
		// consecutive kills accumulate in the order they were cut
		e.killRing[len(e.killRing)-1] = append(e.killRing[len(e.killRing)-1], dead...)
	} else {
		e.killRing = append(e.killRing, dead)
		if len(e.killRing) > 16 {
			e.killRing = e.killRing[1:]
		}
	}
	e.lastKill, e.lastYank = true, false
}

// killToEnd kills from the cursor to the end of the line (Ctrl-K).
func (e *editor) killToEnd() { e.kill(len(e.buf)) }

// killToStart kills from the start of the line to the cursor (Ctrl-U).
func (e *editor) killToStart() { e.kill(0) }

// killPrevWord kills the word before the cursor (Ctrl-W / Alt-Backspace).
func (e *editor) killPrevWord() { e.kill(e.wordStart(e.idx)) }

// killNextWord kills the word after the cursor (Alt-D).
func (e *editor) killNextWord() { e.kill(e.wordEnd(e.idx)) }

// yank pastes the front kill ring entry at the cursor (Ctrl-Y).
func (e *editor) yank() bool {
	if len(e.killRing) == 0 {
		return false
	}
	e.insertRunes(e.killRing[len(e.killRing)-1])
	e.lastYank, e.lastKill = true, false
	return true
}

// deletePrevWord deletes the word before the cursor without saving it to the
// kill ring (used by completion-driven edits).
func (e *editor) deleteRunes(n int) {
	if n <= 0 || e.idx == 0 {
		return
	}
	if n > e.idx {
		n = e.idx
	}
	e.buf = append(e.buf[:e.idx-n], e.buf[e.idx:]...)
	e.idx -= n
	e.lastKill, e.lastYank = false, false
}

// replaceBefore deletes n runes before the cursor and inserts rs — the
// completion accept primitive for both append (n=0) and replace semantics.
func (e *editor) replaceBefore(n int, rs []rune) {
	e.deleteRunes(n)
	e.insertRunes(rs)
}

// wordBefore returns the word immediately before the cursor.
func (e *editor) wordBefore() []rune {
	return e.buf[e.wordStart(e.idx):e.idx]
}

// String renders the buffer with a marker at the cursor; for tests.
func (e *editor) String() string {
	return string(e.buf[:e.idx]) + "|" + string(e.buf[e.idx:])
}

// displayWidth returns the display width of the buffer contents.
func (e *editor) displayWidth() int {
	return uitheme.CellWidth(string(e.buf))
}

// wordBreaks are the characters that delimit completion words, matching the
// completer's notion of a word.
const wordBreaks = "\t\n$><=;|&{() "

// wordAt returns the word boundaries around an arbitrary position, used by
// completion replace semantics.
func wordAt(line []rune, pos int) (start, end int) {
	isSpace := func(r rune) bool { return strings.ContainsRune(wordBreaks, r) }
	start = pos
	for start > 0 && !isSpace(line[start-1]) {
		start--
	}
	end = pos
	for end < len(line) && !isSpace(line[end]) {
		end++
	}
	return start, end
}
