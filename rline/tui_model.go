package rline

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/xo/usql/uitheme"
)

// menuHeight is the maximum number of candidates shown at once;
// USQL_COMPLETION_ROWS overrides it.
var menuHeight = completionRows()

// Completion debounce policy: the typing-time completion fires after a
// compIdle pause, but never waits longer than compMax since the word's first
// keystroke (so continuous typing still gets suggestions), and a backspace
// that shortened the word holds it off for compBack — a shortened word means
// the candidate set may need re-searching, which should not flash mid-mash.
// When the statement-context memo can serve the request, the result is
// instant anyway and only the idle delay applies. compIdle defaults to
// 150ms and is overridden by USQL_COMPLETION_DELAY (milliseconds); compMax
// scales with it.
var (
	compIdle = completionDelay()
	compMax  = 10 * compIdle
	compBack = 1 * time.Second
)

// menuRefreshCool coalesces delete-driven menu refreshes: a held-backspace
// would otherwise refilter the candidate set once per key event, churning
// the rendered frame. One refresh per cooldown window, with a trailing tick
// catching the final state, is enough.
const menuRefreshCool = 80 * time.Millisecond

// unknownRowMenuMax caps the menu height when the cursor row is unknown
// (a failed DSR probe): if the cursor actually sits at the screen bottom, a
// full-height menu opening below would scroll that much history at once.
const unknownRowMenuMax = 4

// badgeMaxWidth caps the candidate text width below which kind badges are
// right-aligned; wider candidates push the badge off-screen, so it is
// dropped instead of wrapping the menu row.
const badgeMaxWidth = 60

// detailMaxWidth caps a candidate's dim detail (a function signature, a
// column's data type); longer details are truncated.
const detailMaxWidth = 36

// truncateDetail caps s to n runes, marking a cut with two ASCII dots ('…'
// does not survive the GBK-family transcode, like '▸' above).
func truncateDetail(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n < 2 {
		n = 2
	}
	return string(rs[:n-2]) + ".."
}

// kickMsg is delivered when an asynchronous completion result lands.
type kickMsg struct{}

// compMsg is delivered when the typing pause elapses; the trailing
// completion is computed then, unless typing continued in the meantime.
type compMsg struct{}

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
	menu    []Cand // candidate suffixes/full words with kinds
	menuLen int    // replace length for replace-style candidates
	menuRep bool   // replace semantics
	// menuExplicit reports that the menu was opened by an explicit Tab (vs
	// popped open by the typing-time completion); only an explicit menu
	// accepts on Enter — a popped-open one must never hijack execution
	menuExplicit bool
	menuSel      int
	menuTop      int // window offset
	menuMax      int // window height (terminal height bounded)
	topRow       int // terminal row the read started on (-1 unknown)

	// menu refresh cooldown: while set, delete-driven refreshes are held
	// off and re-run by a trailing tick (menuPend)
	menuCoolAt time.Time
	menuPend   bool

	// ghost suggestion (dim suffix shown at end of line)
	ghost []rune

	// incremental search (Ctrl-R reverse, Ctrl-S forward)
	searching  bool
	searchFwd  bool   // search direction
	searchFail bool   // the last query found no match
	searchFrom int    // history position the search started from
	query      string // search query
	preSearch  []rune // buffer to restore on Ctrl-G
	preHist    int    // history position to restore on Ctrl-G
	preHas     bool   // history position validity to restore on Ctrl-G

	// emacs numeric prefix argument (Alt-<digits>); consumed by the next
	// command that understands it
	arg int

	// output filter (syntax highlight) cache; see highlight in tui_hl.go
	hlIn   string    // buffer content the cache was built from
	hlOut  string    // filtered (highlighted) form of hlIn
	hlAt   time.Time // when hlIn/hlOut were computed
	hlPend bool      // a trailing re-highlight is scheduled

	// typing-time completion debounce: the ghost/metadata completion wants
	// to run (compDue) no earlier than compDeadline; while keys keep
	// arriving the deadline slides forward and compPend holds one armed
	// tick that re-checks it. compFirst is when the word under the cursor
	// started changing (compMax cap), compBackAt the last deletion
	// (compBack cooldown), compWordStart detects word switches.
	compDue       bool
	compDeadline  time.Time
	compPend      bool
	compFirst     time.Time
	compBackAt    time.Time
	compWordStart int

	// result plumbing
	done        bool
	interrupted bool // ^C
	waiting     bool // an async completion is pending (kick armed)
}

