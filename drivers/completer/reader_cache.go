package completer

import (
	"fmt"
	"sync"
	"time"

	"github.com/xo/usql/drivers/metadata"
)

// readerCacheTTL is how long a metadata query result is reused before the
// next query goes to the database again. Typing produces many completion
// requests for the same statement scope, so a short TTL turns them into one
// query plus in-memory filtering.
const readerCacheTTL = time.Minute

// readerCacheSize caps the number of cached queries (FIFO eviction).
const readerCacheSize = 128

// NewCachedReader wraps a metadata.Reader so that identical queries within
// the TTL are served from memory. The wrapper implements every reader
// interface; queries the wrapped reader does not support return empty sets,
// which behaves exactly like the failed type assertions did before.
//
// Callers should use stable filters (no typed text embedded in the name
// pattern) so successive keystrokes hit the same cache entry.
func NewCachedReader(inner metadata.Reader) metadata.Reader {
	return newCachedReader(inner, readerCacheTTL)
}

func newCachedReader(inner metadata.Reader, ttl time.Duration) metadata.Reader {
	return &cachedReader{
		inner:   inner,
		ttl:     ttl,
		entries: map[string]*cacheEntry{},
	}
}

type cacheEntry struct {
	val any
	exp time.Time
}

type cachedReader struct {
	inner metadata.Reader
	ttl   time.Duration

	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string
}

// load returns a cached value for op+f when fresh enough, otherwise runs
// fresh and caches its result. Errors are never cached.
func (c *cachedReader) load(op string, f metadata.Filter, fresh func() (any, error)) (any, error) {
	key := op + " " + fmt.Sprintf("%+v", f)
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.exp) {
		val := e.val
		c.mu.Unlock()
		return val, nil
	}
	c.mu.Unlock()

	val, err := fresh()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
		for len(c.order) > readerCacheSize {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.entries[key] = &cacheEntry{val: val, exp: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return val, nil
}

// clear drops every cached query, e.g. when the session scope changed
// (USE, SET search_path, ALTER SESSION SET CURRENT_SCHEMA).
func (c *cachedReader) clear() {
	c.mu.Lock()
	c.entries = map[string]*cacheEntry{}
	c.order = nil
	c.mu.Unlock()
}

func (c *cachedReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	inner, ok := c.inner.(metadata.TableReader)
	if !ok {
		return metadata.NewTableSet(nil), nil
	}
	v, err := c.load("tables", f, func() (any, error) {
		set, err := inner.Tables(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Table
		for set.Next() {
			t := set.Get()
			out = append(out, *t)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewTableSet(v.([]metadata.Table)), nil
}

func (c *cachedReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	inner, ok := c.inner.(metadata.ColumnReader)
	if !ok {
		return metadata.NewColumnSet(nil), nil
	}
	v, err := c.load("columns", f, func() (any, error) {
		set, err := inner.Columns(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Column
		for set.Next() {
			col := set.Get()
			out = append(out, *col)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewColumnSet(v.([]metadata.Column)), nil
}

func (c *cachedReader) Functions(f metadata.Filter) (*metadata.FunctionSet, error) {
	inner, ok := c.inner.(metadata.FunctionReader)
	if !ok {
		return metadata.NewFunctionSet(nil), nil
	}
	v, err := c.load("functions", f, func() (any, error) {
		set, err := inner.Functions(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Function
		for set.Next() {
			fn := set.Get()
			out = append(out, *fn)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewFunctionSet(v.([]metadata.Function)), nil
}

func (c *cachedReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	inner, ok := c.inner.(metadata.SequenceReader)
	if !ok {
		return metadata.NewSequenceSet(nil), nil
	}
	v, err := c.load("sequences", f, func() (any, error) {
		set, err := inner.Sequences(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Sequence
		for set.Next() {
			s := set.Get()
			out = append(out, *s)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewSequenceSet(v.([]metadata.Sequence)), nil
}

func (c *cachedReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	inner, ok := c.inner.(metadata.SchemaReader)
	if !ok {
		return metadata.NewSchemaSet(nil), nil
	}
	v, err := c.load("schemas", f, func() (any, error) {
		set, err := inner.Schemas(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Schema
		for set.Next() {
			s := set.Get()
			out = append(out, *s)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewSchemaSet(v.([]metadata.Schema)), nil
}

func (c *cachedReader) Catalogs(f metadata.Filter) (*metadata.CatalogSet, error) {
	inner, ok := c.inner.(metadata.CatalogReader)
	if !ok {
		return metadata.NewCatalogSet(nil), nil
	}
	v, err := c.load("catalogs", f, func() (any, error) {
		set, err := inner.Catalogs(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Catalog
		for set.Next() {
			out = append(out, set.Get())
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewCatalogSet(v.([]metadata.Catalog)), nil
}

func (c *cachedReader) Indexes(f metadata.Filter) (*metadata.IndexSet, error) {
	inner, ok := c.inner.(metadata.IndexReader)
	if !ok {
		return metadata.NewIndexSet(nil), nil
	}
	v, err := c.load("indexes", f, func() (any, error) {
		set, err := inner.Indexes(f)
		if err != nil {
			return nil, err
		}
		defer set.Close()
		var out []metadata.Index
		for set.Next() {
			i := set.Get()
			out = append(out, *i)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return metadata.NewIndexSet(v.([]metadata.Index)), nil
}
