package rline

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xo/usql/uitheme"
)

// menuHeight is the maximum number of candidates shown at once.
const menuHeight = 10

// kickMsg is delivered when an asynchronous completion result lands.
type kickMsg struct{}

// finalizeMsg carries the outcome of a line read out of the model.
type finalizeMsg struct {
	line     []rune
	err      error // ErrInterrupt or io.EOF; nil on Enter
	finalize string
}

// lineModel is the per-Next() bubbletea model: a single input line with a
// vertical (IME-style) candidate menu, a dim ghost suggestion, and Ctrl-R
// incremental history search.
type lineModel struct {
	// engine back-references
	t    *tuiRline
	prom string // prompt for this read

	// editor state
	ed      editor
	histPos int  // history position for up/down navigation
	hasHist bool // histPos/draft are meaningful
	draft   []rune

	// candidate menu
	menu    [][]rune // candidate suffixes/full words
	menuLen int      // replace length for replace-style candidates
	menuRep bool     // replace semantics
	menuSel int
	menuTop int // window offset
	menuMax int // window height (terminal height bounded)

	// ghost suggestion (dim suffix shown at end of line)
	ghost []rune

	// incremental search
	searching bool
	query     string
	preSearch []rune // buffer to restore on Ctrl-G

	// result plumbing
	done        bool
	interrupted bool // ^C
	waiting     bool // an async completion is pending (kick armed)
}

// newLineModel builds the model for one read.
func newLineModel(t *tuiRline, prompt string) *lineModel {
	return &lineModel{
		t:       t,
		prom:    prompt,
		histPos: len(t.hist.lines),
		menuMax: menuHeight,
	}
}

// Init starts the completion kick listener.
func (m *lineModel) Init() tea.Cmd {
	return m.waitKick()
}

func (m *lineModel) waitKick() tea.Cmd {
	return func() tea.Msg {
		<-m.t.kickCh
		return kickMsg{}
	}
}

// Update dispatches a message.
func (m *lineModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if h := msg.Height - 2; h < m.menuMax {
			if h < 1 {
				h = 1
			}
			m.menuMax = h
		}
		return m, nil

	case kickMsg:
		if !m.done {
			m.refreshCompletion()
			m.waiting = false
		}
		return m, m.waitKick()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey processes one keystroke.
func (m *lineModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	switch key := msg.String(); key {
	case "ctrl+c":
		m.done = true
		m.interrupted = true
		m.closeMenu()
		return m, tea.Quit

	case "ctrl+z":
		// readline's engine filters Ctrl-Z; do the same

	case "ctrl+d":
		if m.ed.empty() {
			m.done = true
			return m, tea.Quit
		}
		m.ed.delete()
		m.afterEdit(false)

	case "enter", "ctrl+j":
		// the menu, not the line, is being submitted while it is open
		if m.searching {
			return m.handleSearchKey(msg, key)
		}
		if m.menu != nil {
			return m.handleMenuKey(msg, key)
		}
		m.done = true
		m.ghost = nil
		return m, tea.Quit

	default:
		switch {
		case m.searching:
			return m.handleSearchKey(msg, key)
		case m.menu != nil:
			return m.handleMenuKey(msg, key)
		default:
			return m.handleEditKey(msg, key)
		}
	}
	return m, nil
}

// handleSearchKey processes keys during Ctrl-R search.
func (m *lineModel) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+r":
		m.doSearch()
	case "ctrl+g", "esc":
		m.searching = false
		m.ed.reset(m.preSearch)
	case "backspace":
		if m.query != "" {
			m.query = m.query[:len(m.query)-1]
			m.doSearch()
		}
	case "enter", "ctrl+j":
		m.searching = false
	default:
		if rs := msg.Runes; len(rs) > 0 && !msg.Alt {
			m.query += string(rs)
			m.doSearch()
		}
	}
	return m, nil
}

// doSearch scans history backwards for the query.
func (m *lineModel) doSearch() {
	pos := m.histPos
	if !m.hasHist {
		pos = len(m.t.hist.lines)
	}
	if i, line, ok := m.t.hist.search(m.query, pos+1); ok {
		m.histPos, m.hasHist = i, true
		m.ed.reset([]rune(line))
	}
}

