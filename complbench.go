//go:build ignore

// complbench.go is a manual harness (excluded from builds via //go:build
// ignore) that
// measures SQL tab-completion performance against live databases at scale,
// through the same public API the interactive completer uses
// (completer.NewDefaultCompleter + WithContextCompletion, mirroring
// handler.Open's wiring).
//
// Usage:
//
//	PG_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go run complbench.go pg -setup 1000 -timeline
//	OG_TEST_DSN='opengauss://user:pass@host:5432/db' go run complbench.go og -setup 5000 -teardown
//	MYSQL_TEST_DSN='user:pass@tcp(host:3306)/test' go run complbench.go mysql -setup 1000
//	MYSQL_TEST_DSN='user@tenant:pass@tcp(host:2881)/test' go run complbench.go mysql -setup 1000 -reps 3
//	OB_ORA_TEST_DSN='sys@tenant:pass@tcp(host:2881)/' BENCH_SCHEMA=SYS go run complbench.go oboracle -setup 1000
//
// Flags:
//
//	-setup N     create bench_t0001..bench_tNNNN (6 cols each), bench_wide
//	             (200 cols) and 20 views, then run the benchmark
//	-teardown    drop all bench_% objects (after benchmarking)
//	-reps R      timed repetitions per case (default 5; first rep is cold)
//	-timeline    probe completion latency right after connect (snapshot
//	             warm-up) at 0/100/300/700/1500/3000/6000ms
//	-no-context  run the legacy tail-matching heuristics only
//	-only A,B    run a subset of cases by name
//
// BENCH_TIMEOUT_MS overrides the metadata reader timeout (default 3000;
// OceanBase wants ~15000). BENCH_LIMIT overrides the row limit (default
// 50000, mirroring the app's completer reader).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "gitcode.com/opengauss/openGauss-connector-go-pq"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/helingjun/obconnector-go"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	mymeta "github.com/xo/usql/drivers/metadata/mysql"
	orameta "github.com/xo/usql/drivers/metadata/oracle"
	pgmeta "github.com/xo/usql/drivers/metadata/postgres"
	"github.com/xo/usql/rline"
)

type benchCase struct {
	name string
	line string
	desc string
}

