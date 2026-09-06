package rline

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEditorBasics(t *testing.T) {
	var e editor
	for _, r := range "SELECT" {
		e.insert(r)
	}
	if e.String() != "SELECT|" || !e.atEnd() {
		t.Fatalf("after insert: %q atEnd=%v", e.String(), e.atEnd())
	}
	e.moveStart()
	e.moveRight()
	if got := e.String(); got != "S|ELECT" {
		t.Fatalf("after moveRight: %q", got)
	}
	e.backspace()
	if got := e.String(); got != "|ELECT" {
		t.Fatalf("after backspace: %q", got)
	}
	e.moveEnd()
	e.insertRunes([]rune(" * FROM"))
	if got := string(e.line()); got != "ELECT * FROM" {
		t.Fatalf("after insertRunes: %q", got)
	}
}

func TestEditorKillYank(t *testing.T) {
	var e editor
	e.reset([]rune("one two three"))
	e.moveEnd()
	e.killToEnd() // cursor at end: nothing to kill
	if got := string(e.line()); got != "one two three" {
		t.Fatalf("killToEnd at end: %q", got)
	}
	e.killPrevWord() // kills " three"
	if got := e.String(); got != "one two |" {
		t.Fatalf("killPrevWord: %q", got)
	}
	e.killToStart() // kills "one two"
	if !e.empty() {
		t.Fatalf("killToStart: %q", e.String())
	}
	// consecutive kills accumulate in cut order
	if !e.yank() {
		t.Fatal("yank failed")
	}
	if got := string(e.line()); got != "threeone two " {
		t.Fatalf("yank: %q", got)
	}
	// word kill: Ctrl-W removes the word before the cursor, including the
	// space that separated it
	e.reset([]rune("alpha beta"))
	e.moveWordLeft()
	e.killPrevWord()
	if got := e.String(); got != "|beta" {
		t.Fatalf("killPrevWord: %q", got)
	}
}

func TestEditorReplaceBefore(t *testing.T) {
	var e editor
	e.reset([]rune("select * from film"))
	e.moveEnd()
	e.deleteRunes(2) // erase "lm"
	if got := e.String(); got != "select * from fi|" {
		t.Fatalf("after deleteRunes: %q", got)
	}
	e.replaceBefore(0, []rune("lm"))
	if got := string(e.line()); got != "select * from film" {
		t.Fatalf("after replaceBefore: %q", got)
	}
}

func TestTUIHistoryPrevNextDraft(t *testing.T) {
	h := &tuiHistory{lines: []string{"select 1", "select 2"}}
	pos := 2 // at the draft
	pos, line, ok := h.prev(pos, "sel")
	if !ok || line != "select 2" {
		t.Fatalf("prev: %q %v", line, ok)
	}
	pos, line, ok = h.next(pos, "sel")
	if !ok || line != "sel" {
		t.Fatalf("next to draft: %q %v", line, ok)
	}
	pos, line, ok = h.next(pos, "sel")
	if ok {
		t.Fatal("next past draft should not move")
	}
}

func TestTUIHistorySearchAndSuggest(t *testing.T) {
	h := &tuiHistory{lines: []string{"SELECT * FROM a", "select 1", "SELECT * FROM b"}}
	if i, line, ok := h.search("select * from", 3); !ok || line != "SELECT * FROM b" || i != 2 {
		t.Fatalf("search: %d %q %v", i, line, ok)
	}
	if i, line, ok := h.search("select * from", 2); !ok || line != "SELECT * FROM a" || i != 0 {
		t.Fatalf("search again: %d %q %v", i, line, ok)
	}
	if got := h.suggest([]rune("sel")); string(got) != "ect 1" {
		t.Fatalf("suggest: %q", got)
	}
	if got := h.suggest([]rune("no match")); got != nil {
		t.Fatalf("suggest no match: %q", got)
	}
}

func TestTUIHistoryFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")
	h := loadTUIHistory(path)
	for _, s := range []string{"select a", "select b", ""} {
		if err := h.Save(s); err != nil {
			t.Fatalf("Save(%q): %v", s, err)
		}
	}
	// empty lines are not persisted
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "select a\nselect b\n" {
		t.Fatalf("file = %q", string(b))
	}
	// reload caps past historyMaxLines
	h2 := loadTUIHistory(path)
	if len(h2.lines) != 2 || h2.lines[1] != "select b" {
		t.Fatalf("reloaded: %q", h2.lines)
	}
}

