package mysql_test

import (
	"database/sql"
	"database/sql/driver"
	"sync"
	"testing"

	"github.com/xo/usql/drivers/metadata"
	mymeta "github.com/xo/usql/drivers/metadata/mysql"

	sqlite "modernc.org/sqlite"
)

// currentDatabase is what the DATABASE() stub reports; empty means no
// default database is selected (DATABASE() returns NULL).
var (
	currentDatabaseMu sync.Mutex
	currentDatabase   string
)

func setCurrentDatabase(name string) {
	currentDatabaseMu.Lock()
	defer currentDatabaseMu.Unlock()
	currentDatabase = name
}

// registerDatabaseStub stands in for MySQL's DATABASE() once, globally.
var databaseStubOnce sync.Once

func registerDatabaseStub() {
	databaseStubOnce.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction("DATABASE", 0,
			func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
				currentDatabaseMu.Lock()
				defer currentDatabaseMu.Unlock()
				if currentDatabase == "" {
					return nil, nil
				}
				return currentDatabase, nil
			})
	})
}

// openSchemataDB opens an in-memory database standing in for MySQL's
// information_schema.schemata: alpha, my_app and myxapp (the latter pair
// differ only at the underscore position), plus the mysql system database.
// The pool is pinned to one connection, because :memory: is per-connection.
func openSchemataDB(t *testing.T) *sql.DB {
	t.Helper()
	registerDatabaseStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`ATTACH ':memory:' AS information_schema`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE information_schema.schemata (catalog_name text, schema_name text)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{
		{"def", "alpha"},
		{"def", "my_app"},
		{"def", "myxapp"},
		{"def", "mysql"},
	} {
		if _, err := db.Exec(`INSERT INTO information_schema.schemata VALUES (?, ?)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestCurrentSchemaResolvesSessionDatabase: the completer resolves the
// session's default schema via Schemas(Filter{OnlyVisible: true}) and takes
// the first row, so the restriction must match exactly. A LIKE here breaks
// two ways: an underscore in the database name acts as a wildcard
// (DATABASE() 'my_app' also matches 'myxapp'), and with no default database
// selected the COALESCE(DATABASE(), '%') sentinel matches every database —
// the completer then treats the alphabetically first one as the session
// schema.
func TestCurrentSchemaResolvesSessionDatabase(t *testing.T) {
	cases := []struct {
		name string
		db   string
		want []string
	}{
		{"underscore is not a wildcard", "my_app", []string{"my_app"}},
		{"no default database matches nothing", "", nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setCurrentDatabase(test.db)
			r := mymeta.NewReader(openSchemataDB(t)).(metadata.SchemaReader)
			set, err := r.Schemas(metadata.Filter{OnlyVisible: true})
			if err != nil {
				t.Fatalf("Schemas: %v", err)
			}
			defer set.Close()
			var got []string
			for set.Next() {
				got = append(got, set.Get().Schema)
			}
			if len(got) != len(test.want) {
				t.Fatalf("Schemas(OnlyVisible) = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("Schemas(OnlyVisible) = %v, want %v", got, test.want)
				}
			}
		})
	}
}

// TestCatalogsServeListDatabases: databases double as catalogs on MySQL, so
// the schemata view backs both \l and its argument completion.
func TestCatalogsServeListDatabases(t *testing.T) {
	r := mymeta.NewReader(openSchemataDB(t))
	cr, ok := r.(metadata.CatalogReader)
	if !ok {
		t.Fatal("mysql reader does not implement metadata.CatalogReader: \\l is not supported")
	}
	set, err := cr.Catalogs(metadata.Filter{})
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		got = append(got, set.Get().Catalog)
	}
	want := []string{"alpha", "my_app", "mysql", "myxapp"}
	if len(got) != len(want) {
		t.Fatalf("Catalogs() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Catalogs() = %v, want %v", got, want)
		}
	}

	// the \l argument position filters by name pattern
	set2, err := cr.Catalogs(metadata.Filter{Name: "my%"})
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	defer set2.Close()
	var got2 []string
	for set2.Next() {
		got2 = append(got2, set2.Get().Catalog)
	}
	if len(got2) != 3 || got2[0] != "my_app" || got2[1] != "mysql" || got2[2] != "myxapp" {
		t.Fatalf("Catalogs(my%%) = %v, want [my_app mysql myxapp]", got2)
	}
}

// openColumnsDB opens an in-memory database with an information_schema.columns
// catalog: my_app.t1(id) and myxapp.t1(id, extra) — the two schemas differ
// only at the underscore position.
func openColumnsDB(t *testing.T) *sql.DB {
	t.Helper()
	registerDatabaseStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`ATTACH ':memory:' AS information_schema`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE information_schema.columns (
			table_catalog text, table_schema text, table_name text, column_name text,
			ordinal_position int, column_type text, column_default text, is_nullable text,
			character_maximum_length int, numeric_precision int, datetime_precision int,
			numeric_scale int, character_octet_length int)`); err != nil {
		t.Fatal(err)
	}
	rows := [][]interface{}{
		{"def", "my_app", "t1", "id", 1, "int", "", "YES", nil, 10, nil, 0, 0},
		{"def", "myxapp", "t1", "id", 1, "int", "", "YES", nil, 10, nil, 0, 0},
		{"def", "myxapp", "t1", "extra", 2, "varchar(10)", "", "YES", 10, nil, nil, 0, 10},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO information_schema.columns VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestNewReaderKeepsItsOwnDB: two readers built on two different pools must
// each query their own pool — the completer and the \d commands each build
// one, and a future metadata-only connection (the completion/connection
// contention fix) coexists with the user pool by design. The shared
// information-schema reader must not rebind an earlier reader to a later
// reader's database.
func TestNewReaderKeepsItsOwnDB(t *testing.T) {
	dbA := openSchemataDB(t)
	dbB := openSchemataDB(t)
	// mark B's catalog with a database A does not have
	if _, err := dbB.Exec(`INSERT INTO information_schema.schemata VALUES ('def', 'beta_only')`); err != nil {
		t.Fatal(err)
	}
	rA := mymeta.NewReader(dbA).(metadata.SchemaReader)
	_ = mymeta.NewReader(dbB) // a second reader on a second pool, built later
	set, err := rA.Schemas(metadata.Filter{})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		got = append(got, set.Get().Schema)
	}
	for _, name := range got {
		if name == "beta_only" {
			t.Errorf("first reader served the second reader's database: Schemas() = %v", got)
		}
	}
}

// TestColumnsToleratesNullStatistics: engines leave the numeric statistics
// columns NULL (MySQL's character_octet_length and numeric_scale for some
// types, PostgreSQL's character_octet_length for non-character types) — the
// columns load must not fail on them, or completion retries the load on
// every keystroke and the column candidates never land.
func TestColumnsToleratesNullStatistics(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`ATTACH ':memory:' AS information_schema`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE information_schema.columns (
			table_catalog text, table_schema text, table_name text, column_name text,
			ordinal_position int, column_type text, column_default text, is_nullable text,
			character_maximum_length int, numeric_precision int, datetime_precision int,
			numeric_scale int, character_octet_length int)`); err != nil {
		t.Fatal(err)
	}
	// an int column with every nullable statistic NULL
	if _, err := db.Exec(`INSERT INTO information_schema.columns VALUES
			('def', 'my_app', 't1', 'id', 1, 'int', NULL, 'YES', NULL, NULL, NULL, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	r := mymeta.NewReader(db).(metadata.ColumnReader)
	set, err := r.Columns(metadata.Filter{Schema: "my_app", Parent: "t1"})
	if err != nil {
		t.Fatalf("Columns with NULL statistics: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		col := set.Get()
		got = append(got, col.Schema+"."+col.Table+"."+col.Name)
	}
	if len(got) != 1 || got[0] != "my_app.t1.id" {
		t.Errorf("Columns(t1) = %v, want [my_app.t1.id]", got)
	}
}

// TestColumnsOnlyVisibleExactCurrentSchema: bare column loads resolve via
// the session's current schema — the completer's hottest path (every WHERE
// keystroke). A LIKE against the current-schema expression treats
// underscores as wildcards, so a look-alike schema's same-named table leaks
// its columns into the candidates (live PostgreSQL: SET search_path =
// my_schema made myxschema.orders' columns appear for bare "orders").
func TestColumnsOnlyVisibleExactCurrentSchema(t *testing.T) {
	setCurrentDatabase("my_app")
	r := mymeta.NewReader(openColumnsDB(t)).(metadata.ColumnReader)
	set, err := r.Columns(metadata.Filter{Parent: "t1", OnlyVisible: true})
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		col := set.Get()
		got = append(got, col.Schema+"."+col.Table+"."+col.Name)
	}
	if len(got) != 1 || got[0] != "my_app.t1.id" {
		t.Errorf("Columns(t1, OnlyVisible) = %v, want [my_app.t1.id]", got)
	}
}
