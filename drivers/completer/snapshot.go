package completer

import (
	"regexp"
	"strings"
	"sync"

	"github.com/xo/usql/drivers/metadata"
)

// snapshotReader serves catalog candidate queries from a connect-time
// snapshot: the visible tables, functions, sequences and schemas load once
// in the background (through the wrapped reader, so its timeout and row
// limit apply), and keystroke-time queries filter them in memory. Filters
// the snapshot cannot answer — name/parent patterns, system-object
// requests, column and index lookups — fall through to the wrapped reader,
// which the cachedReader in front of the database keeps responsive.
//
// The snapshot rebuilds on Invalidate (session scope changes) and a
// reconnect builds a fresh one with its completer.
type snapshotReader struct {
	inner metadata.Reader

	mu   sync.RWMutex
	snap *snapshot
	gen  int64 // guards against a stale load overwriting a newer one
	seq  int64 // bumps whenever the snapshot state changes (publish, invalidate)
}

// snapshot holds one kind of catalog rows per reader sub-interface, with a
// loaded flag: a kind that failed to load keeps falling through instead of
// serving an empty list.
type snapshot struct {
	tables    []metadata.Table
	functions []metadata.Function
	sequences []metadata.Sequence
	schemas   []metadata.Schema

	tablesOK    bool
	functionsOK bool
	sequencesOK bool
	schemasOK   bool
}

var (
	_ metadata.Reader           = &snapshotReader{}
	_ interface{ Invalidate() } = &snapshotReader{}
)

// NewSnapshotReader wraps a metadata.Reader with a connect-time catalog
// snapshot and starts the background load.
func NewSnapshotReader(inner metadata.Reader) *snapshotReader {
	s := &snapshotReader{inner: inner}
	go s.load()
	return s
}

// load fetches the reachable catalog objects: everything in non-system
// schemas, not just the current search path/schema — completion must offer
// cross-schema objects to superusers and users granted across schemas
// (problem: \dt in Oracle only saw CURRENT_SCHEMA). Each kind loads
// independently.
func (s *snapshotReader) load() {
	s.mu.RLock()
	g := s.gen
	s.mu.RUnlock()
	snap := &snapshot{}
	if r, ok := s.inner.(metadata.TableReader); ok {
		if set, err := r.Tables(metadata.Filter{WithSystem: false}); err == nil {
			snap.tablesOK = true
			defer set.Close()
			for set.Next() {
				snap.tables = append(snap.tables, *set.Get())
			}
		}
	}
	if r, ok := s.inner.(metadata.FunctionReader); ok {
		if set, err := r.Functions(metadata.Filter{WithSystem: false}); err == nil {
			snap.functionsOK = true
			defer set.Close()
			for set.Next() {
				snap.functions = append(snap.functions, *set.Get())
			}
		}
	}
	if r, ok := s.inner.(metadata.SequenceReader); ok {
		if set, err := r.Sequences(metadata.Filter{WithSystem: false}); err == nil {
			snap.sequencesOK = true
			defer set.Close()
			for set.Next() {
				snap.sequences = append(snap.sequences, *set.Get())
			}
		}
	}
	if r, ok := s.inner.(metadata.SchemaReader); ok {
		if set, err := r.Schemas(metadata.Filter{WithSystem: false}); err == nil {
			snap.schemasOK = true
			defer set.Close()
			for set.Next() {
				snap.schemas = append(snap.schemas, *set.Get())
			}
		}
	}
	s.mu.Lock()
	if s.gen == g {
		s.seq++
		s.snap = snap
	}
	s.mu.Unlock()
}

// Invalidate drops the snapshot and reloads it in the background.
func (s *snapshotReader) Invalidate() {
	s.mu.Lock()
	s.gen++
	s.seq++
	s.snap = nil
	s.mu.Unlock()
	go s.load()
}

// current returns the loaded snapshot, or nil while none is available.
func (s *snapshotReader) current() *snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// state reports the snapshot's version and whether one has finished
// loading. The version changes on every state transition — including the
// very first publish, where gen alone would not move — so callers stamping
// cached results drop pre-snapshot (empty or fall-through) results the
// moment the snapshot supersedes them.
func (s *snapshotReader) state() (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq, s.snap != nil
}

// serveable reports whether a filter is answerable from the snapshot: a
// whole-catalog request without name patterns — the shape the completion hot
// paths use. The snapshot holds all reachable (non-system) schemas, so an
// OnlyVisible request is served from it too: bare `\dt foo` completes across
// schemas, which is the point (the login can query what it can query); the
// old visible-scope restriction hid granted objects from superusers.
func serveable(f metadata.Filter) bool {
	return !f.WithSystem && f.Name == "" && f.Parent == "" &&
		f.Reference == ""
}