// newLineModel builds the model for one read. topRow is the terminal row
// the read starts on (-1 when unknown).
func newLineModel(t *tuiRline, prompt string, topRow int) *lineModel {
	return &lineModel{
		t:             t,
		prom:          prompt,
		histPos:       len(t.hist.lines),
		menuMax:       menuHeight,
		menuSel:       -1,
		menuTop:       0,
		topRow:        topRow,
		compWordStart: -1,
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
		m.resize(msg.Height)
		return m, nil

	case kickMsg:
		if !m.done {
			m.refreshCompletion()
			m.waiting = false
		}
		return m, m.waitKick()

	case compMsg:
		// the typing pause elapsed; typing meanwhile slid the deadline, in
		// which case the tick re-arms instead of completing now
		m.compPend = false
		if !m.done && m.menuPend {
			// trailing menu refresh: the tick fired at the cooldown point
			m.menuPend = false
			if m.menu != nil {
				m.refreshCompletion()
			}
		}
		if !m.done && m.compDue && time.Until(m.compDeadline) <= 0 {
			m.computeCompletion()
		}
		return m, m.scheduleCompletion()

	case hlMsg:
		// trailing re-highlight after the debounce window
		m.hlPend = false
		if m.t.outFn != nil && string(m.ed.buf) != m.hlIn {
			m.computeHL()
		}
		return m, nil

	case tea.KeyMsg:
		mod, cmd := m.handleKey(msg)
		return mod, tea.Batch(cmd, m.refreshHighlight(), m.scheduleCompletion())
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
		m.afterEdit(false, true)

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

// handleSearchKey processes keys during Ctrl-R / Ctrl-S search.
func (m *lineModel) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+r":
		m.searchFwd = false
		m.doSearch(false)
	case "ctrl+s":
		m.searchFwd = true
		m.doSearch(false)
	case "ctrl+g", "esc":
		m.searching, m.searchFail = false, false
		m.histPos, m.hasHist = m.preHist, m.preHas
		m.ed.reset(m.preSearch)
	case "backspace":
		if m.query != "" {
			if _, size := utf8.DecodeLastRuneInString(m.query); size > 0 {
				m.query = m.query[:len(m.query)-size]
			}
			m.doSearch(true)
		}
	case "enter", "ctrl+j":
		m.searching, m.searchFail = false, false
	default:
		if rs := msg.Runes; len(rs) > 0 && !msg.Alt {
			m.query += string(rs)
			m.doSearch(true)
		}
	}
	return m, nil
}

// startSearch enters search mode in the given direction.
func (m *lineModel) startSearch(fwd bool) {
	m.searching, m.searchFwd = true, fwd
	m.query, m.searchFail = "", false
	m.preSearch = append([]rune(nil), m.ed.buf...)
	m.preHist, m.preHas = m.histPos, m.hasHist
	m.searchFrom = m.histPos
	if !m.hasHist {
		m.searchFrom = len(m.t.hist.lines)
	}
}

// doSearch finds the next match for the query in the search direction. A
// query edit restarts the scan from where search mode began; a direction
// key (Ctrl-R/Ctrl-S) steps past the current match.
func (m *lineModel) doSearch(edit bool) {
	start := m.searchFrom
	if !edit && m.hasHist {
		start = m.histPos
	}
	switch {
	case m.searchFwd && edit:
		start-- // fwdSearch scans start+1 upwards; an edit restarts at the origin itself
	case edit:
		start++ // search scans start-1 downwards; an edit restarts at the origin itself
	}
	var i int
	var line string
	var ok bool
	if m.searchFwd {
		i, line, ok = m.t.hist.fwdSearch(m.query, start)
	} else {
		i, line, ok = m.t.hist.search(m.query, start)
	}
	m.searchFail = !ok && m.query != ""
	if ok {
		m.histPos, m.hasHist = i, true
		m.ed.reset([]rune(line))
	}
}