// handleMenuKey processes keys while the candidate menu is open.
func (m *lineModel) handleMenuKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch {
	case key == "up" || key == "ctrl+p":
		m.selectCandidate(-1)
	case key == "down" || key == "ctrl+n":
		m.selectCandidate(1)
	case key == "pgup":
		m.menuSel -= m.menuMax
		m.clampMenuSel()
	case key == "pgdown":
		m.menuSel += m.menuMax
		m.clampMenuSel()
	case key == "tab" || key == "enter" || key == "ctrl+j":
		m.acceptCandidate(m.menuSel)
	case key == "esc" || key == "ctrl+g":
		m.closeMenu()
		m.ghost = nil
	case len(key) == 5 && strings.HasPrefix(key, "alt+") && key[4] >= '1' && key[4] <= '9':
		if i, _ := strconv.Atoi(key[4:]); i-1 < len(m.menu) {
			m.acceptCandidate(i - 1)
		}
	case key == "left" || key == "right" || key == "home" || key == "end" ||
		key == "ctrl+left" || key == "ctrl+right" || key == "ctrl+a" || key == "ctrl+e":
		m.closeMenu()
		return m.handleEditKey(msg, key)
	case key == "ctrl+c" || key == "ctrl+d":
		return m.handleKey(msg)
	default:
		// keep the menu open and refilter while typing (IME behavior)
		m.closeMenu()
		mod, cmd := m.handleEditKey(msg, key)
		if !m.done && m.menu == nil {
			m.openMenu()
		}
		return mod, cmd
	}
	return m, nil
}

// handleEditKey processes keys in normal editing mode.
func (m *lineModel) handleEditKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	ed := &m.ed
	switch key {
	case "enter", "ctrl+j":
		m.done = true
		m.ghost = nil
	case "esc":
		// clear the ghost, no-op otherwise
		m.ghost = nil
	case "backspace":
		ed.backspace()
		m.afterEdit(false)
	case "delete":
		ed.delete()
		m.afterEdit(false)
	case "left", "ctrl+b":
		ed.moveLeft()
		m.ghost = nil
	case "right":
		if ed.atEnd() && len(m.ghost) > 0 {
			// fish-style: right arrow accepts the whole suggestion
			ed.insertRunes(m.ghost)
			m.ghost = nil
			m.afterEdit(false)
		} else {
			ed.moveRight()
			m.ghost = nil
		}
	case "ctrl+right", "alt+f":
		if ed.atEnd() && len(m.ghost) > 0 {
			// accept one word of the suggestion
			n := len(m.ghost)
			for i, r := range m.ghost {
				if unicodeIsSpace(r) && i > 0 {
					n = i + 1
					break
				}
			}
			ed.insertRunes(m.ghost[:n])
			m.afterEdit(false)
		} else {
			ed.moveWordRight()
			m.ghost = nil
		}
	case "alt+b", "ctrl+left":
		ed.moveWordLeft()
		m.ghost = nil
	case "ctrl+a", "home":
		ed.moveStart()
		m.ghost = nil
	case "ctrl+e", "end":
		ed.moveEnd()
		m.afterEdit(false)
	case "ctrl+k":
		ed.killToEnd()
		m.afterEdit(false)
	case "ctrl+u":
		ed.killToStart()
		m.afterEdit(false)
	case "ctrl+w", "alt+backspace":
		ed.killPrevWord()
		m.afterEdit(false)
	case "alt+d":
		ed.killNextWord()
		m.afterEdit(false)
	case "ctrl+y":
		ed.yank()
		m.afterEdit(false)
	case "tab":
		m.openMenu()
		m.afterEdit(true)
	case "up", "ctrl+p":
		m.historyPrev()
	case "down", "ctrl+n":
		m.historyNext()
	case "ctrl+r":
		m.searching = true
		m.query = ""
		m.preSearch = append([]rune(nil), ed.buf...)
	default:
		if rs := msg.Runes; len(rs) > 0 {
			ed.insertRunes(rs)
			m.afterEdit(false)
		}
	}
	return m, nil
}

func unicodeIsSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// historyPrev moves to the previous history entry, saving the draft.
func (m *lineModel) historyPrev() {
	if !m.hasHist {
		m.draft = append([]rune(nil), m.ed.buf...)
	}
	if pos, line, ok := m.t.hist.prev(m.histPos, string(m.draft)); ok {
		m.histPos, m.hasHist = pos, true
		m.ed.reset([]rune(line))
		m.ed.moveEnd()
	}
	m.ghost = nil
}

// historyNext moves to the next history entry, restoring the draft at the
// end of the list.
func (m *lineModel) historyNext() {
	if !m.hasHist {
		return
	}
	if pos, line, ok := m.t.hist.next(m.histPos, string(m.draft)); ok {
		m.histPos, m.hasHist = pos, true
		m.ed.reset([]rune(line))
		m.ed.moveEnd()
	} else {
		m.hasHist = false
		m.ed.reset(m.draft)
	}
	m.ghost = nil
}

// afterEdit recomputes ghost text and live candidates after a buffer edit.
// When keepMenu is set (Tab completion), an open menu is refreshed instead
// of closed.
func (m *lineModel) afterEdit(keepMenu bool) {
	if m.menu != nil {
		m.refreshCompletion()
		return
	}
	if !keepMenu {
		m.ghost = nil
	}
	if m.ed.atEnd() && !m.ed.empty() && m.t.hist != nil {
		m.ghost = m.t.hist.suggest(m.ed.buf)
	}
	if m.t.comp != nil && m.ed.atEnd() && !m.ed.empty() && len(m.ghost) == 0 {
		// metadata-sourced best match via the live fast path; the ghost is
		// filled in when the (possibly asynchronous) result lands
		m.requestCandidates()
	}
}

// requestCandidates asks the completer for candidates at the cursor,
// preferring the non-blocking live path.
func (m *lineModel) requestCandidates() ([][]rune, int, bool, bool) {
	if m.t.comp == nil {
		return nil, 0, false, false
	}
	if lc, ok := m.t.comp.(LiveCompleter); ok {
		cands, length, replace := lc.DoLive(m.ed.buf, m.ed.idx)
		if cands == nil {
			m.waiting = true
		}
		return cands, length, replace, true
	}
	cands, length := m.t.comp.Do(m.ed.buf, m.ed.idx)
	return cands, length, false, true
}

// refreshCompletion updates an open menu or the ghost from the completer.
func (m *lineModel) refreshCompletion() {
	cands, length, replace, ok := m.requestCandidates()
	if !ok || len(cands) == 0 {
		m.closeMenu()
		m.ghost = nil
		return
	}
	if m.menu != nil {
		m.menu, m.menuLen, m.menuRep = cands, length, replace
		m.clampMenuSel()
		return
	}
	// ghost: the best (fuzzy-ranked) candidate at end of line
	if m.ed.atEnd() && !m.ed.empty() {
		if replace {
			start, _ := wordAt(m.ed.buf, m.ed.idx)
			typed := m.ed.buf[start:m.ed.idx]
			if c := cands[0]; len(c) > len(typed) {
				m.ghost = c[len(typed):]
			}
		} else {
			m.ghost = cands[0]
		}
	}
}

// openMenu requests candidates and opens the vertical menu.
func (m *lineModel) openMenu() {
	cands, length, replace, ok := m.requestCandidates()
	if !ok || len(cands) == 0 {
		m.closeMenu()
		return
	}
	m.menu, m.menuLen, m.menuRep = cands, length, replace
	m.menuSel, m.menuTop = -1, 0
}

// closeMenu closes the candidate menu.
func (m *lineModel) closeMenu() {
	m.menu, m.menuSel, m.menuTop = nil, -1, 0
	m.menuLen, m.menuRep = 0, false
}

// selectCandidate moves the menu selection by delta, wrapping.
func (m *lineModel) selectCandidate(delta int) {
	if m.menuSel < 0 {
		m.menuSel = 0
	} else {
		m.menuSel = (m.menuSel + delta + len(m.menu)) % len(m.menu)
	}
	m.scrollMenu()
}

// clampMenuSel keeps the selection inside the list after a refilter.
func (m *lineModel) clampMenuSel() {
	if m.menuSel >= len(m.menu) {
		m.menuSel = len(m.menu) - 1
	}
	if m.menuSel < 0 {
		m.menuSel = 0
	}
	m.scrollMenu()
}

