package oracle

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/xo/dburl"
	"github.com/xo/usql/drivers/metadata"

	sqlite "modernc.org/sqlite"
)

// openTestDB opens an in-memory sqlite database standing in for an
// Oracle-family server whose catalog tables may or may not exist: the
// reader's Oracle SQL (v$parameter, dba_db_links) is executed as-is, so
// missing views surface as "no such table" errors — the same shape as
// OBE-00942 on OceanBase Oracle tenants, which have no V$PARAMETER.
func openTestDB(t *testing.T, links bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if links {
		if _, err := db.Exec(`CREATE TABLE dba_db_links (db_link text)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO dba_db_links VALUES ('REMOTE.DBLINK')`); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestCatalogsDegradesWithoutAuxiliaryViews: when V$PARAMETER does not
// exist (OceanBase Oracle tenants), Catalogs must return the remaining
// catalogs instead of failing — a reader error here used to kill the whole
// "selectables" completion query.
func TestCatalogsDegradesWithoutAuxiliaryViews(t *testing.T) {
	r := NewReaderQ()(openTestDB(t, true)).(metadata.CatalogReader)
	set, err := r.Catalogs(metadata.Filter{})
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	defer set.Close()
	var catalogs []string
	for set.Next() {
		catalogs = append(catalogs, set.Get().Catalog)
	}
	if len(catalogs) != 1 || catalogs[0] != "REMOTE.DBLINK" {
		t.Fatalf("catalogs: %v, want [REMOTE.DBLINK]", catalogs)
	}
}

// TestCatalogsEmptyWhenAllAuxiliaryViewsMissing: without any of the
// auxiliary views, Catalogs returns an empty set and no error.
func TestCatalogsEmptyWhenAllAuxiliaryViewsMissing(t *testing.T) {
	r := NewReaderQ()(openTestDB(t, false)).(metadata.CatalogReader)
	set, err := r.Catalogs(metadata.Filter{})
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	defer set.Close()
	for set.Next() {
		t.Fatalf("unexpected catalog %q", set.Get().Catalog)
	}
}

// TestConditionsCurrentSchemaCarveOut: the system-schema exclusion must not
// hide the session's own current schema — on OceanBase Oracle tenants the
// login user is SYS, a member of the exclusion list, and its objects would
// otherwise vanish from completion snapshots and listings.
func TestConditionsCurrentSchemaCarveOut(t *testing.T) {
	r := NewReaderQ()(openTestDB(t, false)).(*metaReader)
	conds, vals := r.conditions(metadata.Filter{WithSystem: false}, formats{
		notSchemas: "UPPER(o.owner) NOT IN (%s)",
	})
	want := "(UPPER(o.owner) NOT IN ('CTXSYS', 'FLOWS_FILES', 'MDSYS', 'OUTLN', 'SYS', 'SYSTEM', 'XDB', 'XS$NULL', 'OCEANBASE')" +
		" OR UPPER(o.owner) = SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA'))"
	if len(conds) != 1 || conds[0] != want {
		t.Errorf("conds = %q, want [%s]", conds, want)
	}
	if len(vals) != 0 {
		t.Errorf("vals = %v, want none", vals)
	}

	// WithSystem queries skip the exclusion entirely
	conds, _ = r.conditions(metadata.Filter{WithSystem: true}, formats{
		notSchemas: "UPPER(o.owner) NOT IN (%s)",
	})
	if len(conds) != 0 {
		t.Errorf("WithSystem conds = %q, want none", conds)
	}
}

// registerSysContextStub stands in for Oracle's SYS_CONTEXT once, globally:
// the harness's session always resolves to SCOTT. Must run before the first
// connection is opened.
var sysContextOnce sync.Once

func registerSysContextStub() {
	sysContextOnce.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction("SYS_CONTEXT", 2,
			func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
				return "SCOTT", nil
			})
	})
}

