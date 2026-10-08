package completer_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	_ "github.com/xo/usql/internal"
	"github.com/xo/usql/rline"
)

// TestLivePrivVisibility pins the completion privilege contract against real
// servers: an object the login holds no privilege on must not complete —
// neither its namespace in the FROM tier nor the object itself after a
// qualified "schema." — and after a grant plus Invalidate it must. The gate
// user needs the rights to create schemas/users (like TestLiveMetaPoolReplay).
//
// Skipped when the corresponding USQL_LIVE_* DSN is unset.
func TestLivePrivVisibility(t *testing.T) {
	ran := false
	if dsn := os.Getenv("USQL_LIVE_PG"); dsn != "" {
		ran = true
		t.Run("postgres", func(t *testing.T) {
			runPrivVisibility(t, "postgres", dsn,
				func(t *testing.T, db *sql.DB) string {
					execLive(t, db,
						`DROP ROLE IF EXISTS usql_priv_ltd`,
						`DROP SCHEMA IF EXISTS usql_priv_test CASCADE`,
						`CREATE ROLE usql_priv_ltd LOGIN PASSWORD 'PrivTest_123'`,
						`CREATE SCHEMA usql_priv_test`,
						`REVOKE ALL ON SCHEMA usql_priv_test FROM PUBLIC`,
						`CREATE TABLE usql_priv_test.usql_priv_marker (id int)`,
					)
					t.Cleanup(func() {
						db.Exec(`DROP SCHEMA IF EXISTS usql_priv_test CASCADE`)
						db.Exec(`DROP ROLE IF EXISTS usql_priv_ltd`)
					})
					// lib/pq and pgx: a repeated keyword — the last one wins
					return dsn + " user=usql_priv_ltd password=PrivTest_123"
				},
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`GRANT USAGE ON SCHEMA usql_priv_test TO usql_priv_ltd`,
						`GRANT SELECT ON usql_priv_test.usql_priv_marker TO usql_priv_ltd`,
					)
				},
			)
		})
	}
	if dsn := os.Getenv("USQL_LIVE_MYSQL"); dsn != "" {
		ran = true
		t.Run("mysql", func(t *testing.T) {
			runPrivVisibility(t, "mysql", dsn,
				func(t *testing.T, db *sql.DB) string {
					execLive(t, db,
						`CREATE DATABASE IF NOT EXISTS usql_priv_test`,
						`CREATE TABLE IF NOT EXISTS usql_priv_test.usql_priv_marker (id int)`,
						`CREATE USER IF NOT EXISTS 'usql_priv_ltd'@'%' IDENTIFIED BY 'PrivTest_123'`,
					)
					t.Cleanup(func() {
						db.Exec(`DROP USER IF EXISTS 'usql_priv_ltd'@'%'`)
						db.Exec(`DROP DATABASE IF EXISTS usql_priv_test`)
					})
					// user:pass@tcp(host:port)/db — swap the credentials and
					// drop the default database: the limited login holds no
					// privilege on the gate DSN's database yet
					i := strings.LastIndex(dsn, "@")
					if i < 0 {
						t.Fatalf("mysql DSN without credentials: %s", dsn)
					}
					tail := dsn[i+1:]
					if j := strings.Index(tail, ")/"); j >= 0 {
						tail = tail[:j+2]
					}
					return "usql_priv_ltd:PrivTest_123@" + tail
				},
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`GRANT SELECT ON usql_priv_test.* TO 'usql_priv_ltd'@'%'`,
					)
				},
			)
		})
	}
	if dsn := os.Getenv("USQL_LIVE_ORACLE"); dsn != "" {
		ran = true
		t.Run("oracle", func(t *testing.T) {
			runPrivVisibility(t, "oracle", dsn,
				func(t *testing.T, db *sql.DB) string {
					execLive(t, db,
						`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_PRIV_LTD CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
						`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_PRIV_TEST CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
						`CREATE USER USQL_PRIV_LTD IDENTIFIED BY "PrivTest_123"`,
						`GRANT CREATE SESSION TO USQL_PRIV_LTD`,
						`CREATE USER USQL_PRIV_TEST IDENTIFIED BY "PrivTest_123" QUOTA UNLIMITED ON USERS`,
						`CREATE TABLE USQL_PRIV_TEST.USQL_PRIV_MARKER (ID NUMBER)`,
					)
					t.Cleanup(func() {
						db.Exec(`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_PRIV_LTD CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`)
						db.Exec(`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_PRIV_TEST CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`)
					})
					return regexp.MustCompile(`^(oracle://)[^@]+@`).ReplaceAllString(
						dsn, `${1}USQL_PRIV_LTD:PrivTest_123@`)
				},
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`GRANT SELECT ON USQL_PRIV_TEST.USQL_PRIV_MARKER TO USQL_PRIV_LTD`,
					)
				},
			)
		})
	}
	if !ran {
		t.Skip("USQL_LIVE_MYSQL / USQL_LIVE_PG / USQL_LIVE_ORACLE not set")
	}
}

