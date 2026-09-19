package completer_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/rline"

	// the default driver set, so the bench can connect to real databases
	_ "github.com/xo/usql/internal"
)

// TestBenchLiveCatalog measures typing-time completion against a real,
// large catalog. Enabled only when USQL_BENCH_DSN is set (comma-separated
// DSNs); each connection reports:
//
//	cold    — connect to first non-empty DoLive candidates (lazy cache load)
//	typing  — per-keystroke DoLive latency while extending one word
//	retype  — per-keystroke latency after the word completed
//	erase   — per-backspace latency (candidate set re-filter)
//	tab     — synchronous Do latency (TAB path)
//	list    — \dt argument completion: candidate count and latency
//	menu    — FROM position: candidate count (namespaces + selectables)
func TestBenchLiveCatalog(t *testing.T) {
	dsns := os.Getenv("USQL_BENCH_DSN")
	if dsns == "" {
		t.Skip("USQL_BENCH_DSN not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		dsn = strings.TrimSpace(dsn)
		if dsn != "" {
			t.Run(dsn, func(t *testing.T) { benchDSN(t, dsn) })
		}
	}
}

func benchDSN(t *testing.T, dsn string) {
	u, err := dburl.Parse(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	db, err := sql.Open(u.Driver, u.DSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	comp := drivers.NewCompleter(ctx, u, db, nil,
		completer.WithContextCompletion(),
		completer.WithConnStrings(nil),
	)
	live := completer.NewLive(comp)

	// cold: poll DoLive at a table-position word until candidates land
	start := time.Now()
	var cold time.Duration
	line := []rune("SELECT * FROM t")
	for {
		cands, _, _ := live.(rline.LiveCompleter).DoLive(line, len(line))
		if len(cands) > 0 {
			cold = time.Since(start)
			break
		}
		if time.Since(start) > 30*time.Second {
			t.Fatal("no candidates within 30s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("cold    first candidates after %v", cold)

	// settle: the full snapshot (all reachable schemas) lands after the
	// first fall-through candidates; give it time so the numbers below are
	// the steady-state ones
	time.Sleep(2 * time.Second)

	// typing: extend the word one rune at a time; report the slowest and the
	// keystroke after the memo fills
	word := "_0001"
	worst, total := time.Duration(0), time.Duration(0)
	for i := 1; i <= len(word); i++ {
		l := append([]rune(nil), line...)
		l = append(l, []rune(word[:i])...)
		t0 := time.Now()
		live.(rline.LiveCompleter).DoLive(l, len(l))
		d := time.Since(t0)
		if d > worst {
			worst = d
		}
		total += d
		time.Sleep(20 * time.Millisecond) // let the background compute land
	}
	t.Logf("typing  worst %v, avg %v over %d keystrokes", worst, total/time.Duration(len(word)), len(word))

	// retype: the memo is hot now; keystrokes must be pure in-process work
	l := append([]rune(nil), line...)
	l = append(l, []rune(word)...)
	worst, total = 0, 0
	for i := len(word); i >= 3; i-- { // erase: backspaces re-filter the memo
		ll := append([]rune(nil), line...)
		ll = append(ll, []rune(word[:i])...)
		t0 := time.Now()
		cands, _, _ := live.(rline.LiveCompleter).DoLive(ll, len(ll))
		d := time.Since(t0)
		if d > worst {
			worst = d
		}
		total += d
		if i == len(word) {
			t.Logf("menu    %d candidates for %q", len(cands), word)
		}
	}
	t.Logf("erase   worst %v, avg %v over %d backspaces", worst, total/time.Duration(len(word)-2), len(word)-2)

	// tab: synchronous path
	t0 := time.Now()
	cands, _ := live.Do(append(append([]rune(nil), line...), []rune("_0001")...), len(line)+len(word))
	t.Logf("tab     %d candidates in %v", len(cands), time.Since(t0))

	// \dt listing
	dt := []rune(`\dt `)
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(dt, len(dt))
	t.Logf("list    \\dt -> %d candidates in %v", len(cands), time.Since(t0))

	dt = []rune(`\dt t_01`)
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(dt, len(dt))
	t.Logf("list    \\dt t_01 -> %d candidates in %v", len(cands), time.Since(t0))

	// FROM position: the full option set size (namespaces + selectables)
	fr := []rune("SELECT * FROM ")
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(fr, len(fr))
	t.Logf("menu    FROM -> %d candidates (namespaces first) in %v", len(cands), time.Since(t0))

	fmt.Println()
}