// openUsersDB opens an in-memory database with an all_users catalog of four
// accounts: two system users (excluded from listings), SCOTT (the harness's
// current schema) and ADAM (an ordinary second user).
func openUsersDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE all_users (username text)`); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"SYS", "CTXSYS", "ADAM", "SCOTT"} {
		if _, err := db.Exec(`INSERT INTO all_users VALUES (?)`, u); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestSchemasOnlyVisibleFollowsCurrentSchema: the completer resolves the
// session's default schema via Schemas(Filter{OnlyVisible: true}) and takes
// the first row — the reader must therefore filter to the current schema
// (SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')), not return every account and
// let the alphabetical order pick a stranger's schema.
func TestSchemasOnlyVisibleFollowsCurrentSchema(t *testing.T) {
	r := NewReaderQ()(openUsersDB(t)).(metadata.SchemaReader)
	set, err := r.Schemas(metadata.Filter{OnlyVisible: true})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		got = append(got, set.Get().Schema)
	}
	if len(got) != 1 || got[0] != "SCOTT" {
		t.Errorf("Schemas(OnlyVisible) = %v, want [SCOTT]", got)
	}

	// the unfiltered listing still returns every non-system account
	set2, err := r.Schemas(metadata.Filter{})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set2.Close()
	var got2 []string
	for set2.Next() {
		got2 = append(got2, set2.Get().Schema)
	}
	if len(got2) != 2 || got2[0] != "ADAM" || got2[1] != "SCOTT" {
		t.Errorf("Schemas() = %v, want [ADAM SCOTT]", got2)
	}
}

// openAccessibleDB opens an in-memory database standing in for a server
// where the login (SCOTT) can reach ADAM's ORDERS and the PUBLIC synonyms,
// while HR is an account it holds no privilege on. all_users lists every
// account; all_objects only lists what the login can access.
func openAccessibleDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE all_users (username text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE all_objects (owner text, object_name text, object_type text)`); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"SYS", "CTXSYS", "ADAM", "SCOTT", "HR"} {
		if _, err := db.Exec(`INSERT INTO all_users VALUES (?)`, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][3]string{
		{"SCOTT", "EMPLOYEES", "TABLE"},
		{"ADAM", "ORDERS", "TABLE"},
		{"PUBLIC", "DUAL", "SYNONYM"},
	} {
		if _, err := db.Exec(`INSERT INTO all_objects VALUES (?, ?, ?)`, row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestSchemasOnlyAccessibleDerivesFromAllObjects: the completer's schema
// tier must offer only owners of accessible objects — an account with no
// privileges (HR) would complete into an empty menu, and PUBLIC owns no
// namespace at all ("PUBLIC.x" is ORA-00903). The default path keeps
// listing every account.
func TestSchemasOnlyAccessibleDerivesFromAllObjects(t *testing.T) {
	r := NewReaderQ()(openAccessibleDB(t)).(metadata.SchemaReader)

	set, err := r.Schemas(metadata.Filter{WithSystem: false, OnlyAccessible: true})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		got = append(got, set.Get().Schema)
	}
	want := []string{"ADAM", "SCOTT"}
	if len(got) != len(want) {
		t.Fatalf("Schemas(OnlyAccessible) = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("Schemas(OnlyAccessible) = %v, want %v", got, want)
		}
	}

	// the completer's current-schema resolution keeps working on the same
	// filter shape
	set2, err := r.Schemas(metadata.Filter{OnlyVisible: true, OnlyAccessible: true})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set2.Close()
	var got2 []string
	for set2.Next() {
		got2 = append(got2, set2.Get().Schema)
	}
	if len(got2) != 1 || got2[0] != "SCOTT" {
		t.Errorf("Schemas(OnlyVisible, OnlyAccessible) = %v, want [SCOTT]", got2)
	}

	// the default path is untouched: every account, system ones excluded
	set3, err := r.Schemas(metadata.Filter{})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	defer set3.Close()
	var got3 []string
	for set3.Next() {
		got3 = append(got3, set3.Get().Schema)
	}
	if len(got3) != 3 || got3[0] != "ADAM" || got3[1] != "HR" || got3[2] != "SCOTT" {
		t.Errorf("Schemas() = %v, want [ADAM HR SCOTT]", got3)
	}
}

// openSequencesDB opens an in-memory database with an all_sequences catalog:
// SCOTT.EMP_SEQ (the harness's current schema) and HR.CUSTOMERS_SEQ, plus
// the ISEQ$$_ sequences Oracle generates behind every identity column.
func openSequencesDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE all_sequences (sequence_owner text, sequence_name text)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{
		{"HR", "CUSTOMERS_SEQ"},
		{"SCOTT", "EMP_SEQ"},
		{"SCOTT", "ISEQ$$_74971"},
		{"SCOTT", "ISEQ$$_76256"},
	} {
		if _, err := db.Exec(`INSERT INTO all_sequences VALUES (?, ?)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestSequencesServesCompletionAndListings: sequence names must complete in
// "SELECT seq.NEXTVAL" positions and in \ds listings, so the reader has to
// serve them from all_sequences — with schema filters resolved against the
// session's current schema when OnlyVisible is set. The ISEQ$$_ sequences
// Oracle generates for identity columns are implementation details: they
// would flood the completion menu, so they are excluded.
func TestSequencesServesCompletionAndListings(t *testing.T) {
	r := NewReaderQ()(openSequencesDB(t))
	sr, ok := r.(metadata.SequenceReader)
	if !ok {
		t.Fatal("oracle metaReader does not implement metadata.SequenceReader")
	}

	// the visible-scope load (current schema) finds only the named sequence
	set, err := sr.Sequences(metadata.Filter{OnlyVisible: true})
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		seq := set.Get()
		got = append(got, seq.Schema+"."+seq.Name)
	}
	if len(got) != 1 || got[0] != "SCOTT.EMP_SEQ" {
		t.Errorf("Sequences(OnlyVisible) = %v, want [SCOTT.EMP_SEQ] without ISEQ sequences", got)
	}

	// a named-schema load (the qualified-completion path) matches
	// case-insensitively like the rest of the reader
	set2, err := sr.Sequences(metadata.Filter{Schema: "hr", WithSystem: true})
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	defer set2.Close()
	var got2 []string
	for set2.Next() {
		seq := set2.Get()
		got2 = append(got2, seq.Schema+"."+seq.Name)
	}
	if len(got2) != 1 || got2[0] != "HR.CUSTOMERS_SEQ" {
		t.Errorf("Sequences(Schema hr) = %v, want [HR.CUSTOMERS_SEQ]", got2)
	}
}

// openObjectsDB opens an in-memory database with an all_objects catalog that
// already lists the schema's private synonym, plus the all_synonyms view the
// Tables reader unions in when the types filter includes SYNONYM.
func openObjectsDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE all_objects (owner text, object_name text, object_type text)`,
		`CREATE TABLE all_synonyms (owner text, synonym_name text, table_owner text, table_name text, db_link text)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][3]string{
		{"SCOTT", "T1", "TABLE"},
		{"SCOTT", "S1", "SYNONYM"},
	} {
		if _, err := db.Exec(`INSERT INTO all_objects VALUES (?, ?, ?)`, row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO all_synonyms VALUES ('SCOTT', 'S1', 'SCOTT', 'T1', '')`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestTablesSynonymsNotDuplicated: all_objects already contains the schema's
// private synonyms, and the all_synonyms union adds them again — a synonym
// must be offered once, not twice (live Oracle: every synonym twice in the
// FROM menu and in \dt).
func TestTablesSynonymsNotDuplicated(t *testing.T) {
	r := NewReaderQ()(openObjectsDB(t)).(metadata.TableReader)
	set, err := r.Tables(metadata.Filter{Schema: "SCOTT", WithSystem: true,
		Types: []string{"TABLE", "VIEW", "MATERIALIZED VIEW", "SYNONYM"}})
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	defer set.Close()
	seen := map[string]int{}
	for set.Next() {
		row := set.Get()
		seen[row.Schema+"."+row.Name]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("object %s listed %d times, want once", name, n)
		}
	}
	if len(seen) != 2 {
		t.Errorf("Tables returned %d distinct objects, want 2 (T1, S1)", len(seen))
	}
}

// TestConditionsOnlyVisibleExact: the current-schema expression is a single
// schema name, never a pattern — the OnlyVisible predicate must match it
// exactly. A LIKE here lets look-alike owners (same length, differing at an
// underscore) leak into visible-scope object and column loads.
func TestConditionsOnlyVisibleExact(t *testing.T) {
	r := NewReaderQ()(openTestDB(t, false)).(*metaReader)
	conds, vals := r.conditions(metadata.Filter{OnlyVisible: true, WithSystem: true}, formats{
		schema: "o.owner LIKE %s",
	})
	want := "o.owner = SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')"
	if len(conds) != 1 || conds[0] != want {
		t.Errorf("conds = %q, want [%s]", conds, want)
	}
	if len(vals) != 0 {
		t.Errorf("vals = %v, want none", vals)
	}
}

// decodeOnce registers the Oracle DECODE stub the index queries need.
var decodeOnce sync.Once

func registerDecodeStub() {
	decodeOnce.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction("DECODE", -1,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				// Oracle DECODE(expr, search, result [, search, result ...] [, default])
				if len(args) < 3 {
					return nil, nil
				}
				for i := 1; i+1 < len(args); i += 2 {
					if args[0] == args[i] {
						return args[i+1], nil
					}
				}
				if len(args)%2 == 0 {
					return args[len(args)-1], nil
				}
				return nil, nil
			})
	})
}

