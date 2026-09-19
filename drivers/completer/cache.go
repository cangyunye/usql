package completer

import (
	"container/list"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// obj is one cached completion candidate: an object name and its display
// kind ("table", "view", "sequence", "column", ...).
type obj struct {
	name string
	kind string
}

// bucket is a cache level, in dependency order: L1 schemas visible to the
// login, L2 a schema's objects, L3 a table's columns. Each level is loaded
// only on demand — connecting issues no metadata queries at all.
type bucket int

const (
	bucketSchema bucket = iota
	bucketObject
	bucketColumn
)

// bucketTTL is the freshness window per level. Lower levels change more
// often (DDL on a table) and cost less to refetch, so they expire sooner.
var bucketTTL = map[bucket]time.Duration{
	bucketSchema: 10 * time.Minute,
	bucketObject: 5 * time.Minute,
	bucketColumn: 2 * time.Minute,
}

// catalogCache is the completion candidate cache: a byte-budgeted LRU whose
// entries are filled lazily and asynchronously.
//
// A get returns cached entries when fresh — and stale entries while a
// reload runs (stale-while-revalidate). On a miss it returns nothing and
// runs the bucket's loader in a goroutine: completion never blocks on the
// database. When a load lands, the kick fires so the UI re-requests and
// picks up the new candidates.
type catalogCache struct {
	// kick is invoked after a load stores new entries; wired to the UI's
	// re-render hook by SetKick.
	kick func()

	mu      sync.Mutex
	entries map[string]*catalogEntry
	order   *list.List // front = most recently used
	bytes   int64
	budget  int64
	loading map[string]bool
	gen     int64 // bumped by Invalidate; in-flight loads check it

	// now is swappable for tests
	now func() time.Time
}

// catalogEntry is one stored bucket: the entries, when they were loaded, and
// their approximate byte size for LRU accounting.
type catalogEntry struct {
	key  string
	objs []obj
	at   time.Time
	size int64
	el   *list.Element
}

// newCatalogCache builds an empty cache with the USQL_CACHE_MB budget
// (default 10MB).
func newCatalogCache() *catalogCache {
	return &catalogCache{
		entries: map[string]*catalogEntry{},
		order:   list.New(),
		loading: map[string]bool{},
		budget:  cacheBudget(),
		now:     time.Now,
	}
}

// cacheBudget reads the cache budget in megabytes.
func cacheBudget() int64 {
	mb := 10
	if v := os.Getenv("USQL_CACHE_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 4096 {
			mb = n
		}
	}
	return int64(mb) * 1024 * 1024
}

// SetKick wires the re-render hook called after each completed load.
func (c *catalogCache) SetKick(f func()) {
	c.mu.Lock()
	c.kick = f
	c.mu.Unlock()
}

// get returns the cached entries for the bucket and key, or (nil, false)
// after starting an asynchronous load. Expired entries are served while a
// reload runs in the background.
func (c *catalogCache) get(b bucket, key string, load func() ([]obj, error)) ([]obj, bool) {
	full := bucketKey(b, key)
	c.mu.Lock()
	if e, ok := c.entries[full]; ok {
		c.order.MoveToFront(e.el)
		fresh := c.now().Sub(e.at) < bucketTTL[b]
		if fresh || c.loading[full] {
			c.mu.Unlock()
			return e.objs, true
		}
		// stale: serve it and reload behind the caller's back
		c.loading[full] = true
		g := c.gen
		c.mu.Unlock()
		go c.run(b, full, g, load)
		return e.objs, true
	}
	if c.loading[full] {
		c.mu.Unlock()
		return nil, false
	}
	c.loading[full] = true
	g := c.gen
	c.mu.Unlock()
	go c.run(b, full, g, load)
	return nil, false
}

// run executes a load and publishes the result unless it errored or a scope
// change (Invalidate) dropped the bucket in the meantime.
func (c *catalogCache) run(b bucket, full string, g int64, load func() ([]obj, error)) {
	objs, err := load()
	var kick func()
	c.mu.Lock()
	delete(c.loading, full)
	if err == nil && g == c.gen {
		c.store(full, objs)
		kick = c.kick
	}
	c.mu.Unlock()
	if kick != nil {
		kick()
	}
}

// store inserts or replaces an entry and evicts least-recently-used entries
// while over budget. Called with mu held.
func (c *catalogCache) store(full string, objs []obj) {
	if e, ok := c.entries[full]; ok {
		c.bytes -= e.size
		c.order.Remove(e.el)
		delete(c.entries, full)
	}
	size := int64(64)
	for _, o := range objs {
		size += int64(len(o.name) + len(o.kind) + 16)
	}
	e := &catalogEntry{key: full, objs: objs, at: c.now(), size: size}
	e.el = c.order.PushFront(e)
	c.entries[full] = e
	c.bytes += size
	for c.bytes > c.budget && c.order.Len() > 1 {
		back := c.order.Back()
		old := back.Value.(*catalogEntry)
		c.order.Remove(back)
		delete(c.entries, old.key)
		c.bytes -= old.size
	}
}

// Invalidate drops every cached bucket. In-flight loads finish but their
// results are discarded against the generation, and the next get re-arms a
// fresh load.
func (c *catalogCache) Invalidate() {
	c.mu.Lock()
	c.gen++
	c.entries = map[string]*catalogEntry{}
	c.order.Init()
	c.bytes = 0
	c.mu.Unlock()
}

// stats reports the entry count and approximate byte usage (for tests).
func (c *catalogCache) stats() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.bytes
}

// bucketKey builds the full cache key for a bucket and its natural key.
func bucketKey(b bucket, key string) string {
	return fmt.Sprintf("%d|%s", b, key)
}