func main() {
	// support both "complbench pg -setup N" and "complbench -setup N pg"
	args := os.Args[1:]
	flavorArg := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		flavorArg = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("complbench", flag.ExitOnError)
	setup := fs.Int("setup", 0, "create bench_t0001..N tables + bench_wide + 20 views before benchmarking")
	scale := fs.Int("scale", -1, "table count used for case-name derivation (defaults to the -setup value)")
	teardown := fs.Bool("teardown", false, "drop bench_% objects after benchmarking")
	reps := fs.Int("reps", 5, "timed repetitions per case")
	timeline := fs.Bool("timeline", false, "probe warm-up latency after completer creation")
	noContext := fs.Bool("no-context", false, "legacy heuristics only")
	only := fs.String("only", "", "comma-separated case-name filter")
	fs.Parse(args)
	if fs.NArg() != 0 || flavorArg == "" {
		fmt.Fprintln(os.Stderr, "usage: go run complbench.go <pg|og|mysql|oboracle> [flags]")
		fs.PrintDefaults()
		os.Exit(2)
	}
	flavor := flavorArg

	// connect + reader per flavor, mirroring the app's wiring
	var db *sql.DB
	var reader metadata.Reader
	var nsExample string // schema-qualified completion probe
	readerOpts := []metadata.ReaderOption{
		metadata.WithTimeout(benchTimeout()),
		metadata.WithLimit(benchLimit()),
	}
	if os.Getenv("BENCH_LOG_QUERIES") != "" {
		// log every metadata query (and its filters) to stderr
		readerOpts = append(readerOpts, metadata.WithLogger(log.New(os.Stderr, "Q: ", 0)))
	}
	switch flavor {
	case "pg":
		var err error
		db, err = sql.Open("pgx", mustEnv("PG_TEST_DSN"))
		fatalIf(err, "open pgx")
		reader = pgmeta.NewReader()(drivers.DB(db), readerOpts...)
		nsExample = "SELECT * FROM public."
	case "og":
		var err error
		db, err = sql.Open("opengauss", mustEnv("OG_TEST_DSN"))
		fatalIf(err, "open opengauss")
		reader = pgmeta.NewReader()(drivers.DB(db), readerOpts...)
		nsExample = "SELECT * FROM public."
	case "mysql":
		var err error
		db, err = sql.Open("mysql", mustEnv("MYSQL_TEST_DSN"))
		fatalIf(err, "open mysql")
		reader = mymeta.NewReader(db, readerOpts...)
		nsExample = "SELECT * FROM " + benchDBName() + "."
	case "oboracle":
		var err error
		db, err = sql.Open("oboracle", mustEnv("OB_ORA_TEST_DSN"))
		fatalIf(err, "open oboracle")
		reader = orameta.NewReaderQ()(db, readerOpts...)
		nsExample = "SELECT * FROM " + envOr("BENCH_SCHEMA", "SYS") + "."
	default:
		fmt.Fprintf(os.Stderr, "unknown flavor %q\n", flavor)
		os.Exit(2)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	t0 := time.Now()
	fatalIf(db.PingContext(ctx), "ping")
	fmt.Printf("== %s: connected in %v\n", flavor, time.Since(t0).Round(time.Millisecond))

	n := *setup
	if n > 0 {
		setupObjects(ctx, db, flavor, n)
	}
	// case names and teardown are bounded by the explicit scale when given
	nScale := n
	if *scale >= 0 {
		nScale = *scale
	}
	defer func() {
		if *teardown {
			teardownObjects(ctx, db, flavor, nScale)
		}
	}()

	// build the completer exactly like handler.Open does (after tables exist,
	// so the connect-time snapshot sees them)
	opts := []completer.Option{
		completer.WithReader(reader),
		completer.WithLogger(log.New(io.Discard, "", 0)),
	}
	if !*noContext {
		opts = append(opts, completer.WithContextCompletion())
	} else {
		fmt.Println("-- no-context: legacy tail-matching only")
	}
	tc := time.Now()
	c := completer.NewDefaultCompleter(opts...)
	fmt.Printf("== completer built in %v\n", time.Since(tc).Round(time.Microsecond))

	// settle: the lazy cache loads the schema tier on demand and
	// asynchronously, so the empty-word enumeration is answered only once
	// the first load has landed — poll until it is served
	settled := time.Now()
	for {
		if cands, _ := doComplete(c, "SELECT * FROM "); len(cands) > 0 {
			fmt.Printf("== completion cache settled (empty-word enumeration served) after %v\n", time.Since(settled).Round(time.Millisecond))
			break
		}
		if time.Since(settled) > 15*time.Second {
			fmt.Println("== WARNING: snapshot did not settle within 15s; measuring anyway")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if *timeline {
		runTimeline(c)
	}
	runCases(c, nScale, nsExample, *reps, *only)
}

// alreadyExists reports whether err is an object-already-exists error across
// dialects (PG/MySQL "already exists", ORA-00955 "already used").
func alreadyExists(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exist") || strings.Contains(s, "already used")
}

// setupObjects creates the bench tables, wide table and views. Re-runs at a
// larger N keep existing objects (IF NOT EXISTS / OR REPLACE, and
// already-exists errors are tolerated for Oracle-mode flavors).
func setupObjects(ctx context.Context, db *sql.DB, flavor string, n int) {
	ddl, _ := dialects(flavor)
	t0 := time.Now()
	for i := 1; i <= n; i++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(ddl.tbl, i)); err != nil && !alreadyExists(err) {
			fatalIf(err, fmt.Sprintf("create table %d", i))
		}
		if i%500 == 0 {
			fmt.Printf("   ... %d/%d tables (%v elapsed)\n", i, n, time.Since(t0).Round(time.Millisecond))
		}
	}
	var cols strings.Builder
	fmt.Fprintf(&cols, "%s, ", ddl.idCol)
	for i := 1; i <= ddl.wideCols; i++ {
		fmt.Fprintf(&cols, "%s%03d %s, ", ddl.widePrefix, i, ddl.strCol)
	}
	createWide := fmt.Sprintf("CREATE TABLE %s bench_wide (%s)", ddl.ifNE, strings.TrimSuffix(cols.String(), ", "))
	if _, err := db.ExecContext(ctx, createWide); err != nil && !alreadyExists(err) {
		fatalIf(err, "create bench_wide")
	}
	for j := 1; j <= 20; j++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(ddl.view, j)); err != nil && !alreadyExists(err) {
			fatalIf(err, fmt.Sprintf("create view %d", j))
		}
	}
	fmt.Printf("== setup: %d tables + bench_wide (200 cols) + 20 views in %v\n", n, time.Since(t0).Round(time.Millisecond))
}