// openDescribeDB opens an in-memory stand-in for an Oracle catalog laid out
// for a `\d T1`: the table itself with two columns, its index, and two
// sequences — SCOTT.T1_SEQ and an unrelated one in HR.
func openDescribeDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	registerDecodeStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE all_objects (owner text, object_name text, object_type text)`,
		`CREATE TABLE all_synonyms (owner text, synonym_name text, table_owner text, table_name text, db_link text)`,
		`CREATE TABLE all_tab_columns (owner text, table_name text, column_name text, column_id int, data_type text, nullable text, data_length int, data_precision int, data_scale int, char_col_decl_length int)`,
		`CREATE TABLE all_sequences (sequence_owner text, sequence_name text)`,
		`CREATE TABLE all_indexes (owner text, table_name text, index_name text, uniqueness text)`,
		`CREATE TABLE all_ind_columns (index_owner text, index_name text, table_name text, column_name text, column_position int)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	ins := func(q string, args ...interface{}) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// the described table and its index (one unique, one not)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'T1', 'TABLE')`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'T1', 'INDEX')`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'T1', 'TRIGGER')`)
	ins(`INSERT INTO all_tab_columns VALUES ('SCOTT', 'T1', 'ID', 1, 'NUMBER', 'Y', 22, 0, 0, 0)`)
	ins(`INSERT INTO all_tab_columns VALUES ('SCOTT', 'T1', 'NAME', 2, 'VARCHAR2', 'Y', 100, NULL, NULL, 100)`)
	ins(`INSERT INTO all_indexes VALUES ('SCOTT', 'T1', 'T1_IDX', 'NONUNIQUE')`)
	ins(`INSERT INTO all_indexes VALUES ('SCOTT', 'T1', 'T1_UQ', 'UNIQUE')`)
	ins(`INSERT INTO all_ind_columns VALUES ('SCOTT', 'T1_IDX', 'T1', 'ID', 1)`)
	ins(`INSERT INTO all_ind_columns VALUES ('SCOTT', 'T1_UQ', 'T1', 'NAME', 1)`)
	// sequences: one plain, one the identity-column implementation detail —
	// all_objects lists the latter, so \d and \ds need the same exclusion
	// Sequences() applies
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'S1', 'SEQUENCE')`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'ISEQ$$_123', 'SEQUENCE')`)
	ins(`INSERT INTO all_sequences VALUES ('SCOTT', 'T1_SEQ')`)
	ins(`INSERT INTO all_sequences VALUES ('SCOTT', 'S1')`)
	ins(`INSERT INTO all_sequences VALUES ('SCOTT', 'ISEQ$$_123')`)
	ins(`INSERT INTO all_sequences VALUES ('HR', 'UNRELATED_SEQ')`)
	return db
}

