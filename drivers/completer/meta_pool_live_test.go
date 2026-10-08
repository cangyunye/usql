package completer_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	_ "github.com/xo/usql/internal"
	"github.com/xo/usql/rline"
)

// TestLiveMetaPoolReplay drives the metadata-pool wiring at the driver
// level: the completer's loads run on a second, metadata-only pool, the
// user pool executes a scope statement, and the handler's replay plus
// invalidation must make the completer resolve the new scope from the
// metadata pool. The negative control (before the replay) proves the test
// detects a missing replay: the scope would stay the login default.
//
// Skipped when the corresponding USQL_LIVE_* DSN is unset.
func TestLiveMetaPoolReplay(t *testing.T) {
	ran := false
	if dsn := os.Getenv("USQL_LIVE_PG"); dsn != "" {
		ran = true
		t.Run("postgres", func(t *testing.T) {
			runMetaPoolReplay(t, "postgres", dsn,
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`DROP SCHEMA IF EXISTS meta_replay_test CASCADE`,
						`CREATE SCHEMA meta_replay_test`,
						`CREATE TABLE meta_replay_test.meta_replay_marker (id int)`,
					)
					t.Cleanup(func() {
						db.Exec(`DROP SCHEMA IF EXISTS meta_replay_test CASCADE`)
					})
				},
				`SET search_path = meta_replay_test`,
				"meta_replay_test.meta_replay_marker",
			)
		})
	}
	if dsn := os.Getenv("USQL_LIVE_MYSQL"); dsn != "" {
		ran = true
		t.Run("mysql", func(t *testing.T) {
			runMetaPoolReplay(t, "mysql", dsn,
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`CREATE DATABASE IF NOT EXISTS meta_replay_test`,
						`CREATE TABLE IF NOT EXISTS meta_replay_test.meta_replay_marker (id int)`,
					)
					t.Cleanup(func() {
						db.Exec(`DROP DATABASE IF EXISTS meta_replay_test`)
					})
				},
				`USE meta_replay_test`,
				"meta_replay_test.meta_replay_marker",
			)
		})
	}
	if dsn := os.Getenv("USQL_LIVE_ORACLE"); dsn != "" {
		ran = true
		t.Run("oracle", func(t *testing.T) {
			runMetaPoolReplay(t, "oracle", dsn,
				func(t *testing.T, db *sql.DB) {
					execLive(t, db,
						`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_META_TEST CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
						`CREATE USER USQL_META_TEST IDENTIFIED BY "MetaReplay_123" QUOTA UNLIMITED ON USERS`,
						`GRANT CREATE SESSION TO USQL_META_TEST`,
						`CREATE TABLE USQL_META_TEST.META_REPLAY_MARKER (ID NUMBER)`,
					)
					t.Cleanup(func() {
						db.Exec(`BEGIN EXECUTE IMMEDIATE 'DROP USER USQL_META_TEST CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`)
					})
				},
				`ALTER SESSION SET CURRENT_SCHEMA = USQL_META_TEST`,
				"USQL_META_TEST.META_REPLAY_MARKER",
			)
		})
	}
	if !ran {
		t.Skip("USQL_LIVE_MYSQL / USQL_LIVE_PG / USQL_LIVE_ORACLE not set")
	}
}

// execLive runs each statement, failing the test on error.
func execLive(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// runMetaPoolReplay runs one driver through the metadata-pool replay flow.
func runMetaPoolReplay(t *testing.T, driver, dsn string, setup func(*testing.T, *sql.DB), scopeSQL, marker string) {
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
	openPinnedLive := func() *sql.DB {
		t.Helper()
		db, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("ping: %v", err)
		}
		return db
	}
	// the two pools of the handler wiring
	userDB := openPinnedLive()
	metaDB := openPinnedLive()
	setup(t, userDB)

	ctx := context.Background()
	real, err := drivers.NewMetadataReader(ctx, u, metaDB, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatalf("metadata reader: %v", err)
	}
	comp := completer.NewDefaultCompleter(
		completer.WithReader(real),
		completer.WithDB(metaDB),
		completer.WithContextCompletion(),
	)
	live := completer.NewLive(comp).(rline.LiveCompleter)
	inv := live.(interface{ Invalidate() })

	line := []rune("SELECT * FROM ")
	// negative control: before the scope change the marker is absent (wait
	// for the menu to stabilize, so a leak would have surfaced)
	inv.Invalidate()
	deadline := time.Now().Add(15 * time.Second)
	stable, cands := 0, []rline.Cand(nil)
	for time.Now().Before(deadline) && stable < 40 {
		if next, _, _ := live.DoLive(line, len(line)); len(next) == len(cands) {
			stable++
		} else {
			stable, cands = 0, next
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, c := range cands {
		if c.Text == marker {
			t.Fatalf("marker %q visible before the scope change (got %d candidates)", marker, len(cands))
		}
	}

	// the user statement succeeds on the user pool, the handler replays it
	// on the metadata pool, then invalidates the cache
	execLive(t, userDB, scopeSQL)
	execLive(t, metaDB, scopeSQL)
	inv.Invalidate()

	// the marker must appear: the metadata pool resolved the new scope
	deadline = time.Now().Add(15 * time.Second)
	for {
		cands, _, _ := live.DoLive(line, len(line))
		for _, c := range cands {
			if c.Text == marker {
				t.Logf("scope change resolved on the metadata pool: %q in %d candidates", marker, len(cands))
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("scope change did not reach the metadata pool: %q not among %d candidates", marker, len(cands))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
