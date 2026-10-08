package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"testing"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/drivers/metadata/postgres"
)

// recorder is a minimal driver that captures the SQL of every query and
// returns an empty result set: the assertions below are about the
// WHERE-clause assembly, not about a server.
type recorder struct {
	queries []string
}

type recorderConn struct{ rec *recorder }

type recorderStmt struct {
	rec   *recorder
	query string
	cols  []string
}

func (r *recorder) Open(string) (driver.Conn, error) { return &recorderConn{rec: r}, nil }

// Connect satisfies driver.Connector, so the recorder can back a sql.DB
// pool directly.
func (r *recorder) Connect(context.Context) (driver.Conn, error) {
	return &recorderConn{rec: r}, nil
}

func (r *recorder) Driver() driver.Driver { return r }

func (c *recorderConn) Prepare(query string) (driver.Stmt, error) {
	return &recorderStmt{rec: c.rec, query: query}, nil
}
func (c *recorderConn) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	return &recorderStmt{rec: c.rec, query: query, cols: []string{"Schema", "Name", "Type", "Rows", "Size", "Description"}}, nil
}
func (c *recorderConn) Close() error              { return nil }
func (c *recorderConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

func (s *recorderStmt) Close() error  { return nil }
func (s *recorderStmt) NumInput() int { return -1 }
func (s *recorderStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *recorderStmt) Query([]driver.Value) (driver.Rows, error) {
	s.rec.queries = append(s.rec.queries, s.query)
	return &recorderRows{cols: s.cols}, nil
}
func (s *recorderStmt) QueryContext(_ context.Context, _ []driver.NamedValue) (driver.Rows, error) {
	s.rec.queries = append(s.rec.queries, s.query)
	return &recorderRows{cols: s.cols}, nil
}

type recorderRows struct{ cols []string }

func (r *recorderRows) Columns() []string { return r.cols }
func (r *recorderRows) Close() error      { return nil }
func (r *recorderRows) Next([]driver.Value) error {
	return io.EOF // empty result set
}

// TestTablesOnlyAccessiblePredicate: pg catalogs are readable by every
// login, so the completer's loads (OnlyAccessible) must gate relations on
// schema USAGE plus any DML privilege, while the listing loads stay
// unfiltered.
func TestTablesOnlyAccessiblePredicate(t *testing.T) {
	rec := &recorder{}
	db := sql.OpenDB(rec)
	defer db.Close()
	r := postgres.NewReader()(db).(metadata.TableReader)

	if _, err := r.Tables(metadata.Filter{OnlyAccessible: true}); err != nil {
		t.Fatalf("Tables(OnlyAccessible): %v", err)
	}
	if len(rec.queries) != 1 {
		t.Fatalf("Tables issued %d queries, want 1", len(rec.queries))
	}
	q := rec.queries[0]
	if !strings.Contains(q, "has_schema_privilege(n.oid, 'USAGE')") {
		t.Errorf("OnlyAccessible query lacks the schema-USAGE gate:\n%s", q)
	}
	if !strings.Contains(q, "has_table_privilege(c.oid, 'SELECT')") {
		t.Errorf("OnlyAccessible query lacks the table-DML gate:\n%s", q)
	}

	rec.queries = nil
	if _, err := r.Tables(metadata.Filter{}); err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if len(rec.queries) != 1 {
		t.Fatalf("Tables issued %d queries, want 1", len(rec.queries))
	}
	if strings.Contains(rec.queries[0], "has_schema_privilege") {
		t.Errorf("listing query must stay unfiltered:\n%s", rec.queries[0])
	}
}
