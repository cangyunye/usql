package handler

import (
	"io"
	"testing"

	"github.com/xo/usql/rline"
)

func TestMoreWithoutPagedResult(t *testing.T) {
	h := &Handler{}
	if err := h.More(0); err == nil {
		t.Fatal("More without a paged result must fail")
	}
}

func TestPageShownAdvancesAndExhausts(t *testing.T) {
	h := &Handler{l: &rline.Rline{Out: io.Discard, Int: true}}
	h.page = &pageState{offset: 0, size: 100}
	h.pageShown(&limitedRows{rowLimiter: rowLimiter{limit: 100, seen: 100, hasMore: true}})
	if h.page == nil || h.page.offset != 100 {
		t.Fatalf("after a probed page, offset = %d, want 100", h.page.offset)
	}
	h.pageShown(&limitedRows{rowLimiter: rowLimiter{limit: 100, seen: 37, hasMore: false}})
	if h.page != nil {
		t.Fatal("after an exhausted page, the state must be dropped")
	}
}

// TestRowLimiterProbe: the limiter yields exactly limit rows, then peeks
// one row further to learn whether a continuation page exists — and the
// peek consumes exactly one underlying row, exactly once.
func TestRowLimiterProbe(t *testing.T) {
	remaining := 5 // 3 page rows + probe + 1 more the limiter must not touch
	pulled := 0
	l := rowLimiter{
		limit: 3,
		next: func() bool {
			pulled++
			remaining--
			return remaining >= 0
		},
	}
	var got int
	for l.Next() {
		got++
	}
	if got != 3 {
		t.Fatalf("yielded %d rows, want 3", got)
	}
	if !l.hasMore {
		t.Fatal("probe row existed, hasMore = false")
	}
	if pulled != 4 {
		t.Fatalf("pulled %d rows from the source, want 3+1 probe", pulled)
	}
	// further Next calls are stable and do not pull more rows
	if l.Next() {
		t.Fatal("Next after the probe must be false")
	}
	if pulled != 4 {
		t.Fatalf("repeated Next pulled %d rows, want 4", pulled)
	}
}

func TestRowLimiterNoProbeRow(t *testing.T) {
	remaining := 2 // fewer rows than the page size
	l := rowLimiter{limit: 3, next: func() bool {
		remaining--
		return remaining >= 0
	}}
	for l.Next() {
	}
	if l.hasMore || l.probed {
		t.Fatalf("hasMore=%v probed=%v, want false,false (source exhausted before the limit)", l.hasMore, l.probed)
	}
}
