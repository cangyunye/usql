package completer

import (
	"sync"
	"testing"
	"time"

	"github.com/xo/usql/drivers/metadata"
)

// snapReader serves fixed catalog rows and counts queries.
type snapReader struct {
	countingReader
	tables     []metadata.Table
	schemas    []metadata.Schema
	failTables bool
}

func (r *snapReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.count("tables")
	if r.failTables {
		return nil, errSnapFail
	}
	rows := make([]metadata.Table, 0, len(r.tables))
	for _, t := range r.tables {
		if filterMatches(f, t.Catalog, t.Schema, t.Type) {
			rows = append(rows, t)
		}
	}
	return metadata.NewTableSet(rows), nil
}

func (r *snapReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	r.count("schemas")
	rows := make([]metadata.Schema, 0, len(r.schemas))
	for _, s := range r.schemas {
		if filterMatches(f, s.Catalog, "", "") {
			rows = append(rows, s)
		}
	}
	return metadata.NewSchemaSet(rows), nil
}

var errSnapFail = errSnapshotFailed{}

type errSnapshotFailed struct{}

func (errSnapshotFailed) Error() string { return "snapshot load failed" }

func waitForSnapshot(t *testing.T, s *snapshotReader) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.current() != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("snapshot did not load in time")
}

func tableNames(s *metadata.TableSet) []string {
	var out []string
	for s.Next() {
		out = append(out, s.Get().Name)
	}
	return out
}

func TestSnapshotServesFromMemory(t *testing.T) {
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}}, tables: []metadata.Table{
		{Schema: "public", Name: "film", Type: "TABLE"},
		{Schema: "public", Name: "actor", Type: "VIEW"},
		{Schema: "private", Name: "secret", Type: "TABLE"},
	}}
	snap := NewSnapshotReader(inner)
	waitForSnapshot(t, snap)
	r := metadata.Reader(snap)
	tr := r.(metadata.TableReader)

	// visible-object queries come from the snapshot
	set, err := tr.Tables(metadata.Filter{OnlyVisible: true, Types: []string{"TABLE"}})
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if names := tableNames(set); len(names) != 2 || names[0] != "film" {
		t.Fatalf("filtered tables: %v", names)
	}
	if n := inner.calls("tables"); n != 1 { // only the load itself
		t.Fatalf("inner queried %d times, want 1", n)
	}

	// name-pattern and system-object queries fall through
	if _, err := tr.Tables(metadata.Filter{OnlyVisible: true, Name: "film%"}); err != nil {
		t.Fatalf("pattern query: %v", err)
	}
	if _, err := tr.Tables(metadata.Filter{WithSystem: true}); err != nil {
		t.Fatalf("system query: %v", err)
	}
	if n := inner.calls("tables"); n != 3 {
		t.Fatalf("inner queried %d times after fall-throughs, want 3", n)
	}
}

func TestSnapshotSchemas(t *testing.T) {
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}}, schemas: []metadata.Schema{
		{Schema: "public"}, {Schema: "private"},
	}}
	snap := NewSnapshotReader(inner)
	waitForSnapshot(t, snap)
	r := metadata.Reader(snap)
	sr := r.(metadata.SchemaReader)
	set, err := sr.Schemas(metadata.Filter{OnlyVisible: true})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	var names []string
	for set.Next() {
		names = append(names, set.Get().Schema)
	}
	if len(names) != 2 || names[0] != "public" {
		t.Fatalf("schemas: %v", names)
	}
	if n := inner.calls("schemas"); n != 1 {
		t.Fatalf("inner queried %d times, want 1", n)
	}
}

func TestSnapshotFailedKindFallsThrough(t *testing.T) {
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}}, failTables: true}
	snap := NewSnapshotReader(inner)
	waitForSnapshot(t, snap)
	r := metadata.Reader(snap)
	tr := r.(metadata.TableReader)
	if _, err := tr.Tables(metadata.Filter{OnlyVisible: true}); err == nil {
		t.Fatal("expected the inner reader's error to surface")
	}
}

func TestSnapshotInvalidateReloads(t *testing.T) {
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}}, tables: []metadata.Table{
		{Schema: "public", Name: "film", Type: "TABLE"},
	}}
	snap := NewSnapshotReader(inner)
	waitForSnapshot(t, snap)
	snap.Invalidate()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := snap.current(); s != nil && len(s.tables) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if s := snap.current(); s == nil || len(s.tables) != 1 {
		t.Fatal("snapshot did not reload after Invalidate")
	}
}

func TestSnapshotConcurrentServe(t *testing.T) {
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}}, tables: []metadata.Table{
		{Schema: "public", Name: "film", Type: "TABLE"},
	}}
	snap := NewSnapshotReader(inner)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := metadata.TableReader(snap)
			for k := 0; k < 20; k++ {
				set, err := tr.Tables(metadata.Filter{OnlyVisible: true})
				if err != nil {
					t.Error(err)
					return
				}
				set.Close()
			}
		}()
	}
	wg.Wait()
}
