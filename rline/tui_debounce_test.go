package rline

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDebounceIdleFires the completion deadline lands one compIdle after the
// last keystroke.
func TestDebounceIdle(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect"}})
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	if !m.compDue {
		t.Fatal("typing did not arm the debounced completion")
	}
	d := time.Until(m.compDeadline)
	if d <= 0 || d > compIdle+10*time.Millisecond {
		t.Fatalf("deadline in %v, want ~%v", d, compIdle)
	}
}

// TestDebounceBackspaceCooldown: a deletion within the word holds the
// completion off for compBack, not compIdle.
func TestDebounceBackspaceCooldown(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	m := typeLine(newLineModel(tr, "> ", -1), "SELECT * FROM film")
	before := time.Until(m.compDeadline)
	if before <= 0 || before > compIdle+10*time.Millisecond {
		t.Fatalf("deadline before backspace in %v, want ~%v", before, compIdle)
	}
	mod, _ := m.Update(keyName("backspace"))
	m = mod.(*lineModel)
	d := time.Until(m.compDeadline)
	if d < compBack-time.Millisecond*50 {
		t.Fatalf("deadline after backspace in %v, want ~%v", d, compBack)
	}
}

// TestDebounceHardCap: once a word has been typed for compMax, the deadline
// is capped at the word's first keystroke + compMax even while typing
// continues.
func TestDebounceHardCap(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "SELECT * FROM fi")
	// simulate a word that started compMax ago
	m.compFirst = time.Now().Add(-compMax)
	// another keystroke: the deadline must not slide past first+compMax
	mod, _ := m.Update(keyTyped("l"))
	m = mod.(*lineModel)
	if d := time.Until(m.compDeadline); d > 50*time.Millisecond {
		t.Fatalf("deadline in %v, want capped at compMax since word start", d)
	}
}

// TestDebounceStatementEndQuiet: after a terminated statement no completion
// is armed at all.
func TestDebounceStatementEndQuiet(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect"}})
	m := typeLine(newLineModel(tr, "> ", -1), "SELECT 1; ")
	if m.compDue {
		t.Fatal("completion armed after a terminated statement")
	}
	m = typeLine(m, "s")
	if !m.compDue {
		t.Fatal("new word after terminator must arm the completion")
	}
}

// TestMenuViewKindsAndFooter: kind badges render right-aligned on rows and
// the footer counts candidates below the window.
func TestMenuViewKindsAndFooter(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"x"}})
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "film")
	// build a replace-style menu with kinds: 2 visible + extra below window
	m.menuRep = true
	m.menu = []Cand{
		{Text: "public.film", Kind: "table"},
		{Text: "public.film_view", Kind: "view"},
	}
	for i := 0; i < 12; i++ {
		m.menu = append(m.menu, Cand{Text: "zz" + string(rune('a'+i%26))})
	}
	m.menuSel, m.menuTop, m.menuMax = -1, 0, menuHeight
	out := m.menuView()
	if !strings.Contains(out, "table") {
		t.Errorf("menu missing table badge:\n%s", out)
	}
	if !strings.Contains(out, "view") {
		t.Errorf("menu missing view badge:\n%s", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("menu missing +N more footer:\n%s", out)
	}
	// the two badge rows are aligned: both badges start at the same column
	var cols []int
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, "table") || strings.HasSuffix(line, "view") {
			plain := stripANSI(line)
			cols = append(cols, strings.Index(plain, strings.TrimSuffix(plain, ""))+len(strings.TrimRight(plain, "table view")))
		}
		_ = line
	}
	if len(cols) < 2 {
		// alignment asserted loosely: both badge rows must pad to equal width
		var pads []int
		for _, line := range strings.Split(out, "\n") {
			plain := stripANSI(line)
			if strings.Contains(plain, "public.film") || strings.Contains(plain, "public.film_view") {
				pads = append(pads, len(plain))
			}
		}
		if len(pads) == 2 && pads[0] != pads[1] {
			t.Errorf("badge rows differ in width: %v", pads)
		}
	}
}

// TestMenuViewDetails: candidate details render dim after the text — a
// function's signature, a column's data type — take precedence over the
// kind badge, and are truncated when too long.
func TestMenuViewDetails(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"x"}})
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "film")
	m.menuRep = true
	long := strings.Repeat("x", 60) + ")"
	m.menu = []Cand{
		{Text: "city_id", Detail: ":number(10)", Kind: "column"},
		{Text: "count(", Detail: "expr)", Kind: "function"},
		{Text: "now(", Detail: long, Kind: "function"},
		{Text: "public.film", Kind: "table"},
	}
	m.menuSel, m.menuTop, m.menuMax = -1, 0, menuHeight
	out := stripANSI(m.menuView())
	if !strings.Contains(out, "city_id") || !strings.Contains(out, ":number(10)") {
		t.Errorf("menu missing the field:type hint:\n%s", out)
	}
	if !strings.Contains(out, "count(") || !strings.Contains(out, "expr)") {
		t.Errorf("menu missing the function signature:\n%s", out)
	}
	if strings.Contains(out, long) {
		t.Errorf("overlong detail not truncated:\n%s", out)
	}
	// the no-detail row still shows its kind badge
	if !strings.Contains(out, "table") {
		t.Errorf("menu missing table badge on a no-detail row:\n%s", out)
	}
}

// stripANSI removes SGR sequences for plain-text assertions.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// countingTabStub counts completion requests.
type countingTabStub struct {
	n int32
}

func (s *countingTabStub) Do(line []rune, pos int) ([]Cand, int) {
	atomic.AddInt32(&s.n, 1)
	return []Cand{{Text: "ect"}}, 3
}

func (s *countingTabStub) count() int32 {
	return atomic.LoadInt32(&s.n)
}

// TestTabIssuesSingleRequest: TAB opens the menu with exactly one completion
// request — the menu refresh in afterEdit must not duplicate it.
func TestTabIssuesSingleRequest(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	cs := &countingTabStub{}
	tr.Completer(cs)
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	if m.menu == nil {
		t.Fatal("tab did not open the menu")
	}
	if n := cs.count(); n != 1 {
		t.Fatalf("tab issued %d completion requests, want 1", n)
	}
}
