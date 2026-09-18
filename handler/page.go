package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/xo/usql/drivers/rowlimit"
	"github.com/xo/usql/metacmd"
	"github.com/xo/usql/uitheme"
)

// pageState tracks the continuation of a row-limited interactive query (see
// the ROWLIMIT variable and \more): the original, un-rewritten statement,
// its binds, and how many rows have been shown so far. Each page fetches
// size+1 rows — the extra row only probes whether another page exists —
// and displays size of them.
type pageState struct {
	sqlstr string
	bind   []interface{}
	driver string
	offset int
	size   int
}

// limitedRows exposes at most limit rows of the wrapped *sql.Rows; reading
// past the limit consumes one probe row and remembers whether it existed,
// so the caller knows a continuation page is available.
type limitedRows struct {
	*sql.Rows

	rowLimiter
}

// rowLimiter carries the display/probe logic: it yields at most limit rows
// from next, then peeks exactly one row further to learn whether another
// page exists. Split from limitedRows so it can be tested without a
// *sql.Rows.
type rowLimiter struct {
	next func() bool

	limit   int
	seen    int
	hasMore bool
	probed  bool
}

// Next yields up to limit rows, then probes exactly one row further.
func (r *rowLimiter) Next() bool {
	if r.seen >= r.limit {
		if !r.probed {
			r.probed = true
			r.hasMore = r.next()
		}
		return false
	}
	if r.next() {
		r.seen++
		return true
	}
	return false
}

// Next resolves the embedded-name ambiguity in favor of the limiter: both
// *sql.Rows and rowLimiter provide Next, so limitedRows states it.
func (r *limitedRows) Next() bool { return r.rowLimiter.Next() }

// pageShown advances the paging state after a page was displayed: the probe
// row says whether another page exists, and a hint line tells the user how
// to continue. The state is dropped when the pages are exhausted.
func (h *Handler) pageShown(limited *limitedRows) {
	if h.page == nil {
		return
	}
	if !limited.hasMore {
		h.page = nil
		return
	}
	h.page.offset += h.page.size
	fmt.Fprintln(h.l.Stdout(), uitheme.Current().Dim.Render(
		fmt.Sprintf("(%d rows, more available — type \\more for the next page)", limited.seen)))
}

// More shows the next page of the last row-limited interactive query (\more).
// n is the page size; n <= 0 keeps the previous page size.
func (h *Handler) More(n int) error {
	st := h.page
	if st == nil {
		return errors.New("no paged result to continue")
	}
	if h.db == nil {
		return errors.New("no paged result to continue: connection closed")
	}
	if n > 0 {
		st.size = n
	}
	paged, ok := rowlimit.Page(st.driver, st.sqlstr, st.size+1, st.offset)
	if !ok {
		return errors.New("the last query cannot be continued in the connected database's paging syntax")
	}
	opt := metacmd.Option{
		Exec: metacmd.ExecOnly,
		Params: map[string]string{
			"title": fmt.Sprintf("rows %d-%d", st.offset+1, st.offset+st.size),
		},
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return h.doExecSingle(ctx, h.l.Stdout(), opt, "", paged, true, st.bind)
}