// handleMenuKey processes keys while the candidate menu is open. Editing
// keys keep the menu open and refilter it in place — closing and reopening
// per keystroke would make the frame height oscillate, which scrolls the
// terminal history.
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
	case key == "tab":
		m.acceptCandidate(m.menuSel)
	case key == "enter" || key == "ctrl+j":
		if !m.menuExplicit {
			// the menu popped open from typing: Enter runs the line
			m.closeMenu()
			m.ghost = nil
			return m.handleEditKey(msg, key)
		}
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
	case key == "backspace":
		n := m.arg
		m.arg = 0
		if n <= 0 {
			n = 1
		}
		for k := 0; k < n && m.ed.backspace(); k++ {
		}
		// held-backspace refilters once per cooldown window; a trailing
		// tick catches the final state
		now := time.Now()
		if now.Before(m.menuCoolAt) {
			m.menuPend = true
			return m, m.scheduleMenuRefresh()
		}
		m.menuCoolAt = now.Add(menuRefreshCool)
		m.refreshCompletion()
	case key == "ctrl+c" || key == "ctrl+d":
		return m.handleKey(msg)
	default:
		if rs := msg.Runes; len(rs) > 0 && !msg.Alt {
			// keep the menu open and refilter in place (IME behavior)
			m.ed.insertRunes(rs)
			m.refreshCompletion()
			return m, nil
		}
		// other editing keys (kill, transpose, alt-digits, ...): edit, then
		// reopen a fresh menu
		m.closeMenu()
		mod, cmd := m.handleEditKey(msg, key)
		if !m.done && m.menu == nil {
			m.openMenu()
		}
		return mod, cmd
	}
	return m, nil
}