// TestTablesNoTypesDescribesRelationsOnly: with no explicit type filter (the
// `\d name` path), all_objects matches must be restricted to relation kinds —
// an index or trigger sharing the table's name is not a second object to
// describe, and describing it would re-print the table's columns.
func TestTablesNoTypesDescribesRelationsOnly(t *testing.T) {
	r := NewReaderQ()(openDescribeDB(t)).(metadata.TableReader)
	set, err := r.Tables(metadata.Filter{Name: "T1", WithSystem: true})
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		row := set.Get()
		got = append(got, row.Type)
	}
	if len(got) != 1 || got[0] != "TABLE" {
		t.Errorf("Tables(Name T1) types = %v, want [TABLE] only", got)
	}
}

// TestSequencesHonorsNameFilter: the \d path filters sequences by name —
// a reader ignoring Filter.Name makes `\d some_table` print every accessible
// sequence, one block per sequence.
func TestSequencesHonorsNameFilter(t *testing.T) {
	r := NewReaderQ()(openDescribeDB(t)).(metadata.SequenceReader)

	set, err := r.Sequences(metadata.Filter{Name: "T1_SEQ", WithSystem: true})
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	defer set.Close()
	var got []string
	for set.Next() {
		seq := set.Get()
		got = append(got, seq.Schema+"."+seq.Name)
	}
	if len(got) != 1 || got[0] != "SCOTT.T1_SEQ" {
		t.Errorf("Sequences(Name T1_SEQ) = %v, want [SCOTT.T1_SEQ]", got)
	}
}

