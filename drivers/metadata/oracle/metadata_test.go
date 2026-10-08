package oracle

import (
	"database/sql"
	"database/sql/driver"
	"sync"
	"testing"

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