// handleEditKey processes keys in normal editing mode. The emacs numeric
// prefix argument (m.arg) is consumed by the next key that understands it;
// any other key discards it.
func (m *lineModel) handleEditKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	ed := &m.ed
	n := m.arg
	m.arg = 0
	if n <= 0 {
		n = 1
	}
	switch key {
	case "alt+0", "alt+1", "alt+2", "alt+3", "alt+4",
		"alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		// numeric prefix argument (ESC-<digits>); consumed by the next
		// command
		if m.arg < 1000 {
			m.arg = m.arg*10 + int(key[4]-'0')
		}
		return m, nil
	case "enter", "ctrl+j":
		m.done = true
		m.ghost = nil
	case "esc":
		// clear the ghost, no-op otherwise; also cancel a pending debounced
		// completion, so the dismissed suggestion does not pop back
		m.ghost = nil
		m.compDue = false
	case "backspace":
		for k := 0; k < n && ed.backspace(); k++ {
		}
		m.afterEdit(false, true)
	case "delete":
		for k := 0; k < n && ed.delete(); k++ {
		}
		m.afterEdit(false, true)
	case "left", "ctrl+b":
		for k := 0; k < n && ed.moveLeft(); k++ {
		}
		m.ghost = nil
	case "right":
		if ed.atEnd() && len(m.ghost) > 0 {
			// fish-style: right arrow accepts the whole suggestion
			ed.insertRunes(m.ghost)
			m.ghost = nil
			m.afterEdit(false, false)
		} else {
			for k := 0; k < n && ed.moveRight(); k++ {
			}
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
			m.afterEdit(false, false)
		} else {
			for k := 0; k < n && ed.moveWordRight(); k++ {
			}
			m.ghost = nil
		}
	case "alt+b", "ctrl+left":
		for k := 0; k < n && ed.moveWordLeft(); k++ {
		}
		m.ghost = nil
	case "ctrl+a", "home":
		ed.moveStart()
		m.ghost = nil
	case "ctrl+e", "end":
		ed.moveEnd()
		m.afterEdit(false, false)
	case "ctrl+k":
		ed.killToEnd()
		m.afterEdit(false, true)
	case "ctrl+u":
		ed.killToStart()
		m.afterEdit(false, true)
	case "ctrl+w", "alt+backspace":
		for k := 0; k < n; k++ {
			ed.killPrevWord()
		}
		m.afterEdit(false, true)
	case "alt+d":
		for k := 0; k < n; k++ {
			ed.killNextWord()
		}
		m.afterEdit(false, true)
	case "ctrl+y":
		if !ed.yankN(n) {
			// no entry at that depth: fall back to the front one
			ed.yank()
		}
		m.afterEdit(false, false)
	case "alt+y":
		ed.yankPop()
		m.afterEdit(false, false)
	case "ctrl+t":
		for k := 0; k < n; k++ {
			ed.transpose()
		}
		m.afterEdit(false, false)
	case "alt+t":
		for k := 0; k < n; k++ {
			ed.transposeWords()
		}
		m.afterEdit(false, false)
	case "tab":
		m.openMenu()
		if m.menu == nil {
			// nothing to show yet (e.g. the catalog snapshot is still
			// loading): arm the debounced completion, so a landing
			// background result still surfaces as a ghost
			m.afterEdit(true, false)
		}
	case "up", "ctrl+p":
		m.historyPrev()
	case "down", "ctrl+n":
		m.historyNext()
	case "ctrl+r":
		m.startSearch(false)
	case "ctrl+s":
		m.startSearch(true)
	case "ctrl+l":
		// clear the screen; bubbletea repaints the line (with ghost and
		// menu) in place, matching readline's Ctrl-L
		return m, tea.ClearScreen
	default:
		if rs := msg.Runes; len(rs) > 0 {
			ed.insertRunes(rs)
			m.afterEdit(false, false)
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
// of closed. deleted reports whether the edit removed text (backspace
// family), which holds the debounced completion off for compBack. The
// typing-time completion is debounced: it runs when the user pauses typing
// (compIdle), but never later than compMax after the word's first
// keystroke.
func (m *lineModel) afterEdit(keepMenu bool, deleted bool) {
	if m.menu != nil {
		m.refreshCompletion()
		return
	}
	if !keepMenu {
		m.ghost = nil
	}
	now := time.Now()
	if deleted {
		m.compBackAt = now
	}
	if ws := wordStartIdx(m.ed.buf, m.ed.idx); ws != m.compWordStart {
		m.compWordStart = ws
		m.compFirst = now
	}
	// a terminated statement stays quiet: no history ghost, no candidates
	// until a new word is typed
	atEnd := AtStatementEnd(m.ed.buf, m.ed.idx)
	if m.ed.atEnd() && !m.ed.empty() && !atEnd && m.t.hist != nil {
		m.ghost = m.t.hist.suggest(m.ed.buf)
	}
	// the metadata-sourced best match waits for the typing pause; the ghost
	// is filled in when the (possibly asynchronous) result lands
	m.compDue = m.t.comp != nil && m.ed.atEnd() && !m.ed.empty() && !atEnd && len(m.ghost) == 0
	idle := compIdle
	if now.Sub(m.compBackAt) < compBack {
		idle = compBack
	}
	deadline := now.Add(idle)
	// continuous typing still gets a suggestion, at most compMax after the
	// word's first keystroke
	if hard := m.compFirst.Add(compMax); hard.Before(deadline) {
		deadline = hard
	}
	m.compDeadline = deadline
}

// wordStartIdx returns the index where the word at idx begins — the run of
// identifier characters (letters, digits, and identifier punctuation)
// ending at idx. Used to detect that the word under the cursor changed.
func wordStartIdx(buf []rune, idx int) int {
	i := idx
	for i > 0 && isWordCharRune(buf[i-1]) {
		i--
	}
	return i
}

func isWordCharRune(r rune) bool {
	return r == '_' || r == '$' || r == '.' || r == '"' ||
		(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') || r >= 0x80
}

// scheduleCompletion arms the trailing completion tick while a debounced
// completion is due and the pause has not yet elapsed.
func (m *lineModel) scheduleCompletion() tea.Cmd {
	if !m.compDue || m.compPend {
		return nil
	}
	if d := time.Until(m.compDeadline); d > 0 {
		m.compPend = true
		return tea.Tick(d, func(time.Time) tea.Msg { return compMsg{} })
	}
	// the pause already elapsed (the deadline was set by an earlier batch of
	// this same keystroke burst): complete on the next Update pass
	m.compPend = true
	return tea.Tick(time.Nanosecond, func(time.Time) tea.Msg { return compMsg{} })
}

// computeCompletion runs the debounced typing-time completion request.
func (m *lineModel) computeCompletion() {
	m.compDue = false
	if m.done || m.menu != nil || m.t.comp == nil || !m.ed.atEnd() ||
		m.ed.empty() || len(m.ghost) > 0 {
		return
	}
	m.requestCandidates()
}

// requestCandidates asks the completer for candidates at the cursor,
// preferring the non-blocking live path.
func (m *lineModel) requestCandidates() ([]Cand, int, bool, bool) {
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

// refreshCompletion updates an open menu or — when none is open — pops the
// IME-style dropdown open from the typing-time completion: any pause long
// enough for the debounced completion shows the candidate list under the
// cursor, without needing Tab. Enter still executes the line then (only a
// Tab-opened menu accepts on Enter), so drafting a statement is never
// hijacked by the popup.
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
		m.setGhostAt(cands, m.menuSel, replace)
		return
	}
	// pop the menu open when there is room below the cursor and something
	// worth listing: candidates that carry no text (the word already
	// completed) are not a list
	listed := make([]Cand, 0, len(cands))
	for _, c := range cands {
		if c.Text != "" {
			listed = append(listed, c)
		}
	}
	if m.menuMax >= 1 && len(listed) > 0 {
		m.menu, m.menuLen, m.menuRep = cands, length, replace
		m.menuExplicit = false
		m.menuSel, m.menuTop = 0, 0
	}
	// fish-style: the best match is dimmed after the cursor regardless
	m.setGhostAt(cands, 0, replace)
}

// setGhostAt renders the i-th candidate as fish-style dim text after the
// cursor — the cursor stays where it is; right arrow accepts. With a menu
// open the ghost follows the selected row, so the grey text always matches
// what Tab would insert. Replace-style candidates carry the full word, so
// the typed prefix is stripped; plain candidates are already suffixes.
func (m *lineModel) setGhostAt(cands []Cand, i int, replace bool) {
	if !m.ed.atEnd() || m.ed.empty() || len(cands) == 0 || i < 0 || i >= len(cands) {
		m.ghost = nil
		return
	}
	if replace {
		start, _ := wordAt(m.ed.buf, m.ed.idx)
		typed := m.ed.buf[start:m.ed.idx]
		c := cands[i].Text
		// the ghost must read as a continuation of what is on screen: only
		// candidates that extend the typed word show one — last-segment
		// matches ("film" vs typed "fi" under "public.") would render a
		// misleading mid-word ghost, so they stay menu-only
		if strings.HasPrefix(strings.ToLower(c), strings.ToLower(string(typed))) && len(c) > len(typed) {
			m.ghost = []rune(c[len(typed):])
		} else {
			m.ghost = nil
		}
		return
	}
	m.ghost = []rune(cands[i].Text)
}

// scheduleMenuRefresh arms the trailing tick that refilters the menu after
// the delete cooldown. It shares compPend with the completion debounce —
// only one tick can be outstanding at a time.
func (m *lineModel) scheduleMenuRefresh() tea.Cmd {
	if !m.menuPend || m.compPend {
		return nil
	}
	m.compPend = true
	d := time.Until(m.menuCoolAt)
	if d < 0 {
		d = 0
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return compMsg{} })
}

// openMenu requests candidates and opens the vertical menu. When the space
// below the cursor cannot hold a menu row (menuMax == 0), the menu declines:
// opening it would scroll the terminal, and the ghost suggestion still shows
// the best match.
func (m *lineModel) openMenu() {
	if m.menuMax < 1 {
		m.closeMenu()
		return
	}
	cands, length, replace, ok := m.requestCandidates()
	if !ok || len(cands) == 0 {
		m.closeMenu()
		return
	}
	m.menu, m.menuLen, m.menuRep = cands, length, replace
	m.menuExplicit = true
	// the first candidate starts selected, so Enter/Tab accept it directly
	m.menuSel, m.menuTop = 0, 0
	// fish-style: the selected match is dimmed after the cursor while the
	// menu lists the rest
	m.setGhostAt(cands, 0, replace)
}

// closeMenu closes the candidate menu.
func (m *lineModel) closeMenu() {
	m.menu, m.menuSel, m.menuTop = nil, -1, 0
	m.menuLen, m.menuRep, m.menuExplicit = 0, false, false
}

// selectCandidate moves the menu selection by delta, wrapping.
func (m *lineModel) selectCandidate(delta int) {
	if len(m.menu) == 0 {
		return
	}
	if m.menuSel < 0 {
		m.menuSel = 0
	} else {
		m.menuSel = (m.menuSel + delta + len(m.menu)) % len(m.menu)
	}
	m.scrollMenu()
	m.setGhostAt(m.menu, m.menuSel, m.menuRep)
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
	if m.menu != nil {
		m.setGhostAt(m.menu, m.menuSel, m.menuRep)
	}
}

// scrollMenu keeps the selection inside the visible window.
func (m *lineModel) scrollMenu() {
	h := m.menuMax
	if h > menuHeight {
		h = menuHeight
	}
	if h < 1 {
		h = 1
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
	cand := []rune(m.menu[i].Text)
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
	m.afterEdit(false, false)
}

// minBelowRows is the smallest terminal headroom for a usable menu.
const minBelowRows = 5

// resize recomputes the candidate menu placement for a terminal height. The
// bubbletea frame grows downward from the row the read started on, so the
// menu can only use the rows below the cursor without scrolling the
// terminal history — one row is reserved for the footer. Without a known
// cursor row the conservative small cap applies.
func (m *lineModel) resize(h int) {
	switch {
	case m.topRow < 0:
		// unknown cursor row (DSR probe failed): keep the menu small — if
		// the cursor actually sits at the screen bottom, a full-height menu
		// opening below would scroll that much history at once
		if m.menuMax > unknownRowMenuMax {
			m.menuMax = unknownRowMenuMax
		}
	case h < minBelowRows+2:
		// tiny terminal: shrink below
		if hh := h - 2; hh < m.menuMax {
			if hh < 1 {
				hh = 1
			}
			m.menuMax = hh
		}
	default:
		// the bubbletea frame grows downward from the row the read started
		// on, so the menu can only use the rows below the cursor without
		// scrolling the terminal history — one row reserved for the footer
		if mm := h - m.topRow - 2; mm < m.menuMax {
			if mm < 0 {
				mm = 0
			}
			m.menuMax = mm
		}
	}
	if m.menu != nil {
		m.clampMenuSel()
	}
}

// View renders the input line, candidate menu, and search prompt.
func (m *lineModel) View() string {
	var b strings.Builder
	switch {
	case m.searching:
		b.WriteString(m.searchView())
	default:
		b.WriteString(m.lineView())
		if m.menu != nil {
			b.WriteString(m.menuView())
		}
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
	// the debounced output-filter (syntax highlight) render of the line
	hi := m.highlight(string(buf))
	// when finalizing, render without the cursor block
	if m.done {
		b.WriteString(hi)
		return b.String()
	}
	switch {
	case idx < len(buf):
		b.WriteString(m.lineStyled(hi, buf, idx))
	case len(m.ghost) > 0 && uitheme.Enabled():
		// block cursor over the first ghost rune, rest dim
		b.WriteString(hi)
		b.WriteString(t.Selected.Render(string(m.ghost[0])))
		b.WriteString(t.Dim.Render(string(m.ghost[1:])))
	default:
		b.WriteString(hi)
		b.WriteString(t.Selected.Render(" "))
	}
	return b.String()
}

// searchView renders the incremental search prompt.
func (m *lineModel) searchView() string {
	t := uitheme.Current()
	dir := "reverse-i-search"
	if m.searchFwd {
		dir = "forward-i-search"
	}
	mark := t.Warn.Render("(" + dir + ")`")
	markEnd := t.Warn.Render("': ")
	if m.searchFail {
		mark = t.Dim.Render("(failed " + dir + ")`")
	}
	return mark + m.query + markEnd + string(m.ed.buf)
}

// menuView renders the vertical candidate menu below the input line: the
// visible window of candidates, each with its display kind right-aligned as
// a dim badge, and a footer counting the candidates below the window.
//
// The frame height is constant — min(menuMax, menuHeight) candidate rows
// plus one footer row — regardless of how many candidates match: padding
// with blank rows keeps the rendered frame from shrinking and growing on
// every refilter, which would scroll the terminal history.
func (m *lineModel) menuView() string {
	t := uitheme.Current()
	h := m.menuMax
	if h > menuHeight {
		h = menuHeight
	}
	if h < 1 {
		h = 1
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
	mark, dots := "▸ ", "… "
	if uitheme.ConsoleEncoding() != nil {
		// '▸'/'…' do not exist in GBK-family encodings and would be
		// transcoded to '?', shifting the menu
		mark, dots = "> ", "..."
	}
	// measure the window's rows once, so kind badges align
	texts := make([]string, 0, h)
	widths := make([]int, 0, h)
	details := make([]string, 0, h)
	maxw := 0
	for i := m.menuTop; i < end; i++ {
		full := append(append([]rune(nil), typed...), []rune(m.menu[i].Text)...)
		text := string(full)
		if i-m.menuTop < 9 && m.menuMax >= 9 {
			text = strconv.Itoa(i+1) + " " + text
		}
		w := runewidth.StringWidth(text)
		if w > maxw {
			maxw = w
		}
		texts = append(texts, text)
		widths = append(widths, w)
		details = append(details, truncateDetail(m.menu[i].Detail, detailMaxWidth))
	}
	// badges and details share one width gate: when the widest text row
	// alone is too wide, both stay off so the menu never pushes past the
	// terminal's right edge
	badge := maxw <= badgeMaxWidth
	var b strings.Builder
	for k, i := 0, m.menuTop; k < h; k, i = k+1, i+1 {
		// every row — the first included — starts on a fresh line: the
		// menu renders below the input line, which does not end in a
		// newline of its own
		b.WriteString("\n")
		if i < end {
			row := texts[k] + strings.Repeat(" ", maxw-widths[k])
			kind := m.menu[i].Kind
			detail := details[k]
			if i == m.menuSel {
				b.WriteString(t.Selected.Render(mark + row))
				switch {
				case badge && detail != "":
					b.WriteString(t.Selected.Render(detail))
				case badge && kind != "":
					b.WriteString(t.Selected.Render("  " + kind))
				}
			} else {
				b.WriteString("  " + row)
				switch {
				case badge && detail != "":
					b.WriteString(t.Dim.Render(detail))
				case badge && kind != "":
					b.WriteString(t.Dim.Render("  " + kind))
				}
			}
		}
	}
	// the footer row is always rendered, so the frame ends at a fixed height
	if more := len(m.menu) - end; more > 0 {
		b.WriteString("\n")
		b.WriteString(t.Dim.Render("  " + dots + strconv.Itoa(more) + " more"))
	}
	b.WriteString("\n")
	return b.String()
}
