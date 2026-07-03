// Package ollamacatalog — manager.go: glue between the low-level
// Fetcher and the on-disk Cache. The Manager is what the rest of
// Cercano's server code talks to; individual callers should never poke
// at the Fetcher or write cache files directly.
//
// Responsibilities:
//
//   - On startup, load the cache from disk (if any). Serve stale cache
//     immediately; refresh in the background if past the TTL.
//   - Expose Models() (from cache) and Refresh() (force fetch).
//   - Run a periodic background refresh so the cache stays fresh even
//     for long-lived Cercano servers.
//   - Never fail the caller's request because the network is down —
//     the last-known-good cache is always what Models() returns, even
//     if the most recent refresh attempt failed.
package ollamacatalog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a cache is considered fresh before the
// background refresher tries to fetch again. 24 hours matches the CLI
// UX design (see docs/features/cli/model-catalog-online/design.md).
const DefaultTTL = 24 * time.Hour

// defaultRefreshInterval is how often the background refresher wakes
// up to CHECK the cache age. Chosen shorter than DefaultTTL so a fresh
// server picks up an expired cache reasonably quickly, but not so
// short that we hammer the ollama library on a healthy install.
const defaultRefreshInterval = time.Hour

// Manager wraps a Fetcher + on-disk cache and coordinates background
// refresh. Concurrent Models() / Refresh() / FetchedAt() calls are
// safe; the mutex protects the in-memory Cache pointer only, not the
// on-disk file (which is protected by the atomic-write helper).
type Manager struct {
	fetcher   *Fetcher
	cachePath string
	ttl       time.Duration

	mu    sync.RWMutex
	cache *Cache // nil until first Load succeeds or first Refresh completes

	// stopBg is closed by Stop() to signal the background refresher
	// goroutine to exit. nil when the background refresher isn't
	// running (the constructor doesn't start it — Start() does).
	stopBg chan struct{}

	// Estimate-warming state (see warm.go). warmAttempted tracks the
	// last attempt per ref so failures back off instead of hammering
	// the registry every wake; the intervals are test overrides (zero
	// means the package defaults).
	warmAttempted map[string]time.Time
	warmThrottle  time.Duration
	warmWake      time.Duration
}

// NewManager constructs a Manager with the given fetcher and cache
// path. Does not start the background refresher — call Start() to do
// that. Panics if fetcher is nil since a nil Fetcher would silently
// break all downstream calls.
func NewManager(fetcher *Fetcher, cachePath string) *Manager {
	if fetcher == nil {
		panic("ollamacatalog: NewManager: fetcher must not be nil")
	}
	return &Manager{
		fetcher:   fetcher,
		cachePath: cachePath,
		ttl:       DefaultTTL,
	}
}

// SetTTL overrides the freshness threshold. Useful for tests that want
// to force staleness in milliseconds instead of hours. Not exposed to
// end users — the design deliberately hardcodes 24h so we can adjust
// centrally if traffic patterns shift.
func (m *Manager) SetTTL(ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ttl = ttl
}

// Models returns the currently-cached list of models. Never returns an
// error — a missing cache returns an empty slice so callers can render
// a "loading…" state without special-casing errors. The FetchedAt
// timestamp reflects the age of the returned slice.
func (m *Manager) Models() []Model {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cache == nil {
		return nil
	}
	// Return a defensive copy so callers can't mutate our cache.
	out := make([]Model, len(m.cache.Models))
	copy(out, m.cache.Models)
	return out
}

// FetchedAt returns the time of the most recent successful fetch, or
// the zero value if we've never had a successful fetch (either the
// cache file was missing at startup and the background refresher has
// yet to complete a fetch, or every fetch attempt has failed).
func (m *Manager) FetchedAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cache == nil {
		return time.Time{}
	}
	return m.cache.FetchedAt
}

// LoadCache reads the on-disk cache into memory. Called once at
// server startup. A missing cache file is not an error — the first
// Refresh() will populate it. A corrupt cache is logged but overwritten
// on the next successful refresh.
func (m *Manager) LoadCache() error {
	c, err := Load(m.cachePath)
	if err != nil {
		// Corrupt cache — leave m.cache nil so the next Refresh
		// overwrites. Return the error so the caller can log it.
		return fmt.Errorf("ollamacatalog: load cache: %w", err)
	}
	m.mu.Lock()
	m.cache = c
	m.mu.Unlock()
	return nil
}

