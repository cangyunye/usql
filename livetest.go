//go:build ignore

// livetest.go is a manual, untracked harness (see .git/info/exclude) that
// exercises the context-aware SQL completion against real databases through
// the public completer API: completer.NewDefaultCompleter + the
// WithContextCompletion option from candidates.go. No credentials are
// hardcoded — DSNs come from env vars.
//
// Usage:
//
//	PG_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go run livetest.go pg
//	MYSQL_TEST_DSN='user:pass@tcp(host:3306)/db' go run livetest.go mysql
//	MYSQL_TEST_DSN='user@tenant:pass@tcp(host:2881)/db' go run livetest.go mysql
//	OB_ORA_TEST_DSN='sys@tenant:pass@tcp(host:2881)/' LIVETEST_SCHEMA=SYS go run livetest.go oboracle
//
// LIVETEST_TIMEOUT_MS overrides the metadata reader timeout (default 3s;
// OceanBase needs ~15s). LIVETEST_NO_CONTEXT=1 runs the old heuristics only.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/helingjun/obconnector-go"
	_ "gitcode.com/opengauss/openGauss-connector-go-pq"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	mymeta "github.com/xo/usql/drivers/metadata/mysql"
	orameta "github.com/xo/usql/drivers/metadata/oracle"
	pgmeta "github.com/xo/usql/drivers/metadata/postgres"
)

type testCase struct {
	line  string
	start int
}

func casesFor(schema string) []testCase {
	qualified := "SELECT * FROM " + schema + ".zz_c"
	return []testCase{
		{"SELECT * FROM zz_c", len("SELECT * FROM zz_c")},
		{qualified, len(qualified)},
		{"SELECT * FROM zz_cfilm WHERE ", len("SELECT * FROM zz_cfilm WHERE ")},
		{"SELECT * FROM zz_cfilm f WHERE f.", len("SELECT * FROM zz_cfilm f WHERE f.")},
		{"SELECT * FROM zz_cfilm WHERE release_y", len("SELECT * FROM zz_cfilm WHERE release_y")},
		{"SELECT * FROM zz_cfilm WHERE f_na", len("SELECT * FROM zz_cfilm WHERE f_na")},
		{"INSERT INTO zz_cfilm (", len("INSERT INTO zz_cfilm (")},
		{"SELECT * FROM zz_cfilm ", len("SELECT * FROM zz_cfilm ")},
		{"UPDATE zz_cfilm SET na", len("UPDATE zz_cfilm SET na")},
		{"DELETE FROM zz_c", len("DELETE FROM zz_c")},
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run livetest.go <pg|mysql|oboracle|og>")
		os.Exit(2)
	}

	readerOpts := []metadata.ReaderOption{
		metadata.WithTimeout(readerTimeout()),
		metadata.WithLimit(1000),
	}

	var db *sql.DB
	var reader metadata.Reader
	var schema, ddl string
	switch os.Args[1] {
	case "pg":
		var err error
		db, err = sql.Open("pgx", mustEnv("PG_TEST_DSN"))
		fatalIf(err, "open pgx")
		reader = pgmeta.NewReader()(drivers.DB(db), readerOpts...)
		schema, ddl = "public",
			`CREATE TABLE IF NOT EXISTS zz_cfilm (id INT PRIMARY KEY, name TEXT, release_year INT)`
	case "og":
		// openGauss (all compat modes) via its own driver, PG-style catalog
		var err error
		db, err = sql.Open("opengauss", mustEnv("OG_TEST_DSN"))
		fatalIf(err, "open opengauss")
		reader = pgmeta.NewReader()(drivers.DB(db), readerOpts...)
		schema, ddl = "public",
			`CREATE TABLE IF NOT EXISTS zz_cfilm (id INT PRIMARY KEY, name TEXT, release_year INT)`
	case "mysql":
		var err error
		db, err = sql.Open("mysql", mustEnv("MYSQL_TEST_DSN"))
		fatalIf(err, "open mysql")
		reader = mymeta.NewReader(db, readerOpts...)
		schema, ddl = "test",
			`CREATE TABLE IF NOT EXISTS zz_cfilm (id INT PRIMARY KEY, name TEXT, release_year INT)`
	case "oboracle":
		var err error
		db, err = sql.Open("oboracle", mustEnv("OB_ORA_TEST_DSN"))
		fatalIf(err, "open oboracle")
		reader = orameta.NewReaderQ()(db, readerOpts...)
		schema = envOr("LIVETEST_SCHEMA", "SYS")
		ddl = `CREATE TABLE zz_cfilm (id NUMBER PRIMARY KEY, name VARCHAR2(64), release_year NUMBER)`
	default:
		fmt.Fprintf(os.Stderr, "unknown flavor %q\n", os.Args[1])
		os.Exit(2)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fatalIf(db.PingContext(ctx), "ping")
	fmt.Printf("== %s: connected\n", os.Args[1])

	if _, err := db.ExecContext(ctx, ddl); err != nil {
		fmt.Printf("   setup DDL failed (continuing): %v\n", err)
	}
	defer func() {
		if os.Args[1] == "oboracle" {
			db.Exec("DROP TABLE zz_cfilm") // no IF EXISTS in Oracle
		} else {
			db.Exec("DROP TABLE IF EXISTS zz_cfilm")
		}
	}()

	opts := []completer.Option{
		completer.WithReader(reader),
		completer.WithLogger(log.New(io.Discard, "", 0)),
	}
	if os.Getenv("LIVETEST_NO_CONTEXT") == "" {
		opts = append(opts, completer.WithContextCompletion())
	} else {
		fmt.Println("-- LIVETEST_NO_CONTEXT: old heuristics only")
	}
	c := completer.NewDefaultCompleter(opts...)

	for _, tc := range casesFor(schema) {
		start := time.Now()
		got, length := c.Do([]rune(tc.line), tc.start)
		cands := make([]string, len(got))
		for i, r := range got {
			cands[i] = string(r)
		}
		shown := strings.Join(cands, " ")
		if len(shown) > 110 {
			shown = shown[:107] + "..."
		}
		fmt.Printf("[%-52q] len=%d %2d cands (%v): %s\n", tc.line, length, len(cands), time.Since(start).Round(time.Millisecond), shown)
	}
}

func readerTimeout() time.Duration {
	if ms := os.Getenv("LIVETEST_TIMEOUT_MS"); ms != "" {
		var v int
		fmt.Sscanf(ms, "%d", &v)
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	}
	return 3 * time.Second
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