// TestDescribeTableDetailsBounded drives the `\d T1` flow end to end through
// the default writer: the described table's columns and its index print once,
// and neither an unrelated schema's sequence nor a same-named non-relation
// object leaks into the output. The reader used to ignore the sequence name
// filter — on a login with broad visibility every sequence in the database
// was printed, one block per sequence.
func TestDescribeTableDetailsBounded(t *testing.T) {
	db := openDescribeDB(t)
	u, err := dburl.Parse("oracle://scott:tiger@localhost:1521/orcl")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := metadata.NewDefaultWriter(NewReaderQ()(db))(db, &buf)
	if err := w.DescribeTableDetails(u, "T1", false, false); err != nil {
		t.Fatalf("DescribeTableDetails: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "TABLE \"SCOTT.T1\"") {
		t.Errorf("output missing the described table:\n%s", out)
	}
	if strings.Contains(out, "UNRELATED_SEQ") {
		t.Errorf("\\d T1 printed unrelated sequence HR.UNRELATED_SEQ:\n%s", out)
	}
	if n := strings.Count(out, "TABLE \"SCOTT.T1\""); n != 1 {
		t.Errorf("table T1 described %d times, want once:\n%s", n, out)
	}
}

// TestTablesExcludesIdentitySequences: all_objects lists the ISEQ$$_
// sequences behind every identity column — the Tables-based listings (\d
// and \ds) must exclude them exactly like Sequences() does, or the listing
// floods with implementation details.
func TestTablesExcludesIdentitySequences(t *testing.T) {
	r := NewReaderQ()(openDescribeDB(t)).(metadata.TableReader)

	for _, types := range [][]string{nil, {"SEQUENCE"}} {
		set, err := r.Tables(metadata.Filter{Types: types, WithSystem: true})
		if err != nil {
			t.Fatalf("Tables(types=%v): %v", types, err)
		}
		var got []string
		for set.Next() {
			row := set.Get()
			got = append(got, row.Name)
			if strings.HasPrefix(row.Name, "ISEQ$$_") {
				t.Errorf("Tables(types=%v) listed identity sequence %s", types, row.Name)
			}
		}
		set.Close()
		if len(got) == 0 {
			t.Fatalf("Tables(types=%v) returned nothing, want S1 and the relations", types)
		}
	}
}

// TestIndexesUniquenessNotInverted: the uniqueness decode must map a UNIQUE
// index to YES — inverted, \d labels every NONUNIQUE index "UNIQUE" and the
// \di+ Unique? column lies.
func TestIndexesUniquenessNotInverted(t *testing.T) {
	r := NewReaderQ()(openDescribeDB(t)).(metadata.IndexReader)
	set, err := r.Indexes(metadata.Filter{Schema: "SCOTT", Parent: "T1", WithSystem: true})
	if err != nil {
		t.Fatalf("Indexes: %v", err)
	}
	defer set.Close()
	got := map[string]string{}
	for set.Next() {
		i := set.Get()
		got[i.Name] = string(i.IsUnique)
	}
	if got["T1_UQ"] != "YES" {
		t.Errorf("unique index T1_UQ IsUnique = %q, want YES", got["T1_UQ"])
	}
	if got["T1_IDX"] != "NO" {
		t.Errorf("non-unique index T1_IDX IsUnique = %q, want NO", got["T1_IDX"])
	}
}

// openFunctionsDB opens an in-memory stand-in for an Oracle catalog with
// functions, a procedure and a package: SCOTT.F1 (returns NUMBER, arg
// A_IN), SCOTT.F2 (returns VARCHAR2, arg B_IN), SCOTT.P1 (IN OUT arg),
// the package SCOTT.PKG1 with routines PK1 (returns DATE, arg X_IN) and
// PK2 (returns NUMBER), plus ADAM.F3 in a second schema.
func openFunctionsDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	registerDecodeStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE all_objects (owner text, object_name text, object_type text, object_id int)`,
		`CREATE TABLE all_arguments (owner text, object_id int, object_name text, sequence int, position int, argument_name text, in_out text, data_type text, data_length int, data_precision int, data_scale int, radix int, data_level int)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	ins := func(q string, args ...interface{}) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'F1', 'FUNCTION', 10)`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'F2', 'FUNCTION', 11)`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'P1', 'PROCEDURE', 20)`)
	ins(`INSERT INTO all_objects VALUES ('SCOTT', 'PKG1', 'PACKAGE', 30)`)
	ins(`INSERT INTO all_objects VALUES ('ADAM', 'F3', 'FUNCTION', 40)`)
	// functions: the return row (position 0) carries sequence 1, so the
	// Functions join sees exactly one row per function
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 10, 'F1', 1, 0, NULL, 'OUT', 'NUMBER', 22, NULL, NULL, 10, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 10, 'F1', 2, 1, 'A_IN', 'IN', 'NUMBER', 22, NULL, NULL, 10, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 11, 'F2', 1, 0, NULL, 'OUT', 'VARCHAR2', 100, NULL, NULL, 0, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 11, 'F2', 2, 1, 'B_IN', 'IN', 'VARCHAR2', 100, NULL, NULL, 0, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('ADAM', 40, 'F3', 1, 0, NULL, 'OUT', 'DATE', 7, NULL, NULL, 0, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('ADAM', 40, 'F3', 2, 1, 'C_IN', 'IN', 'DATE', 7, NULL, NULL, 0, 0)`)
	// procedure: no return row, the first argument carries sequence 1
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 20, 'P1', 1, 1, 'P_IN', 'IN/OUT', 'DATE', 7, NULL, NULL, 0, 0)`)
	// package: one return row per routine
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 30, 'PK1', 1, 0, NULL, 'OUT', 'DATE', 7, NULL, NULL, 0, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 30, 'PK1', 2, 1, 'X_IN', 'IN', 'NUMBER', 22, NULL, NULL, 10, 0)`)
	ins(`INSERT INTO all_arguments VALUES ('SCOTT', 30, 'PK2', 1, 0, NULL, 'OUT', 'NUMBER', 22, NULL, NULL, 10, 0)`)
	return db
}