// teardownObjects drops the bench objects (views first), bounded by n.
func teardownObjects(ctx context.Context, db *sql.DB, flavor string, n int) {
	_, drop := dialects(flavor)
	t0 := time.Now()
	dropped := 0
	for j := 1; j <= 20; j++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(drop.view, j)); err == nil {
			dropped++
		}
	}
	for i := 1; i <= n; i++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(drop.tbl, i)); err == nil {
			dropped++
		}
		if i%500 == 0 {
			fmt.Printf("   ... dropped %d/%d\n", i, n)
		}
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE bench_wide"); err == nil {
		dropped++
	}
	fmt.Printf("== teardown: dropped %d objects in %v\n", dropped, time.Since(t0).Round(time.Millisecond))
}

type dialect struct {
	tbl        string // %d = ordinal
	view       string // %d = ordinal
	idCol      string
	strCol     string
	widePrefix string
	wideCols   int
	ifNE       string // "IF NOT EXISTS" or "" (Oracle-mode)
}

func dialects(flavor string) (create dialect, drop dialect) {
	if flavor == "oboracle" {
		return dialect{
				tbl:        "CREATE TABLE bench_t%04d (id NUMBER(10), c1 VARCHAR2(64), c2 NUMBER(10), c3 VARCHAR2(128), c4 DATE, c5 NUMBER(10,2))",
				view:       "CREATE VIEW bench_v%02d AS SELECT id, c001, c002 FROM bench_wide",
				idCol:      "id NUMBER(10)",
				strCol:     "VARCHAR2(64)",
				widePrefix: "c",
				wideCols:   200,
				ifNE:       "",
			}, dialect{
				tbl:  "DROP TABLE bench_t%04d",
				view: "DROP VIEW bench_v%02d",
			}
	}
	str := "VARCHAR(64)"
	wide := 200
	if flavor == "mysql" {
		// InnoDB caps inline row size (~8126 bytes, innodb_strict_mode=ON):
		// 200 columns fail even as TEXT, ~150 fit — TEXT lives off-page
		str = "TEXT"
		wide = 150
	}
	return dialect{
			tbl:        "CREATE TABLE IF NOT EXISTS bench_t%04d (id INT, c1 VARCHAR(64), c2 INT, c3 VARCHAR(128), c4 TIMESTAMP, c5 DECIMAL(10,2))",
			view:       "CREATE OR REPLACE VIEW bench_v%02d AS SELECT id, c001, c002 FROM bench_wide",
			idCol:      "id INT",
			strCol:     str,
			widePrefix: "c",
			wideCols:   wide,
			ifNE:       "IF NOT EXISTS",
		}, dialect{
			tbl:  "DROP TABLE IF EXISTS bench_t%04d",
			view: "DROP VIEW IF EXISTS bench_v%02d",
		}
}

// runTimeline probes completion latency right after completer construction:
// the first calls may block on fallback DB queries while the snapshot is
// still loading.
func runTimeline(c rline.Completer) {
	fmt.Println("== warm-up timeline (SELECT * FROM bench_w | bench_t0):")
	delays := []int{0, 100, 300, 700, 1500, 3000, 6000}
	start := time.Now()
	for _, d := range delays {
		if wait := time.Duration(d)*time.Millisecond - time.Since(start); wait > 0 {
			time.Sleep(wait)
		}
		cw, tw := doComplete(c, "SELECT * FROM bench_w")
		ct, tt := doComplete(c, "SELECT * FROM bench_t0")
		fmt.Printf("   t=%4dms  bench_w: %3d cands in %-9v  bench_t0: %4d cands in %v\n",
			d, len(cw), tw.Round(time.Microsecond), len(ct), tt.Round(time.Microsecond))
	}
}

