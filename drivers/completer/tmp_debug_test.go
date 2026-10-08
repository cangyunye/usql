package completer_test

import (
	"context"
	"database/sql"
	"fmt"
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

func TestTmpMySQLColumnsDebug(t *testing.T) {
	dsn := os.Getenv("USQL_LIVE_MYSQL")
	if dsn == "" {
		t.Skip("no dsn")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	u := &dburl.URL{Driver: "mysql"}
	real, err := drivers.NewMetadataReader(context.Background(), u, db, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatal(err)
	}
	dbg := &dbgCols{Reader: real}
	c := completer.NewDefaultCompleter(
		completer.WithReader(dbg),
		completer.WithDB(db),
		completer.WithSchemaKind("database"),
		completer.WithContextCompletion(),
	)
	live := completer.NewLive(c).(rline.LiveCompleter)
	inv := live.(interface{ Invalidate() })

	inv.Invalidate()
	from := []rune("SELECT * FROM ")
	for i := 0; i < 20; i++ {
		live.DoLive(from, len(from))
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("--- L3 default_db.DEPT WHERE ---")
	colLine := []rune("SELECT * FROM default_db.DEPT WHERE ")
	for i := 0; i < 20; i++ {
		live.DoLive(colLine, len(colLine))
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("--- repeats ---")
	for _, suf := range []string{"i", "id", "id2"} {
		word := append(append([]rune(nil), colLine...), []rune(suf)...)
		live.DoLive(word, len(colLine)+1)
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("--- done ---")
}

type dbgCols struct{ metadata.Reader }

func (r *dbgCols) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	fmt.Printf("DBG-Columns catalog=%q schema=%q name=%q visible=%v system=%v\n",
		f.Catalog, f.Schema, f.Name, f.OnlyVisible, f.WithSystem)
	return r.Reader.(metadata.ColumnReader).Columns(f)
}
