//go:build !windows

// PTY regression scenarios for the terminal display/input changes made in
// this fork. Each test is a named bug: run them with `go test -run TestPTY`
// from the repository root; they build the usql binary themselves and only
// need sqlite3 (no docker).
//
//	FirstCharacter   the first keystroke after a query was swallowed
//	Paging           plain ";" selects are capped at ROWLIMIT and \more continues exactly
//	CJKLocales       \dt and SELECT must render under CJK/UTF-8 locales
//	Truncation       scripted runs, \g file and filtered queries are never truncated
//	TerminalState    the line discipline is restored when the session ends
//	MenuAtBottom     Tab at the screen bottom still opens the candidate menu
//	ConnsModal       the \conns modal learns the terminal size and repaints cleanly
//	ConnsWheel       the \conns modal owns the mouse wheel and scrolls the list
package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/xo/usql/internal/ptytest"
)

var binPath string

func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skip: pty scenarios do not run on windows")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "usql-pty-*")
	if err != nil {
		fmt.Println("tempdir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "usql")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		fmt.Printf("build: %v\n%s\n", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newDB creates a sqlite3 database with a 300-row table and returns its
// DSN. 300 rows makes the ROWLIMIT cap (100) and the untruncated count
// clearly distinguishable.
func newDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "t.db")
	dsn := "sqlite3://" + db // db is absolute: exactly three slashes total
	var vals strings.Builder
	vals.WriteString("INSERT INTO t VALUES ")
	for i := 1; i <= 300; i++ {
		if i > 1 {
			vals.WriteString(",")
		}
		fmt.Fprintf(&vals, "(%d,'name-%d')", i, i)
	}
	if out, err := exec.Command(binPath, dsn,
		"-c", "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT)",
		"-c", "CREATE TABLE invoices (n int)",
		"-c", vals.String()).CombinedOutput(); err != nil {
		t.Fatalf("setup db: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(db) })
	return dsn
}

// newSession spawns usql interactively against dsn with the given extra
// environment (LC_ALL and friends).
func newSession(t *testing.T, dsn string, extraEnv ...string) *ptytest.Terminal {
	t.Helper()
	return ptytest.New(t, ptytest.Options{
		Args: []string{binPath, dsn},
		Env:  append(os.Environ(), extraEnv...),
	})
}

func idCount(text string) int {
	return len(regexp.MustCompile(`name-\d+`).FindAllString(text, -1))
}

// FirstCharacter: after a query's output, the first keystroke of the next
// input was swallowed (the pre-TUI-fork readline never did this). Type
// three keys, one at a time, and require all three on the line.
func TestPTYFirstCharacter(t *testing.T) {
	dsn := newDB(t)
	tm := newSession(t, dsn)
	defer tm.WaitExit(10 * time.Second)

	tm.WaitFor(`Type "help"`, 30*time.Second)
	tm.SendLine("SELECT * FROM t;")
	tm.WaitFor(`more available`, 30*time.Second) // 100-row cap reached
	time.Sleep(300 * time.Millisecond)           // let the prompt settle

	tm.SendKeys("abc")
	tm.WaitFor(`abc[^\n]*\z`, 10*time.Second)

	text := tm.Text()
	if regexp.MustCompile(`=> b[^c]|=> c`).MatchString(text) {
		t.Fatalf("first character swallowed:\n%s", ptytest.Tail(text, 400))
	}

	tm.Ctrl('c') // clear the line
	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// Paging: an interactive, unfiltered select is capped at ROWLIMIT (100)
// rows with a continuation hint, and \more fetches the exact next pages —
// 100 + 100 + 100 = the full 300 rows, no overlap, no gaps.
func TestPTYPaging(t *testing.T) {
	dsn := newDB(t)
	tm := newSession(t, dsn)
	defer tm.WaitExit(10 * time.Second)

	tm.WaitFor(`Type "help"`, 30*time.Second)
	tm.SendLine("SELECT * FROM t;")
	text := tm.WaitFor(`more available`, 30*time.Second)
	if n := idCount(text); n != 100 {
		t.Fatalf("page 1: got %d rows, want 100", n)
	}

	tm.SendLine("\\more")
	text = tm.WaitFor(`rows 101-200`, 30*time.Second)
	if n := idCount(text); n != 200 {
		t.Fatalf("after \\more: got %d rows, want 200", n)
	}

	tm.SendLine("\\more 100")
	text = tm.WaitFor(`name-300`, 30*time.Second)
	if n := idCount(text); n != 300 {
		t.Fatalf("after \\more 100: got %d rows, want 300", n)
	}
	if got := strings.Count(text, "more available"); got != 2 {
		t.Fatalf("continuation hints: got %d, want 2 (pages exhausted)", got)
	}
	if ids := regexp.MustCompile(`name-(\d+)`).FindAllStringSubmatch(text, -1); len(ids) != 300 {
		t.Fatalf("unique rows: got %d, want 300", len(ids))
	}

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// CJKLocales: runewidth treats any zh/ja/ko locale as east-asian, which
// used to make tblfmt reject the unicode line style on every interactive
// table ("invalid line style"). Tables must render under all of them; the
// borders fall back to ascii unless the user opts out with
// RUNEWIDTH_EASTASIAN=0.
func TestPTYCJKLocales(t *testing.T) {
	dsn := newDB(t)
	for _, env := range [][]string{
		{"LC_ALL=zh_CN.UTF-8", "LANG=zh_CN.UTF-8"},
		{"LC_ALL=ja_JP.UTF-8", "LANG=ja_JP.UTF-8"},
		{"LC_ALL=zh_CN.GB18030", "LANG=zh_CN.GB18030"},
		{"LC_ALL=C.UTF-8", "LANG=C.UTF-8"},
		{"LC_ALL=zh_CN.UTF-8", "LANG=zh_CN.UTF-8", "RUNEWIDTH_EASTASIAN=0"},
	} {
		t.Run(strings.Join(env, " "), func(t *testing.T) {
			tm := newSession(t, dsn, env...)
			defer tm.WaitExit(10 * time.Second)

			tm.WaitFor(`Type "help"`, 30*time.Second)
			tm.SendLine("\\dt")
			tm.WaitFor(`List of relations`, 30*time.Second)
			tm.SendLine("SELECT 1 AS x;")
			text := tm.WaitFor(`\(1 row\)`, 30*time.Second)

			if strings.Contains(text, "invalid line style") {
				t.Fatalf("invalid line style under %v", env)
			}
			if strings.Contains(text, "error:") {
				t.Fatalf("unexpected error under %v", env)
			}
			tm.SendLine("\\q")
		})
	}
}

// Truncation: only interactive terminal output is capped. Scripted runs
// (-c), \g to a file and filtered selects must be complete.
func TestPTYTruncationBoundaries(t *testing.T) {
	dsn := newDB(t)

	// scripted (-c) mode is never rewritten
	out, err := exec.Command(binPath, dsn, "-c", "SELECT * FROM t;").CombinedOutput()
	if err != nil {
		t.Fatalf("scripted select: %v\n%s", err, out)
	}
	if n := idCount(string(out)); n != 300 {
		t.Fatalf("scripted select: got %d rows, want 300", n)
	}

	tm := newSession(t, dsn)
	defer tm.WaitExit(10 * time.Second)
	tm.WaitFor(`Type "help"`, 30*time.Second)

	// \g to a file is never truncated
	file := "/tmp/gprobe-out.txt"
	tm.SendLine("SELECT * FROM t\\g " + file)
	tm.WaitFor(`=>[^\n]*\z`, 30*time.Second)
	time.Sleep(300 * time.Millisecond)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read \\g file: %v", err)
	}
	if n := idCount(string(data)); n != 300 {
		t.Fatalf("\\g file: got %d rows, want 300", n)
	}

	// a filtered select is not rewritten and shows everything it matches
	tm.SendLine("SELECT * FROM t WHERE id <= 150;")
	text := tm.WaitFor(`\(150 rows\)`, 30*time.Second)
	if strings.Contains(text, "more available") {
		t.Fatal("filtered select was paged")
	}
	if n := idCount(text); n != 150 {
		t.Fatalf("filtered select: got %d rows, want 150", n)
	}

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// TerminalState: at the prompt the engine holds ECHO|ICANON off and ISIG
// off too (the read takes Ctrl-C as a byte that clears the line; between
// reads ISIG is back on, so Ctrl-C cancels a running query), and when the
// session ends the original line discipline is restored.
func TestPTYTerminalState(t *testing.T) {
	dsn := newDB(t)
	tm := newSession(t, dsn)
	defer tm.WaitExit(10 * time.Second)
	if !tm.HasSlaveTermios() {
		tm.KillIfRunning()
		t.Skip("slave termios not available on this platform")
	}

	tm.WaitFor(`Type "help"`, 30*time.Second)
	time.Sleep(300 * time.Millisecond) // prompt settled

	echo, icanon, isig, err := tm.SlaveTermiosFlags()
	if err != nil {
		t.Fatalf("termios at prompt: %v", err)
	}
	if echo || icanon {
		t.Fatalf("line discipline at prompt: echo=%v icanon=%v, want both off", echo, icanon)
	}
	if isig {
		t.Fatal("line discipline at prompt: isig on, want off (reads take Ctrl-C as a byte)")
	}

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	echo, icanon, isig, err = tm.SlaveTermiosFlags()
	if err != nil {
		t.Fatalf("termios after exit: %v", err)
	}
	if !echo || !icanon || !isig {
		t.Fatalf("line discipline after exit: echo=%v icanon=%v isig=%v, want all restored on", echo, icanon, isig)
	}
}

// MenuAtBottom: after output pushes the prompt to the terminal's last row,
// an explicit Tab must still open the candidate menu (the frame scrolls a
// few history rows once). The headroom-only cap made Tab a silent no-op in
// the most common prompt position, so completion looked "gone". Cols is
// wide so the long temp-path prompt never soft-wraps (a rendering artifact
// unrelated to the menu); Rows is short so the big result fills the screen.
func TestPTYMenuAtBottom(t *testing.T) {
	dsn := newDB(t)
	// isolate history: a developer's real ~/.usql_history seeds word
	// candidates and would make the menu contents nondeterministic
	home := t.TempDir()
	tm := ptytest.New(t, ptytest.Options{
		Args: []string{binPath, dsn},
		Env:  append(os.Environ(), "HOME="+home, "USQL_HISTORY="+filepath.Join(home, "hist")),
		Rows: 24, Cols: 220,
	})
	defer tm.WaitExit(20 * time.Second)
	tm.WaitFor(`Type "help"`, 30*time.Second)

	// a second table (created in newDB) whose name is NOT fully typed
	// below: completing the exact name of table t would leave zero
	// candidate text to list

	// fill the screen: the prompt ends on the bottom row
	tm.SendLine("SELECT * FROM t;")
	tm.WaitFor(`more available`, 30*time.Second)
	time.Sleep(300 * time.Millisecond)

	// type a table prefix, then Tab: a candidate menu must appear even
	// though there is no headroom below the cursor
	for _, c := range "select * from inv" {
		tm.SendKeys(string(c))
		time.Sleep(80 * time.Millisecond)
	}
	tm.SendRaw("\t")
	tm.WaitFor(`▸ main\.invoices\s+table|> main\.invoices\s+table`, 10*time.Second)

	// accept the selection with Tab: the line completes and stays operable
	// after the forced menu scrolled the history
	tm.SendRaw("\t")
	tm.WaitFor(`=> select \* from main\.invoices`, 10*time.Second)
	tm.SendLine(" WHERE n IS NULL;")
	tm.WaitFor(`\(0 rows\)`, 30*time.Second)

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// pgKeywordToURL converts a lib/pq keyword DSN into a postgres:// URL with
// the given application_name — the CLI accepts URLs, not keyword DSNs.
func pgKeywordToURL(dsn, appName string) string {
	params := map[string]string{"port": "5432"}
	for _, kv := range strings.Fields(dsn) {
		if i := strings.IndexByte(kv, '='); i > 0 {
			params[kv[:i]] = kv[i+1:]
		}
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(params["user"], params["password"]),
		Host:   params["host"] + ":" + params["port"],
		Path:   "/" + params["dbname"],
	}
	q := url.Values{}
	if ssl, ok := params["sslmode"]; ok {
		q.Set("sslmode", ssl)
	}
	q.Set("application_name", appName)
	u.RawQuery = q.Encode()
	return u.String()
}

// TestPTYMetaPool verifies the metadata-pool wiring end to end against a
// real PostgreSQL (gated by USQL_LIVE_PG, like the completer live tests):
//
//	connections  the interactive session holds two connections — the
//	             statement pool and the completer's metadata pool
//	replay       after SET search_path, completion resolves the new scope
//	             (the replayed statement reached the metadata pool)
func TestPTYMetaPool(t *testing.T) {
	dsn := os.Getenv("USQL_LIVE_PG")
	if dsn == "" {
		t.Skip("USQL_LIVE_PG not set")
	}
	// application_name marks the session's two connections in
	// pg_stat_activity; the marker schema gives the replayed scope a
	// distinct table to complete
	obs, err := sql.Open("postgres", dsn+" application_name=usql_pty_obs")
	if err != nil {
		t.Skipf("open observer: %v", err)
	}
	defer obs.Close()
	// the keyword DSN may not carry application_name when the driver
	// rejects unknown params — strip and retry via Exec params is
	// overkill: lib/pq accepts application_name
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS meta_replay_pty CASCADE`,
		`CREATE SCHEMA meta_replay_pty`,
		`CREATE TABLE meta_replay_pty.zed_marker (id int)`,
	} {
		if _, err := obs.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { obs.Exec(`DROP SCHEMA IF EXISTS meta_replay_pty CASCADE`) })

	home := t.TempDir()
	tm := ptytest.New(t, ptytest.Options{
		Args: []string{binPath, pgKeywordToURL(dsn, "usql_pty")},
		Env:  append(os.Environ(), "HOME="+home, "USQL_HISTORY="+filepath.Join(home, "hist")),
		Rows: 24, Cols: 220,
	})
	defer tm.WaitExit(20 * time.Second)
	tm.WaitFor(`Type "help"`, 30*time.Second)

	// replay: switch the scope on the statement pool; the metadata pool
	// gets the same statement, so completion resolves the new schema's
	// table. (The metadata pool connects lazily — connecting issues zero
	// metadata queries — so its physical connection exists only after
	// this replay or the first load.)
	tm.SendLine("SET search_path = meta_replay_pty;")
	tm.WaitFor(`SET`, 10*time.Second)
	time.Sleep(500 * time.Millisecond)

	// connections: the session must hold two — statement + metadata pool
	deadline := time.Now().Add(10 * time.Second)
	var n int
	for {
		if err := obs.QueryRow(
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = 'usql_pty'`,
		).Scan(&n); err != nil {
			t.Fatalf("count connections: %v", err)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session holds %d connections with application_name usql_pty, want 2 (statement + metadata pool)", n)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, c := range "select * from z" {
		tm.SendKeys(string(c))
		time.Sleep(80 * time.Millisecond)
	}
	tm.SendRaw("\t")
	tm.WaitFor(`zed[_ ]marker`, 15*time.Second)

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// MenuEnter: with the typing-time completion menu open, Enter must submit
// the line. The menu-open route funneled Enter into handleEditKey, which set
// done but never quit the bubbletea program: the session hung with the
// prompt frozen and every later keystroke silently swallowed.
func TestPTYMenuEnterSubmits(t *testing.T) {
	dsn := newDB(t)
	tm := newSession(t, dsn)
	defer tm.WaitExit(10 * time.Second)

	tm.WaitFor(`Type "help"`, 30*time.Second)
	time.Sleep(300 * time.Millisecond)

	// type the full table name slowly so the typing-time completion menu
	// pops open mid-word and is still open when Enter lands
	for _, c := range "select * from invoices" {
		tm.SendKeys(string(c))
		time.Sleep(90 * time.Millisecond)
	}
	tm.WaitFor(`▸ main\.invoices`, 10*time.Second)

	// Enter with the menu open: the line submits — the statement has no
	// semicolon, so the continuation prompt must appear and typing must
	// continue (pre-fix this wedged the session instead)
	tm.SendKeys("\r")
	tm.WaitFor(`->`, 10*time.Second)
	tm.SendLine(";")
	tm.WaitFor(`\(0 rows\)`, 30*time.Second)

	tm.SendLine("\\q")
	if err := tm.WaitExit(10 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// seedConnsConfig writes 24 named connections with wide host/database
// values, so the \conns table's natural width (~150 columns) far exceeds
// the 80-column test terminal. The config directory follows
// os.UserConfigDir: $XDG_CONFIG_HOME/usql on unix, ~/Library/Application
// Support/usql on darwin (XDG is ignored there).
func seedConnsConfig(t *testing.T, home, xdg string) {
	t.Helper()
	dir := filepath.Join(xdg, "usql")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(home, "Library", "Application Support", "usql")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("connections:\n")
	fmt.Fprintf(&b, "  book_sqlite: %q\n", "sqlite3:///Users/sinvigil/prog/usql/testdata/booktest/booktest.db")
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&b, "  conn%02d: %q\n", i, fmt.Sprintf("mysql://dev@mysql-%02d.corp.example.com:3306/testdb", i))
	}
	for _, c := range [][2]string{
		{"csvq_report", "csvq:///tmp/sales_report"},
		{"dm8_local", "mysql://SYSDBA@localhost:5236/DMSERVER"},
		{"mysql_test", "mysql://dev@mysql-test.corp.example.com:3306/testdb"},
		{"ob_oracle", "oracle://usqlora@10.20.30.41:1521/ORCLPDB1"},
		{"obmysql", "mysql://root@10.20.30.40:2881/oceanbase"},
		{"pg_local", "postgres://sinvigil@localhost:5432/postgres"},
		{"pg_prod", "postgres://analytics@pg-prod-cluster.internal.example.com:5432/sales?sslmode=require"},
	} {
		fmt.Fprintf(&b, "  %s: %q\n", c[0], c[1])
	}
	if err := os.WriteFile(filepath.Join(dir, "connections.yaml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ConnsModal: the \conns TUI modal must learn the real terminal geometry.
// It used to render into the guarded Stdout writer, so bubbletea never saw
// the window size, skipped its overwide-line truncation, and soft-wrapped
// the ~150-column table at 80 columns — every repaint then landed on
// shifted rows and stacked stale fragments (the "garbled, overlapping
// list" report). With the size known the table truncates with an ellipsis
// and long lists scroll inside a height-fitting window.
func TestPTYConnsModal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty scenarios do not run on windows")
	}
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	seedConnsConfig(t, home, xdg)
	tm := ptytest.New(t, ptytest.Options{
		Args: []string{binPath},
		Env: append(os.Environ(),
			"HOME="+home,
			"XDG_CONFIG_HOME="+xdg,
			"USQL_HISTORY="+filepath.Join(home, "hist"),
		),
		Rows: 24, Cols: 80,
	})
	defer tm.WaitExit(20 * time.Second)
	tm.WaitFor(`Type "help"`, 30*time.Second)
	time.Sleep(300 * time.Millisecond)

	tm.SendLine("\\conns")
	tm.WaitFor(`d delete  q quit`, 15*time.Second)
	time.Sleep(500 * time.Millisecond) // modal settled

	// sorted names: book_sqlite, conn01..conn16, csvq_report, dm8_local,
	// mysql_test, ob_oracle, obmysql, pg_local, pg_prod — 24 rows, of which
	// 18 fit in a 24-row terminal. 19 downs put the selection (index 19,
	// mysql_test) on the last visible row and scroll conn01/book_sqlite off.
	for i := 0; i < 19; i++ {
		tm.SendRaw("\x1b[B")
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	// the renderer diffs frames and skips unchanged lines, so the raw bytes
	// of the last incremental repaint don't carry the whole screen; toggle
	// the delete confirmation (d opens it, n cancels) to force one full
	// frame rewrite to snapshot against
	tm.SendKeys("d")
	time.Sleep(400 * time.Millisecond)
	tm.SendKeys("n")
	time.Sleep(500 * time.Millisecond)

	raw := string(tm.Raw())
	start := strings.Index(raw, "\x1b[?1049h")
	if start < 0 {
		t.Fatal("modal never entered the alt screen")
	}
	modal := raw[start:]

	// no modal line may exceed the terminal width: a wider line would mean
	// the width never reached the renderer and rows soft-wrap (the bug).
	// Frames begin with a cursor-home, not a newline, so home positions
	// become line breaks before stripping (otherwise the tail of one frame
	// would concatenate with the head of the next)
	flat := strings.ReplaceAll(modal, "\x1b[H", "\n")
	for _, line := range strings.Split(ptytest.StripANSI(flat), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if w := ansi.StringWidth(line); w > 80 {
			t.Fatalf("modal line is %d cells wide (> 80): %q", w, line)
		}
	}

	// the last repaint must be a complete, windowed frame: full borders,
	// hints, no scrolled-off rows, and the highlight on the selected row
	final := modal[strings.LastIndex(modal, "\x1b[H"):]
	text := ptytest.StripANSI(final)
	for _, want := range []string{"╭", "╰", "a add"} {
		if !strings.Contains(text, want) {
			t.Fatalf("final frame lost %q after walking 19 rows:\n%s", want, ptytest.Tail(text, 800))
		}
	}
	if strings.Contains(text, "book_sqlite") {
		t.Fatal("final frame still shows rows scrolled off the window")
	}
	if !strings.Contains(text, "…") {
		t.Fatal("final frame has no truncated cells; the width clamp did not engage")
	}
	var highlighted string
	for _, line := range strings.Split(final, "\n") {
		// the selected row's background (bold may merge into one SGR: 1;48;5;236)
		if strings.Contains(line, "48;5;236m") {
			highlighted += ptytest.StripANSI(line)
		}
	}
	// row 20 (names[19], mysql_test; the name cell may be truncated) is the
	// selected row after 19 downs, and it must carry the highlight style
	if !strings.Contains(highlighted, "│20│mysql") {
		t.Fatalf("selection highlight not on row 20 (mysql_test) after 19 downs (got %q)", strings.TrimSpace(highlighted))
	}
	if n := strings.Count(final, "\n"); n > 25 {
		t.Fatalf("final frame is %d lines tall, exceeds the 24-row terminal", n)
	}

	tm.SendKeys("q") // leaves the manager, back at the prompt
	time.Sleep(300 * time.Millisecond)
	tm.SendLine("\\q")
	if err := tm.WaitExit(15 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// ConnsWheel: the \conns modal must own the mouse wheel. Without mouse
// reporting the terminal either translates wheel turns into arrow bursts at
// the renderer or scrolls its own scrollback, dragging pre-modal screen
// content over the alt screen (the "overlapping/lost rows on mouse move"
// report). With reporting enabled the wheel arrives as SGR sequences and
// scrolls the selection like the arrow keys do.
func TestPTYConnsWheel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty scenarios do not run on windows")
	}
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	seedConnsConfig(t, home, xdg)
	tm := ptytest.New(t, ptytest.Options{
		Args: []string{binPath},
		Env: append(os.Environ(),
			"HOME="+home,
			"XDG_CONFIG_HOME="+xdg,
			"USQL_HISTORY="+filepath.Join(home, "hist"),
		),
		Rows: 24, Cols: 80,
	})
	defer tm.WaitExit(20 * time.Second)
	tm.WaitFor(`Type "help"`, 30*time.Second)
	time.Sleep(300 * time.Millisecond)

	tm.SendLine("\\conns")
	tm.WaitFor(`d delete  q quit`, 15*time.Second)
	time.Sleep(500 * time.Millisecond)

	// mouse reporting must be on, or real terminals keep the wheel for
	// themselves (scrollback over the alt screen / arrow bursts)
	if raw := string(tm.Raw()); !strings.Contains(raw, "\x1b[?1002h") {
		t.Fatal("modal did not enable mouse reporting")
	}

	// sorted names: book_sqlite(0), conn01..conn16(1..16), ... — three
	// wheel-down turns put the selection on index 3 (row 4, conn03)
	for i := 0; i < 3; i++ {
		tm.SendRaw("\x1b[<65;10;5M") // SGR wheel down
		time.Sleep(150 * time.Millisecond)
	}
	// the renderer skips unchanged lines: toggle the delete confirm (d, n)
	// to force a full-frame rewrite the assertions can read
	tm.SendKeys("d")
	time.Sleep(300 * time.Millisecond)
	tm.SendKeys("n")
	time.Sleep(500 * time.Millisecond)
	if hl := connsHighlighted(tm); !strings.Contains(hl, "conn03") {
		t.Fatalf("wheel-down selection not on row 4 (conn03), got %q", strings.TrimSpace(hl))
	}

	// two wheel-up turns: back to index 1 (row 2, conn01)
	for i := 0; i < 2; i++ {
		tm.SendRaw("\x1b[<64;10;5M") // SGR wheel up
		time.Sleep(150 * time.Millisecond)
	}
	tm.SendKeys("d")
	time.Sleep(300 * time.Millisecond)
	tm.SendKeys("n")
	time.Sleep(500 * time.Millisecond)
	if hl := connsHighlighted(tm); !strings.Contains(hl, "conn01") {
		t.Fatalf("wheel-up selection not on row 2 (conn01), got %q", strings.TrimSpace(hl))
	}

	// the wheel path repaints like any other: no line may exceed the width
	raw := string(tm.Raw())
	flat := strings.ReplaceAll(raw[strings.Index(raw, "\x1b[?1049h"):], "\x1b[H", "\n")
	for _, line := range strings.Split(ptytest.StripANSI(flat), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if w := ansi.StringWidth(line); w > 80 {
			t.Fatalf("modal line is %d cells wide (> 80): %q", w, line)
		}
	}

	tm.SendKeys("q")
	time.Sleep(300 * time.Millisecond)
	tm.SendLine("\\q")
	if err := tm.WaitExit(15 * time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// connsHighlighted returns the text of the last full repaint's highlighted
// row (the d/n confirm toggle before the snapshot forces that repaint).
func connsHighlighted(tm *ptytest.Terminal) string {
	final := string(tm.Raw())[strings.LastIndex(string(tm.Raw()), "\x1b[H"):]
	var hl string
	for _, line := range strings.Split(final, "\n") {
		if strings.Contains(line, "48;5;236m") {
			hl += ptytest.StripANSI(line)
		}
	}
	return hl
}

// newShortDB creates a sqlite3 database under a short directory: the prompt
// is the DSN, and the paste scenarios assert on full-line echoes, so the
// prompt must leave terminal width for the statement.
func newShortDB(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "up-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db := filepath.Join(dir, "p.db")
	dsn := "sqlite3://" + db
	if out, err := exec.Command(binPath, dsn,
		"-c", "SELECT 1").CombinedOutput(); err != nil {
		t.Fatalf("setup db: %v\n%s", err, out)
	}
	return dsn
}

// TestPTYPasteMultiline: a bracketed multi-line paste replays line by line —
// every line echoes in full with its own prompt, and the pasted statements
// execute in order. The regression: the whole paste used to land in the
// single-line buffer, the rendering kept only its last line visible, and the
// lines after the first newline were dropped entirely.
func TestPTYPasteMultiline(t *testing.T) {
	dsn := newShortDB(t)
	tm := newSession(t, dsn)
	defer tm.KillIfRunning()
	tm.WaitFor(`sqlite`, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	const stmts = "CREATE TABLE p1(v int);\nINSERT INTO p1 VALUES (4242);\nSELECT v FROM p1;\n"
	tm.SendRaw("\x1b[200~" + stmts + "\x1b[201~")
	tm.WaitFor(`\(1 row\)`, 10*time.Second)

	out := ptytest.StripANSI(string(tm.Raw()))
	for _, want := range []string{
		"CREATE TABLE p1(v int);",
		"INSERT INTO p1 VALUES (4242);",
		"SELECT v FROM p1;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("paste echo missing %q:\n%s", want, out)
		}
	}
	// the statements executed in order: the table exists and holds the row
	tm.SendLine("SELECT COUNT(*) FROM p1")
	tm.WaitFor(`\(1 row\)`, 10*time.Second)
	out = ptytest.StripANSI(string(tm.Raw()))
	if !strings.Contains(out, "4242") || !strings.Contains(out, "2") {
		t.Errorf("pasted statements did not execute in order:\n%s", out)
	}
	tm.Ctrl('d')
}

// TestPTYPasteSingleLine: a bracketed paste without a trailing newline
// inserts into the buffer and waits — nothing executes until the user's
// Enter.
func TestPTYPasteSingleLine(t *testing.T) {
	dsn := newShortDB(t)
	tm := newSession(t, dsn)
	defer tm.KillIfRunning()
	tm.WaitFor(`sqlite`, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	tm.SendRaw("\x1b[200~SELECT 4242 AS v;\x1b[201~")
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(ptytest.StripANSI(string(tm.Raw())), "(1 row)") {
		t.Fatalf("paste without newline executed on its own:\n%s",
			ptytest.StripANSI(string(tm.Raw())))
	}
	tm.SendKeys("\r")
	tm.WaitFor(`\(1 row\)`, 10*time.Second)
	tm.Ctrl('d')
}

// TestPTYListStreaming: a large \dt listing renders its rows and the
// footer counts them; Ctrl-C during the listing leaves the session
// operable. The streaming path encodes rows as the server produces them
// instead of after the whole set is materialized.
func TestPTYListStreaming(t *testing.T) {
	dsn := newShortDB(t)
	// 120 tables via one script file
	sql := &strings.Builder{}
	for i := 0; i < 120; i++ {
		fmt.Fprintf(sql, "CREATE TABLE s%d(id int);\n", i)
	}
	f := filepath.Join(t.TempDir(), "setup.sql")
	if err := os.WriteFile(f, []byte(sql.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(binPath, dsn, "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}

	tm := newSession(t, dsn)
	defer tm.KillIfRunning()
	tm.WaitFor(`sqlite`, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	tm.SendLine("\\dt")
	tm.WaitFor(`\(120 rows\)`, 30*time.Second)
	out := ptytest.StripANSI(string(tm.Raw()))
	for _, want := range []string{"s0", "s119", "List of relations"} {
		if !strings.Contains(out, want) {
			t.Errorf("streamed \\dt missing %q", want)
		}
	}
	if n := strings.Count(out, "(120 rows)"); n != 1 {
		t.Errorf("footer rendered %d times, want once", n)
	}

	// the session stays operable after the listing
	tm.SendLine("SELECT 4242")
	tm.WaitFor(`4242`, 10*time.Second)
	tm.Ctrl('d')
}

// TestPTYListInterrupt: Ctrl-C during a listing cancels it and the
// session comes back with a working prompt.
func TestPTYListInterrupt(t *testing.T) {
	dsn := newShortDB(t)
	sql := &strings.Builder{}
	for i := 0; i < 300; i++ {
		fmt.Fprintf(sql, "CREATE TABLE b%d(id int);\n", i)
	}
	f := filepath.Join(t.TempDir(), "setup.sql")
	if err := os.WriteFile(f, []byte(sql.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(binPath, dsn, "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}

	tm := newSession(t, dsn)
	defer tm.KillIfRunning()
	tm.WaitFor(`sqlite`, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	tm.SendLine("\\dt")
	time.Sleep(150 * time.Millisecond)
	tm.Ctrl('c')
	time.Sleep(500 * time.Millisecond)
	// the prompt must be back and the session usable
	tm.SendLine("SELECT 4242")
	tm.WaitFor(`4242`, 10*time.Second)
	tm.Ctrl('d')
}