// deferred reports whether a query should be answered empty instead of
// falling through to the inner reader: while the snapshot is still loading,
// whole-visible-catalog queries (the exact shapes the snapshot loads, with
// or without name patterns) would duplicate the in-flight load and block
// synchronous completion on a slow catalog. Scoped queries (WithSystem),
// column lookups (Parent) and references are independent of the snapshot
// and still pass through. Empty results are stamped with the current
// generation, so the completion caches drop them the moment the snapshot
// lands and recompute from it.
func (s *snapshotReader) deferred(f metadata.Filter) bool {
	if s.current() != nil {
		return false
	}
	return !f.WithSystem && f.Parent == "" && f.Reference == ""
}

func (s *snapshotReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	if snap := s.current(); snap != nil && snap.tablesOK && serveable(f) {
		rows := make([]metadata.Table, 0, len(snap.tables))
		for _, t := range snap.tables {
			if filterMatches(f, t.Catalog, t.Schema, t.Type) {
				rows = append(rows, t)
			}
		}
		return metadata.NewTableSet(rows), nil
	}
	if s.deferred(f) {
		return metadata.NewTableSet(nil), nil
	}
	if inner, ok := s.inner.(metadata.TableReader); ok {
		return inner.Tables(f)
	}
	return metadata.NewTableSet(nil), nil
}

func (s *snapshotReader) Functions(f metadata.Filter) (*metadata.FunctionSet, error) {
	if snap := s.current(); snap != nil && snap.functionsOK && serveable(f) {
		rows := make([]metadata.Function, 0, len(snap.functions))
		for _, fn := range snap.functions {
			if filterMatches(f, fn.Catalog, fn.Schema, fn.Type) {
				rows = append(rows, fn)
			}
		}
		return metadata.NewFunctionSet(rows), nil
	}
	if s.deferred(f) {
		return metadata.NewFunctionSet(nil), nil
	}
	if inner, ok := s.inner.(metadata.FunctionReader); ok {
		return inner.Functions(f)
	}
	return metadata.NewFunctionSet(nil), nil
}

func (s *snapshotReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	if snap := s.current(); snap != nil && snap.sequencesOK && serveable(f) {
		rows := make([]metadata.Sequence, 0, len(snap.sequences))
		for _, sq := range snap.sequences {
			if filterMatches(f, sq.Catalog, sq.Schema, "") {
				rows = append(rows, sq)
			}
		}
		return metadata.NewSequenceSet(rows), nil
	}
	if s.deferred(f) {
		return metadata.NewSequenceSet(nil), nil
	}
	if inner, ok := s.inner.(metadata.SequenceReader); ok {
		return inner.Sequences(f)
	}
	return metadata.NewSequenceSet(nil), nil
}

func (s *snapshotReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	if snap := s.current(); snap != nil && snap.schemasOK && serveable(f) {
		rows := make([]metadata.Schema, 0, len(snap.schemas))
		for _, sc := range snap.schemas {
			if filterMatches(f, sc.Catalog, "", "") {
				rows = append(rows, sc)
			}
		}
		return metadata.NewSchemaSet(rows), nil
	}
	if s.deferred(f) {
		return metadata.NewSchemaSet(nil), nil
	}
	if inner, ok := s.inner.(metadata.SchemaReader); ok {
		return inner.Schemas(f)
	}
	return metadata.NewSchemaSet(nil), nil
}

func (s *snapshotReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	if inner, ok := s.inner.(metadata.ColumnReader); ok {
		return inner.Columns(f)
	}
	return metadata.NewColumnSet(nil), nil
}

func (s *snapshotReader) Catalogs(f metadata.Filter) (*metadata.CatalogSet, error) {
	if inner, ok := s.inner.(metadata.CatalogReader); ok {
		return inner.Catalogs(f)
	}
	return metadata.NewCatalogSet(nil), nil
}

func (s *snapshotReader) Indexes(f metadata.Filter) (*metadata.IndexSet, error) {
	if inner, ok := s.inner.(metadata.IndexReader); ok {
		return inner.Indexes(f)
	}
	return metadata.NewIndexSet(nil), nil
}

// filterMatches reports whether a snapshot row satisfies a serveable
// filter's catalog, schema and type constraints. Catalog and schema are
// treated as patterns (SQL LIKE style: % any sequence, _ one rune, matched
// case-insensitively), though the snapshot's own hot paths pass plain
// names.
func filterMatches(f metadata.Filter, catalog, schema, typ string) bool {
	if !patternMatches(f.Catalog, catalog) || !patternMatches(f.Schema, schema) {
		return false
	}
	if typ == "" {
		// rows without a type (sequences, schemas): the inner readers ignore
		// Types for them, so the snapshot must too
		return true
	}
	for _, t := range f.Types {
		if strings.EqualFold(t, typ) {
			return true
		}
	}
	return len(f.Types) == 0
}

// patternMatches reports whether name matches pattern, or pattern is empty.
func patternMatches(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	if !strings.ContainsAny(pattern, "%_") {
		return strings.EqualFold(pattern, name)
	}
	var b strings.Builder
	b.WriteString("(?is)^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(name)
}