// runCases benchmarks each completion context at the current scale.
func runCases(c rline.Completer, n int, nsExample string, reps int, only string) {
	mid := n * 9 / 10
	prefMid := fmt.Sprintf("SELECT * FROM bench_t%02d", mid/100)
	lastName := fmt.Sprintf("bench_t%04d", n)
	cases := []benchCase{
		{"t_all", "SELECT * FROM ", "FROM + empty word: full enumeration + ranking"},
		{"t_pref0", "SELECT * FROM bench_t0", "FROM + prefix matching ~min(N,1000) tables"},
		{"t_prefmid", prefMid, "FROM + prefix in the last 10% of the range (limit probe)"},
		{"t_last", "SELECT * FROM " + lastName, "FROM + prefix of the very last table (limit probe)"},
		{"wide_where", "SELECT * FROM bench_wide WHERE ", "WHERE + unqualified columns of 200-col table"},
		{"wide_alias", "SELECT * FROM bench_wide w WHERE w.c0", "alias-qualified columns (w.c0 → 99 of 200)"},
		{"wide_insert", "INSERT INTO bench_wide (", "INSERT INTO + ( → full column list"},
		{"join_scope", "SELECT * FROM bench_t0001 b JOIN bench_wide w ON ", "JOIN ON: columns of two tables in scope"},
		{"meta_dt", `\dt bench_`, "\\dt with table-name pattern (DB query)"},
		{"ns_qualified", nsExample, "schema./db.-qualified table enumeration (DB query)"},
	}
	if only != "" {
		want := map[string]bool{}
		for _, s := range strings.Split(only, ",") {
			want[strings.TrimSpace(s)] = true
		}
		f := cases[:0]
		for _, c := range cases {
			if want[c.name] {
				f = append(f, c)
			}
		}
		cases = f
	}

	fmt.Printf("== cases (reps=%d, cold=first call, warm=median of the rest):\n", reps)
	fmt.Printf("   %-12s %7s %10s %10s %12s  %s\n", "case", "cands", "cold", "warm(p50)", "warm(max)", "desc")
	for _, tc := range cases {
		var cold time.Duration
		var warm []time.Duration
		var ncands int
		for r := 0; r < reps; r++ {
			cands, el := doComplete(c, tc.line)
			if r == 0 {
				cold = el
				ncands = len(cands)
			} else {
				warm = append(warm, el)
			}
		}
		sort.Slice(warm, func(i, j int) bool { return warm[i] < warm[j] })
		med, max := time.Duration(0), time.Duration(0)
		if len(warm) > 0 {
			med = warm[len(warm)/2]
			max = warm[len(warm)-1]
		}
		fmt.Printf("   %-12s %7d %10v %10v %12v  %s\n", tc.name, ncands, cold.Round(time.Microsecond), med.Round(time.Microsecond), max.Round(time.Microsecond), tc.desc)
	}
}

// doComplete runs the TAB path once (context-aware first, heuristics second,
// exactly like completer.Do) and returns the candidate texts and the elapsed
// time.
func doComplete(c rline.Completer, line string) ([]string, time.Duration) {
	lineR := []rune(line)
	t0 := time.Now()
	cands, _ := c.Do(lineR, len(lineR))
	el := time.Since(t0)
	res := make([]string, len(cands))
	for i, cand := range cands {
		res[i] = cand.Text
	}
	return res, el
}

func benchTimeout() time.Duration {
	if ms := os.Getenv("BENCH_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	}
	return 3 * time.Second
}

func benchLimit() int {
	if s := os.Getenv("BENCH_LIMIT"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	// mirrors the app's completer reader limit (see drivers.NewCompleter)
	return 50000
}

func benchDBName() string {
	if s := os.Getenv("BENCH_DB"); s != "" {
		return s
	}
	return "test"
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "%s not set\n", name)
		os.Exit(2)
	}
	return v
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func fatalIf(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
