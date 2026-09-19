package oracle

import (
	"database/sql"
	"testing"

	"github.com/xo/usql/drivers/metadata"

	_ "modernc.org/sqlite"
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
