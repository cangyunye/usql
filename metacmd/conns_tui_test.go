package metacmd

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestConnsListRows pins the vertical budget of the list view: the chrome
// (borders, header, blank, hints) leaves height-6 rows, the error line takes
// two more, and the budget never drops below one row.
func TestConnsListRows(t *testing.T) {
	for _, tt := range []struct {
		height int
		err    bool
		want   int
	}{
		{height: 24, want: 18},
		{height: 24, err: true, want: 16},
		{height: 100, want: 94},
		{height: 7, want: 1},
		{height: 0, want: 1},
	} {
		m := &connsModel{height: tt.height}
		if tt.err {
			m.err = "boom"
		}
		if got := m.listRows(); got != tt.want {
			t.Errorf("listRows(height=%d, err=%v) = %d, want %d", tt.height, tt.err, got, tt.want)
		}
	}
}

// TestConnsListFirst pins the scroll window: it pins the top row so the
// selection stays visible, never scrolls past either end, and holds at 0
// while everything fits.
func TestConnsListFirst(t *testing.T) {
	m := &connsModel{names: make([]string, 24)}
	for i := range m.names {
		m.names[i] = fmt.Sprintf("c%02d", i)
	}
	for _, tt := range []struct {
		rowsel, vis, want int
	}{
		{rowsel: -1, vis: 18, want: 0},
		{rowsel: 0, vis: 18, want: 0},
		{rowsel: 17, vis: 18, want: 0},
		{rowsel: 18, vis: 18, want: 1},
		{rowsel: 19, vis: 18, want: 2},
		{rowsel: 23, vis: 18, want: 6},
		{rowsel: 5, vis: 30, want: 0}, // everything fits: no scrolling
		{rowsel: 23, vis: 1, want: 23},
	} {
		m.rowsel = tt.rowsel
		if got := m.listFirst(tt.vis); got != tt.want {
			t.Errorf("listFirst(rowsel=%d, vis=%d) = %d, want %d", tt.rowsel, tt.vis, got, tt.want)
		}
	}
}

// TestConnsListEditKeys pins the list-mode edit shortcuts: e and ↵ open the
// edit form for the row under the cursor — including rows past the ninth,
// which the retired digit shortcuts could never reach — and a digit key no
// longer opens a form. With no row selected, e stays in the list with an
// error line.
func TestConnsListEditKeys(t *testing.T) {
	m := &connsModel{mode: connsModeList, rowsel: 11}
	m.names = make([]string, 12)
	for i := range m.names {
		m.names[i] = fmt.Sprintf("c%02d", i)
	}
	typeKey := func(s string) tea.KeyMsg {
		return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(s)})
	}
	nm, _ := m.handleKey(typeKey("e"))
	em := nm.(*connsModel)
	if em.mode != connsModeForm || em.formEdit != "c11" || len(em.fields) == 0 {
		t.Fatalf("e at row 12: mode=%v formEdit=%q fields=%d", em.mode, em.formEdit, len(em.fields))
	}
	nm, _ = em.handleKey(tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	lm := nm.(*connsModel)
	if lm.mode != connsModeList {
		t.Fatalf("esc: mode=%v, want list", lm.mode)
	}
	lm.rowsel = 3
	nm, _ = lm.handleKey(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	em = nm.(*connsModel)
	if em.mode != connsModeForm || em.formEdit != "c03" {
		t.Fatalf("enter at row 4: mode=%v formEdit=%q", em.mode, em.formEdit)
	}
	nm, _ = em.handleKey(tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	lm = nm.(*connsModel)
	nm, _ = lm.handleKey(typeKey("5"))
	lm = nm.(*connsModel)
	if lm.mode != connsModeList {
		t.Fatalf("digit 5: mode=%v, want list", lm.mode)
	}
	lm.rowsel = -1
	nm, _ = lm.handleKey(typeKey("e"))
	em = nm.(*connsModel)
	if em.mode != connsModeList || em.err == "" {
		t.Fatalf("e with no selection: mode=%v err=%q", em.mode, em.err)
	}
}
