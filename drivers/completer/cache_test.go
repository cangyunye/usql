package completer

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testObjs(names ...string) []obj {
	out := make([]obj, 0, len(names))
	for _, n := range names {
		out = append(out, obj{name: n, kind: "table"})
	}
	return out
}

// waitFor polls until cond passes or the timeout expires.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestCacheLazyLoadAndHit(t *testing.T) {
	c := newCatalogCache()
	var calls atomic.Int32
	load := func() ([]obj, error) {
		calls.Add(1)
		return testObjs("public", "sales"), nil
	}
	// first get: miss, load starts in the background
	objs, ok := c.get(bucketSchema, "", load)
	if ok || objs != nil {
		t.Fatalf("first get served %+v ok=%v, want miss", objs, ok)
	}
	waitFor(t, func() bool { _, bytes := c.stats(); return bytes > 0 })
	// second get: hit, no new load
	objs, ok = c.get(bucketSchema, "", load)
	if !ok || len(objs) != 2 || objs[0].name != "public" {
		t.Fatalf("second get: %+v ok=%v", objs, ok)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader ran %d times, want 1", calls.Load())
	}
	// a different key loads independently
	_, _ = c.get(bucketSchema, "other", load)
	waitFor(t, func() bool { return calls.Load() == 2 })
}

func TestCacheStaleWhileRevalidate(t *testing.T) {
	c := newCatalogCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	var calls atomic.Int32
	load := func() ([]obj, error) {
		calls.Add(1)
		return testObjs("a"), nil
	}
	_, _ = c.get(bucketColumn, "t", load)
	waitFor(t, func() bool { _, bytes := c.stats(); return bytes > 0 })
	// still fresh: no reload
	if _, ok := c.get(bucketColumn, "t", load); !ok || calls.Load() != 1 {
		t.Fatalf("fresh get reloaded: calls=%d", calls.Load())
	}
	// past the column TTL: the stale entry is served while a reload runs
	now = now.Add(3 * time.Minute)
	objs, ok := c.get(bucketColumn, "t", load)
	if !ok || len(objs) != 1 {
		t.Fatalf("stale get: %+v ok=%v", objs, ok)
	}
	waitFor(t, func() bool { return calls.Load() == 2 })
}

func TestCacheInvalidateDropsInFlight(t *testing.T) {
	c := newCatalogCache()
	release := make(chan struct{})
	load := func() ([]obj, error) {
		<-release
		return testObjs("late"), nil
	}
	_, _ = c.get(bucketSchema, "", load)
	c.Invalidate()
	// a get after invalidation re-arms its own load; both loads are now in
	// flight. Release them: the first (stale generation) must be discarded.
	_, ok := c.get(bucketSchema, "", func() ([]obj, error) { return testObjs("fresh"), nil })
	if ok {
		t.Fatal("get after invalidate served a hit")
	}
	close(release)
	waitFor(t, func() bool {
		objs, ok := c.get(bucketSchema, "", func() ([]obj, error) { return testObjs("fresh"), nil })
		return ok && len(objs) == 1 && objs[0].name == "fresh"
	})
}

func TestCacheErrorsNotCached(t *testing.T) {
	c := newCatalogCache()
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	load := func() ([]obj, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("boom")
		}
		return testObjs("ok"), nil
	}
	_, _ = c.get(bucketObject, "public", load)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.loading[bucketKey(bucketObject, "public")]
	})
	if _, n := c.stats(); n != 0 {
		t.Fatalf("error cached: %d entries", n)
	}
	fail.Store(false)
	objs, ok := c.get(bucketObject, "public", load)
	if ok {
		t.Fatal("retry served a hit")
	}
	waitFor(t, func() bool {
		objs, ok := c.get(bucketObject, "public", load)
		return ok && len(objs) == 1
	})
	_ = objs
}

func TestCacheLRUEviction(t *testing.T) {
	c := newCatalogCache()
	c.budget = 256 // a few hundred bytes: holds a handful of small entries
	load := func() ([]obj, error) {
		return testObjs(strings.Repeat("x", 40)), nil
	}
	for i := 0; i < 20; i++ {
		_, _ = c.get(bucketColumn, strings.Repeat("k", i+1), load)
	}
	// steady state: each ~125-byte entry fits two per 256-byte budget
	waitFor(t, func() bool {
		entries, bytes := c.stats()
		return entries >= 2 && bytes <= c.budget
	})
	entries, bytes := c.stats()
	if bytes > c.budget {
		t.Fatalf("over budget: %d bytes", bytes)
	}
	if entries < 2 {
		t.Fatalf("evicted everything: %d entries", entries)
	}
}

func TestCacheKickFires(t *testing.T) {
	c := newCatalogCache()
	kicks := make(chan struct{}, 1)
	c.SetKick(func() {
		select {
		case kicks <- struct{}{}:
		default:
		}
	})
	_, _ = c.get(bucketSchema, "", func() ([]obj, error) { return testObjs("a"), nil })
	select {
	case <-kicks:
	case <-time.After(2 * time.Second):
		t.Fatal("kick did not fire after load")
	}
}

func TestCacheBudgetEnv(t *testing.T) {
	t.Setenv("USQL_CACHE_MB", "3")
	if got := cacheBudget(); got != 3*1024*1024 {
		t.Fatalf("budget = %d", got)
	}
	t.Setenv("USQL_CACHE_MB", "bogus")
	if got := cacheBudget(); got != 10*1024*1024 {
		t.Fatalf("bogus budget = %d", got)
	}
}
