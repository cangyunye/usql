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
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

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

// TerminalState: between reads the engine holds ECHO|ICANON off (and ISIG
// on, so Ctrl-C cancels a running query), and when the session ends the
// original line discipline is restored.
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
	if !isig {
		t.Fatal("line discipline at prompt: isig off, want on (Ctrl-C must cancel queries)")
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