// keyTyped builds a printable-key message.
func keyTyped(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyName(name string) tea.KeyMsg {
	names := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "ctrl+c": tea.KeyCtrlC, "ctrl+d": tea.KeyCtrlD,
		"tab": tea.KeyTab, "esc": tea.KeyEscape, "up": tea.KeyUp,
		"down": tea.KeyDown, "right": tea.KeyRight, "ctrl+r": tea.KeyCtrlR,
		"backspace": tea.KeyBackspace,
	}
	if kt, ok := names[name]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// typeLine drives the model with a string and returns it.
func typeLine(m *lineModel, s string) *lineModel {
	for _, r := range s {
		mod, _ := m.Update(keyTyped(string(r)))
		m = mod.(*lineModel)
	}
	return m
}

func TestLineModelGhostAccept(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.hist = &tuiHistory{lines: []string{"select * from align"}}
	tr.Prompt("usql> ")
	m := newLineModel(tr, "usql> ")
	m = typeLine(m, "sel")
	if len(m.ghost) == 0 {
		t.Fatal("expected a ghost suggestion after typing a history prefix")
	}
	// right arrow accepts the whole suggestion
	mod, _ := m.Update(keyName("right"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select * from align" {
		t.Fatalf("after ghost accept: %q", got)
	}
}

func TestLineModelInterruptAndEOF(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("ctrl+c"))
	m = mod.(*lineModel)
	if !m.done || !m.interrupted {
		t.Fatal("ctrl+c must finalize with interrupt")
	}
	m2 := newLineModel(tr, "> ")
	mod, _ = m2.Update(keyName("ctrl+d"))
	m2 = mod.(*lineModel)
	if !m2.done || m2.interrupted {
		t.Fatal("ctrl+d on empty line must finalize as EOF")
	}
}

// stubCompleter returns fixed suffix candidates.
type stubCompleter struct {
	cands []string
}

func (s *stubCompleter) Do(line []rune, pos int) ([][]rune, int) {
	out := make([][]rune, 0, len(s.cands))
	for _, c := range s.cands {
		out = append(out, []rune(c))
	}
	return out, len(line)
}

func TestLineModelMenuTabAccept(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.Completer(&stubCompleter{cands: []string{"ect", "ect 1"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	m = typeLine(m, "sel")
	// Tab opens the vertical menu
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	if m.menu == nil || len(m.menu) != 2 {
		t.Fatalf("tab did not open menu: %+v", m.menu)
	}
	// down selects, enter accepts the candidate but not the line
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if m.done {
		t.Fatal("enter must accept the candidate, not finalize the line")
	}
	if got := string(m.ed.buf); got != "select 1" {
		t.Fatalf("after menu accept: %q", got)
	}
	if m.menu != nil {
		t.Fatal("menu must close after accepting")
	}
}

func TestLineModelMenuAltDigit(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.Completer(&stubCompleter{cands: []string{"ect", "ect 1"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1"), Alt: true})
	m = mod.(*lineModel)
	if m.done {
		t.Fatal("alt+digit must accept the candidate, not finalize")
	}
	if got := string(m.ed.buf); got != "select" {
		t.Fatalf("alt+1 accept: %q", got)
	}
}

func TestLineModelMenuFilterWhileTyping(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.Completer(&stubCompleter{cands: []string{"ect", "ection"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	if m.menu == nil {
		t.Fatal("menu did not open")
	}
	// typing keeps the menu open and refilters
	mod, _ = m.Update(keyTyped("i"))
	m = mod.(*lineModel)
	if m.menu == nil {
		t.Fatal("menu closed while typing")
	}
	// esc closes it
	mod, _ = m.Update(keyName("esc"))
	m = mod.(*lineModel)
	if m.menu != nil {
		t.Fatal("esc did not close the menu")
	}
}

func TestLineModelHistoryNav(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.hist = &tuiHistory{lines: []string{"select 1", "select 2"}}
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	mod, _ := m.Update(keyName("up"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 2" {
		t.Fatalf("up: %q", got)
	}
	mod, _ = m.Update(keyName("up"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 1" {
		t.Fatalf("up again: %q", got)
	}
	// editing a history entry does not change the draft: down walks to the
	// next entry, then past the newest entry restores the original draft
	mod, _ = m.Update(keyTyped("x"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 2" {
		t.Fatalf("down: %q", got)
	}
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "" {
		t.Fatalf("down to draft: %q", got)
	}
}

func TestLineModelIncrementalSearch(t *testing.T) {
	tr := newTUI(nil, nil, nil, "")
	tr.hist = &tuiHistory{lines: []string{"SELECT 1 FROM a", "select 2"}}
	tr.Prompt("> ")
	m := newLineModel(tr, "> ")
	mod, _ := m.Update(keyName("ctrl+r"))
	m = mod.(*lineModel)
	if !m.searching {
		t.Fatal("ctrl+r did not enter search mode")
	}
	m = typeLine(m, "select 1")
	if got := string(m.ed.buf); got != "SELECT 1 FROM a" {
		t.Fatalf("search match: %q", got)
	}
	// enter accepts the match without executing
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if m.searching || m.done {
		t.Fatalf("enter in search: searching=%v done=%v", m.searching, m.done)
	}
	if got := string(m.ed.buf); got != "SELECT 1 FROM a" {
		t.Fatalf("after search accept: %q", got)
	}
}

func TestInputMode(t *testing.T) {
	t.Setenv("USQL_INPUT", "tui")
	if !inputMode(true, false) {
		t.Error("tui mode should activate when interactive")
	}
	if inputMode(false, false) {
		t.Error("tui mode must not activate when non-interactive")
	}
	if inputMode(true, true) {
		t.Error("tui mode must not activate when forced non-interactive")
	}
	t.Setenv("USQL_INPUT", "readline")
	if inputMode(true, false) {
		t.Error("readline must stay the default engine")
	}
}
