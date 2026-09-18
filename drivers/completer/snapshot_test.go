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
	sequences  []metadata.Sequence
	failTables bool
}

func (r *snapReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	r.count("sequences")
	rows := make([]metadata.Sequence, 0, len(r.sequences))
	for _, s := range r.sequences {
		if filterMatches(f, s.Catalog, s.Schema, "") {
			rows = append(rows, s)
		}
	}
	return metadata.NewSequenceSet(rows), nil
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

func TestSnapshotSequencesIgnoreTypes(t *testing.T) {
	// the inner sequence readers ignore Filter.Types (sequences carry no
	// type); the snapshot must serve them the same way, or FROM-position
	// candidates lose their sequences once the snapshot is loaded
	inner := &snapReader{countingReader: countingReader{queries: map[string]int{}},
		sequences: []metadata.Sequence{{Schema: "public", Name: "film_id_seq"}}}
	snap := NewSnapshotReader(inner)
	waitForSnapshot(t, snap)
	r := metadata.SequenceReader(snap)
	set, err := r.Sequences(metadata.Filter{OnlyVisible: true, Types: []string{"TABLE", "VIEW"}})
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	var names []string
	for set.Next() {
		names = append(names, set.Get().Name)
	}
	if len(names) != 1 || names[0] != "film_id_seq" {
		t.Fatalf("sequences with a table Types filter: %v", names)
	}
}

// gatedReader holds the snapshot's Tables load open until the gate closes,
// so tests can inspect the loading window deterministically.
type gatedReader struct {
	countingReader
	gate   chan struct{}
	tables []metadata.Table
}

func (r *gatedReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.count("tables")
	<-r.gate
	rows := make([]metadata.Table, 0, len(r.tables))
	for _, t := range r.tables {
		if filterMatches(f, t.Catalog, t.Schema, t.Type) {
			rows = append(rows, t)
		}
	}
	return metadata.NewTableSet(rows), nil
}

func (r *gatedReader) Functions(metadata.Filter) (*metadata.FunctionSet, error) {
	return metadata.NewFunctionSet(nil), nil
}

func (r *gatedReader) Sequences(metadata.Filter) (*metadata.SequenceSet, error) {
	return metadata.NewSequenceSet(nil), nil
}

func (r *gatedReader) Schemas(metadata.Filter) (*metadata.SchemaSet, error) {
	return metadata.NewSchemaSet(nil), nil
}

func TestSnapshotLoadingWindowDefersQueries(t *testing.T) {
	inner := &gatedReader{
		countingReader: countingReader{queries: map[string]int{}},
		gate:           make(chan struct{}),
		tables:         []metadata.Table{{Schema: "public", Name: "film", Type: "TABLE"}},
	}
	snap := NewSnapshotReader(inner) // the load blocks on the gate
	tr := metadata.TableReader(snap)

	// during the load, a visible-catalog query is answered empty right away
	// — nothing blocks on the in-flight load
	t0 := time.Now()
	set, err := tr.Tables(metadata.Filter{OnlyVisible: true})
	if err != nil {
		t.Fatalf("Tables during load: %v", err)
	}
	if elapsed := time.Since(t0); elapsed > 250*time.Millisecond {
		t.Fatalf("deferred query took %v, must not wait on the load", elapsed)
	}
	if names := tableNames(set); len(names) != 0 {
		t.Fatalf("deferred query returned %v, want empty", names)
	}

	// once the load lands, the same query is served from the snapshot, and
	// the inner reader has seen exactly one query — the load itself (the
	// deferred query above issued none)
	close(inner.gate)
	waitForSnapshot(t, snap)
	set, err = tr.Tables(metadata.Filter{OnlyVisible: true})
	if err != nil {
		t.Fatalf("Tables after load: %v", err)
	}
	if names := tableNames(set); len(names) != 1 || names[0] != "film" {
		t.Fatalf("tables after load: %v", names)
	}
	if n := inner.calls("tables"); n != 1 {
		t.Fatalf("inner queried %d times in total, want 1 (the load only)", n)
	}
}