// TestFunctionsServesStringTypesAndResults: the Functions rows must speak
// the writer's conventions — Type is the object-type string (the JDBC-style
// numeric codes rendered \df's Type column as "2") and ResultType carries
// the return type (the "Result data type" column used to stay empty).
func TestFunctionsServesStringTypesAndResults(t *testing.T) {
	r := NewReaderQ()(openFunctionsDB(t)).(metadata.FunctionReader)
	set, err := r.Functions(metadata.Filter{WithSystem: true})
	if err != nil {
		t.Fatalf("Functions: %v", err)
	}
	defer set.Close()
	got := map[string]metadata.Function{}
	for set.Next() {
		fn := set.Get()
		got[fn.Schema+"."+fn.SpecificName] = *fn
	}
	type want struct {
		typ, result string
	}
	for name, w := range map[string]want{
		"SCOTT.F1":       {"FUNCTION", "NUMBER"},
		"SCOTT.F2":       {"FUNCTION", "VARCHAR2"},
		"SCOTT.P1":       {"PROCEDURE", ""},
		"SCOTT.PKG1.PK1": {"PACKAGE", "DATE"},
		"SCOTT.PKG1.PK2": {"PACKAGE", "NUMBER"},
		"ADAM.F3":        {"FUNCTION", "DATE"},
	} {
		fn, ok := got[name]
		if !ok {
			t.Errorf("Functions missing %s (got %v)", name, keys(got))
			continue
		}
		if fn.Type != w.typ {
			t.Errorf("%s Type = %q, want %q", name, fn.Type, w.typ)
		}
		if fn.ResultType != w.result {
			t.Errorf("%s ResultType = %q, want %q", name, fn.ResultType, w.result)
		}
	}
}