// runPrivVisibility runs one family through the privilege contract: the
// completer runs on the limited login's pool, the locked marker is absent
// before the grant and offered after it.
func runPrivVisibility(t *testing.T, driver, dsn string, setup func(*testing.T, *sql.DB) string, grant func(*testing.T, *sql.DB)) {
	t.Helper()
	var u *dburl.URL
	switch driver {
	case "oracle":
		var err error
		if u, err = dburl.Parse(dsn); err != nil {
			t.Fatalf("parse: %v", err)
		}
		dsn = u.DSN
	default:
		u = &dburl.URL{Driver: driver}
	}
	adminDB := func() *sql.DB {
		t.Helper()
		db, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("ping: %v", err)
		}
		return db
	}()
	limitedDSN := setup(t, adminDB)

	limitedDB, err := sql.Open(driver, limitedDSN)
	if err != nil {
		t.Fatalf("open limited: %v", err)
	}
	defer limitedDB.Close()
	limitedDB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := limitedDB.PingContext(ctx); err != nil {
		t.Fatalf("ping limited: %v", err)
	}

	real, err := drivers.NewMetadataReader(ctx, u, limitedDB, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatalf("metadata reader: %v", err)
	}
	comp := completer.NewDefaultCompleter(
		completer.WithReader(real),
		completer.WithDB(limitedDB),
		completer.WithContextCompletion(),
	)
	live := completer.NewLive(comp).(rline.LiveCompleter)
	inv := live.(interface{ Invalidate() })

	// stableCands polls the FROM menu until it stops changing, so a
	// still-in-flight load cannot fake a negative
	stableCands := func(line string) []rline.Cand {
		t.Helper()
		runes := []rune(line)
		inv.Invalidate()
		deadline := time.Now().Add(15 * time.Second)
		stable, cands := 0, []rline.Cand(nil)
		for time.Now().Before(deadline) && stable < 40 {
			if next, _, _ := live.DoLive(runes, len(runes)); len(next) == len(cands) {
				stable++
			} else {
				stable, cands = 0, next
			}
			time.Sleep(5 * time.Millisecond)
		}
		return cands
	}
	hasCand := func(cands []rline.Cand, text string) bool {
		for _, c := range cands {
			if c.Text == text {
				return true
			}
		}
		return false
	}

	var ns, marker string
	switch driver {
	case "oracle":
		ns, marker = "USQL_PRIV_TEST.", "USQL_PRIV_TEST.USQL_PRIV_MARKER"
	default:
		ns, marker = "usql_priv_test.", "usql_priv_test.usql_priv_marker"
	}

	// negative: the locked namespace is not offered, and a qualified load of
	// it yields nothing
	for line, forbid := range map[string]string{
		"SELECT * FROM ":      marker,
		"SELECT * FROM " + ns: marker,
	} {
		for _, c := range stableCands(line) {
			if c.Text == forbid {
				t.Fatalf("%q: locked marker %q offered before the grant", line, forbid)
			}
		}
	}
	if cands := stableCands("SELECT * FROM "); hasCand(cands, strings.TrimSuffix(ns, ".")) {
		t.Fatalf("FROM menu offers the locked namespace: %v", cands)
	}

	// grant, invalidate, retry: the marker must surface, qualified
	grant(t, adminDB)
	deadline := time.Now().Add(15 * time.Second)
	line := "SELECT * FROM " + ns
	for {
		cands := stableCands(line)
		if hasCand(cands, marker) {
			t.Logf("granted marker %q offered among %d candidates", marker, len(cands))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("granted marker %q still absent (candidates: %v)", marker, cands)
		}
	}
}