// Refresh force-fetches the model list from Ollama's library, updates
// the in-memory cache, and writes to disk. Blocks until complete.
// Errors are returned to the caller AND the previous cache is preserved
// so a failed refresh never breaks the served view.
func (m *Manager) Refresh(ctx context.Context) error {
	// The Fetcher doesn't take a context yet — but if the caller's
	// context is cancelled, they can bail from waiting. Actual fetch
	// completion happens on the Fetcher's goroutine timing.
	models, err := m.fetcher.ListModels()
	if err != nil {
		return err
	}
	fresh := &Cache{
		FetchedAt: time.Now().UTC(),
		Source:    m.fetcher.libraryURL() + "/library",
		Models:    models,
	}
	m.mu.Lock()
	// Estimates are keyed by immutable digests and revalidated on
	// their own TTL — a catalog refresh must not throw them away.
	if m.cache != nil {
		fresh.Estimates = m.cache.Estimates
	}
	m.cache = fresh
	m.mu.Unlock()
	// Write to disk. Save errors are worth surfacing (misconfigured
	// permissions, disk full) but they don't invalidate the in-memory
	// cache we just updated.
	if err := fresh.Save(m.cachePath); err != nil {
		return fmt.Errorf("ollamacatalog: save cache: %w", err)
	}
	return nil
}

// Start launches the background refresher goroutine. Safe to call
// multiple times (idempotent). Runs until Stop() is called or the ctx
// passed here is cancelled. Uses defaultRefreshInterval as the wake-up
// cadence; if the cache is stale when the tick fires, it fetches.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.stopBg != nil {
		m.mu.Unlock()
		return // already started
	}
	m.stopBg = make(chan struct{})
	stop := m.stopBg
	m.mu.Unlock()

	go m.refresherLoop(ctx, stop)
	go m.warmLoop(ctx, stop)
}

// Stop terminates the background refresher. Safe to call before
// Start() (no-op) and multiple times.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopBg == nil {
		return
	}
	select {
	case <-m.stopBg:
		// already closed
	default:
		close(m.stopBg)
	}
	m.stopBg = nil
}

// refresherLoop is the background goroutine body. Wakes up at
// defaultRefreshInterval, refreshes if the cache is stale, keeps
// serving the previous cache on failure. Exits on ctx cancel or Stop().
func (m *Manager) refresherLoop(ctx context.Context, stop chan struct{}) {
	// One immediate check at startup — if the cache is stale (or
	// missing entirely), fetch right away so users don't stare at an
	// empty list for the first hour.
	if m.needsRefresh() {
		_ = m.Refresh(ctx) // errors already logged by callers if they care
	}
	t := time.NewTicker(defaultRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
			if m.needsRefresh() {
				_ = m.Refresh(ctx)
			}
		}
	}
}

// needsRefresh reports whether the cache is past its TTL. Read-locks
// the mutex so callers don't need to.
func (m *Manager) needsRefresh() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache.IsStale(time.Now(), m.ttl)
}

// Resolve implements localruntime.OCIResolver: given an Ollama library
// reference of the form "name:tag" (e.g. "qwen2.5-coder:7b"), fetches
// the manifest from registry.ollama.ai, picks the model layer, and
// returns the blob URL + total size in bytes. Callers (the download
// manager) then treat the URL like any other HTTP GET — the blob is a
// raw GGUF file that both local runtimes can consume directly.
//
// Errors on malformed refs (missing colon or empty name/tag), on
// manifest fetch failure, and on manifests that have no model layer.
func (m *Manager) Resolve(ctx context.Context, ref string) (string, int64, error) {
	name, tag, ok := splitOllamaRef(ref)
	if !ok {
		return "", 0, fmt.Errorf("ollamacatalog: invalid ollama ref %q (want name:tag)", ref)
	}
	manifest, err := m.fetcher.FetchManifest(name, tag)
	if err != nil {
		return "", 0, err
	}
	layer, err := manifest.ModelLayer()
	if err != nil {
		return "", 0, err
	}
	url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", m.fetcher.registryURL(), name, layer.Digest)
	return url, layer.Size, nil
}

// splitOllamaRef parses "name:tag" into its components. Empty name
// or tag returns ok=false. The colon must be present and both sides
// must be non-empty — a bare family name isn't downloadable because
// we don't know which quant/size the user wants.
func splitOllamaRef(ref string) (name, tag string, ok bool) {
	idx := strings.Index(ref, ":")
	if idx <= 0 || idx == len(ref)-1 {
		return "", "", false
	}
	return ref[:idx], ref[idx+1:], true
}