// TestFunctionColumnsFiltersPerRoutine: the argument lookup must answer
// with exactly the named routine's arguments — an unparenthesized type
// predicate used to let every FUNCTION bypass the filters, so \df showed
// all functions' arguments mashed into each row. Package routines are
// addressed by their "PKG.FUNC" specific names and report IN/IN OUT as
// strings, not JDBC numeric codes.
func TestFunctionColumnsFiltersPerRoutine(t *testing.T) {
	r := NewReaderQ()(openFunctionsDB(t)).(metadata.FunctionColumnReader)

	// plain function: only its own argument
	set, err := r.FunctionColumns(metadata.Filter{Schema: "SCOTT", Parent: "F1", WithSystem: true})
	if err != nil {
		t.Fatalf("FunctionColumns(F1): %v", err)
	}
	defer set.Close()
	var args []string
	for set.Next() {
		c := set.Get()
		if c.OrdinalPosition == 0 {
			continue
		}
		args = append(args, c.Type+" "+c.Name+" "+c.DataType)
	}
	if len(args) != 1 || args[0] != "IN A_IN NUMBER" {
		t.Errorf("F1 args = %v, want [IN A_IN NUMBER]", args)
	}

	// package routine: addressed as PKG.FUNC
	set2, err := r.FunctionColumns(metadata.Filter{Schema: "SCOTT", Parent: "PKG1.PK1", WithSystem: true})
	if err != nil {
		t.Fatalf("FunctionColumns(PKG1.PK1): %v", err)
	}
	defer set2.Close()
	args = nil
	for set2.Next() {
		c := set2.Get()
		if c.OrdinalPosition == 0 {
			continue
		}
		args = append(args, c.Type+" "+c.Name+" "+c.DataType)
	}
	if len(args) != 1 || args[0] != "IN X_IN NUMBER" {
		t.Errorf("PKG1.PK1 args = %v, want [IN X_IN NUMBER]", args)
	}
}

// TestDescribeFunctionsRendersOwnArgsOnly: the \df output end to end — each
// row renders only its own arguments and type.
func TestDescribeFunctionsRendersOwnArgsOnly(t *testing.T) {
	db := openFunctionsDB(t)
	u, err := dburl.Parse("oracle://scott:tiger@localhost:1521/orcl")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := metadata.NewDefaultWriter(NewReaderQ()(db))(db, &buf)
	if err := w.DescribeFunctions(u, "f", "", false, false); err != nil {
		t.Fatalf("DescribeFunctions: %v", err)
	}
	out := buf.String()
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "| F1 "):
			if !strings.Contains(line, "A_IN") || strings.Contains(line, "B_IN") || strings.Contains(line, "C_IN") {
				t.Errorf("\\df F1 row shows foreign args: %s", strings.TrimSpace(line))
			}
		case strings.Contains(line, "| F2 "):
			if strings.Contains(line, "A_IN") || !strings.Contains(line, "B_IN") {
				t.Errorf("\\df F2 row shows foreign args: %s", strings.TrimSpace(line))
			}
		case strings.Contains(line, "| P1 "):
			if !strings.Contains(line, "IN OUT P_IN DATE") {
				t.Errorf("\\df P1 row missing IN OUT argument: %s", strings.TrimSpace(line))
			}
		case strings.Contains(line, "| PKG1.PK1 "):
			if !strings.Contains(line, "X_IN") || strings.Contains(line, "P_IN") {
				t.Errorf("\\df PKG1.PK1 row shows foreign args: %s", strings.TrimSpace(line))
			}
		}
	}
}

func keys(m map[string]metadata.Function) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
