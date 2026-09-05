package completer

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xo/usql/drivers/metadata"
)

// countingReader counts queries per method and serves fixed results.
type countingReader struct {
	mu      sync.Mutex
	queries map[string]int
}

func newCountingReader() *countingReader {
	return &countingReader{queries: map[string]int{}}
}

func (r *countingReader) count(op string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries[op]++
	return r.queries[op]
}

func (r *countingReader) calls(op string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries[op]
}

func (r *countingReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.count("tables")
	return metadata.NewTableSet([]metadata.Table{
		{Schema: "public", Name: "film", Type: "TABLE"},
	}), nil
}

func (r *countingReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	r.count("columns")
	return metadata.NewColumnSet([]metadata.Column{
		{Table: f.Parent, Name: "id"},
		{Table: f.Parent, Name: "name"},
	}), nil
}

func TestCachedReaderServesRepeats(t *testing.T) {
	inner := newCountingReader()
	r := NewCachedReader(inner)

	tr := r.(metadata.TableReader)
	for i := 0; i < 3; i++ {
		set, err := tr.Tables(metadata.Filter{OnlyVisible: true})
		if err != nil {
			t.Fatalf("Tables: %v", err)
		}
		var names []string
		for set.Next() {
			names = append(names, set.Get().Name)
		}
		set.Close()
		if len(names) != 1 || names[0] != "film" {
			t.Errorf("Tables round %d = %v, want [film]", i, names)
		}
	}
	if n := inner.calls("tables"); n != 1 {
		t.Errorf("inner queried %d times, want 1", n)
	}
}

func TestCachedReaderKeyIncludesFilter(t *testing.T) {
	inner := newCountingReader()
	r := NewCachedReader(inner)
	tr := r.(metadata.TableReader)

	tr.Tables(metadata.Filter{OnlyVisible: true})
	tr.Tables(metadata.Filter{Schema: "other", WithSystem: true})
	if n := inner.calls("tables"); n != 2 {
		t.Errorf("inner queried %d times, want 2 (distinct filters)", n)
	}
}

func TestCachedReaderExpires(t *testing.T) {
	inner := newCountingReader()
	r := newCachedReader(inner, time.Millisecond)
	tr := r.(metadata.TableReader)

	tr.Tables(metadata.Filter{OnlyVisible: true})
	time.Sleep(2 * time.Millisecond)
	tr.Tables(metadata.Filter{OnlyVisible: true})
	if n := inner.calls("tables"); n != 2 {
		t.Errorf("inner queried %d times, want 2 (entry expired)", n)
	}
}

func TestCachedReaderUnsupportedIsEmpty(t *testing.T) {
	// inner only implements Tables; the wrapper's other interfaces return
	// empty sets instead of erroring, mirroring failed assertions
	r := NewCachedReader(struct {
		metadata.TableReader
	}{newCountingReader()})
	if _, err := r.(metadata.ColumnReader).Columns(metadata.Filter{}); err != nil {
		t.Errorf("Columns on unsupported reader: %v", err)
	}
}

func TestCachedReaderConcurrent(t *testing.T) {
	inner := newCountingReader()
	r := NewCachedReader(inner)
	tr := r.(metadata.TableReader)

	var wg sync.WaitGroup
	var total atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			set, err := tr.Tables(metadata.Filter{OnlyVisible: true})
			if err != nil {
				t.Error(err)
				return
			}
			for set.Next() {
				total.Add(1)
			}
			set.Close()
		}()
	}
	wg.Wait()
	if total.Load() != 16 {
		t.Errorf("got %d rows total, want 16", total.Load())
	}
	if n := inner.calls("tables"); n > 16 {
		t.Errorf("inner queried %d times, want at most 16", n)
	}
}