// scrollMenu keeps the selection inside the visible window.
func (m *lineModel) scrollMenu() {
	h := m.menuMax
	if h > menuHeight {
		h = menuHeight
	}
	switch {
	case m.menuSel < m.menuTop:
		m.menuTop = m.menuSel
	case m.menuSel >= m.menuTop+h:
		m.menuTop = m.menuSel - h + 1
	}
}

// acceptCandidate inserts the chosen candidate into the buffer and closes
// the menu. Enter never executes the line (IME semantics).
func (m *lineModel) acceptCandidate(i int) {
	if i < 0 || i >= len(m.menu) {
		m.closeMenu()
		return
	}
	cand := m.menu[i]
	n := 0
	if m.menuRep {
		n = m.menuLen
	} else {
		// append semantics: candidates are suffixes after the word being
		// completed; replace what's between the word start and the cursor
		_, end := wordAt(m.ed.buf, m.ed.idx)
		n = m.ed.idx - end
	}
	m.ed.replaceBefore(n, cand)
	m.closeMenu()
	m.ghost = nil
	m.afterEdit(false)
}

// View renders the input line, candidate menu, and search prompt.
func (m *lineModel) View() string {
	var b strings.Builder
	if m.searching {
		b.WriteString(m.searchView())
	} else {
		b.WriteString(m.lineView())
	}
	if m.menu != nil {
		b.WriteString(m.menuView())
	}
	if m.interrupted {
		b.WriteString("^C")
	}
	if m.done {
		b.WriteString("\n")
	}
	return b.String()
}

// lineView renders the prompt, buffer, cursor, and ghost.
func (m *lineModel) lineView() string {
	var b strings.Builder
	b.WriteString(m.prom)
	buf := m.ed.buf
	idx := m.ed.idx
	t := uitheme.Current()
	// when finalizing, render without the cursor block
	if m.done {
		b.WriteString(string(buf))
		return b.String()
	}
	before := string(buf[:idx])
	switch {
	case idx < len(buf):
		b.WriteString(before)
		b.WriteString(t.Selected.Render(string(buf[idx])))
		b.WriteString(string(buf[idx+1:]))
	case len(m.ghost) > 0 && uitheme.Enabled():
		// block cursor over the first ghost rune, rest dim
		b.WriteString(before)
		b.WriteString(t.Selected.Render(string(m.ghost[0])))
		b.WriteString(t.Dim.Render(string(m.ghost[1:])))
	default:
		b.WriteString(before)
		b.WriteString(t.Selected.Render(" "))
	}
	return b.String()
}

// searchView renders the incremental search prompt.
func (m *lineModel) searchView() string {
	t := uitheme.Current()
	mark := t.Warn.Render("(reverse-i-search)`")
	markEnd := t.Warn.Render("': ")
	q := m.query
	if m.query == "" {
		mark = t.Dim.Render("(failed reverse-i-search)`")
	}
	return mark + q + markEnd + string(m.ed.buf)
}

// menuView renders the vertical candidate menu below the input line.
func (m *lineModel) menuView() string {
	t := uitheme.Current()
	h := m.menuMax
	if h > menuHeight {
		h = menuHeight
	}
	if h > len(m.menu) {
		h = len(m.menu)
	}
	end := m.menuTop + h
	if end > len(m.menu) {
		end = len(m.menu)
	}
	// the typed prefix shared with every candidate, for full-word display
	var typed []rune
	if !m.menuRep {
		start, end2 := wordAt(m.ed.buf, m.ed.idx)
		typed = m.ed.buf[start:end2]
	}
	var b strings.Builder
	for i := m.menuTop; i < end; i++ {
		full := append(append([]rune(nil), typed...), m.menu[i]...)
		text := string(full)
		if i < 9 && m.menuMax >= 9 {
			text = strconv.Itoa(i+1) + " " + text
		}
		mark := "▸ "
		if uitheme.ConsoleEncoding() != nil {
			// '▸' does not exist in GBK-family encodings and would be
			// transcoded to '?', shifting the menu
			mark = "> "
		}
		if i == m.menuSel {
			b.WriteString(t.Selected.Render(mark + text))
		} else {
			b.WriteString("  " + text)
		}
		b.WriteString("\n")
	}
	return b.String()
}
