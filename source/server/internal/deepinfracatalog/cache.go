package deepinfracatalog

import (
	"context"
	"sync"
	"time"
)

// indexCache holds the last successfully fetched index.
//
// Two behaviors matter, and they are separate:
//
//   - Fresh within the TTL: a browse inside the window costs nothing. The
//     index changes on the order of days, so this is nearly always a hit.
//   - Stale on failure: once an index has been fetched, a later fetch error
//     serves the old copy rather than failing. A momentarily unreachable
//     provider then degrades to slightly-outdated model metadata instead of
//     an empty models page. This mirrors the posture the rest of the catalog
//     takes toward online sources — browse is best-effort.
//
// The stale path is deliberately unbounded in age: a stale entry is strictly
// better than no entry for a browse list, and the alternative (expiring it)
// would turn a long provider outage into exactly the failure the cache exists
// to prevent.
type indexCache struct {
	mu        sync.Mutex
	models    []wireModel
	fetchedAt time.Time
	// inflight serializes concurrent fetches so a cold cache hit by several
	// browses at once issues one request, not several.
	inflight sync.Mutex
}

func newIndexCache() *indexCache { return &indexCache{} }

// get returns the cached index and whether it is within ttl.
func (c *indexCache) get(ttl time.Duration) (models []wireModel, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.models == nil {
		return nil, false
	}
	return c.models, time.Since(c.fetchedAt) < ttl
}

// put records a successful fetch.
func (c *indexCache) put(models []wireModel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = models
	c.fetchedAt = time.Now()
}

// indexAll returns the full index — cached when fresh, refetched when not,
// and served stale when a refetch fails.
func (s *Source) indexAll(ctx context.Context) ([]wireModel, error) {
	if models, fresh := s.cache.get(s.ttl); fresh {
		return models, nil
	}

	s.cache.inflight.Lock()
	defer s.cache.inflight.Unlock()
	// Re-check: another goroutine may have refreshed while this one waited.
	if models, fresh := s.cache.get(s.ttl); fresh {
		return models, nil
	}

	models, err := s.fetch(ctx)
	if err != nil {
		// Serve stale rather than fail, when there is anything to serve.
		if cached, _ := s.cache.get(s.ttl); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	s.cache.put(models)
	return models, nil
}

// index returns only the eligible models — the browsable set.
func (s *Source) index(ctx context.Context) ([]wireModel, error) {
	all, err := s.indexAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wireModel, 0, len(all))
	for _, m := range all {
		if eligible(m) {
			out = append(out, m)
		}
	}
	return out, nil
}
