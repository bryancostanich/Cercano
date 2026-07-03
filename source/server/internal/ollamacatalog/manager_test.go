package ollamacatalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// managerTestFixture spins up a fake library and returns a Manager
// wired to it plus a hit counter so tests can assert refresh behavior.
func managerTestFixture(t *testing.T) (*Manager, *int64, func()) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.URL.Path != "/library" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="/library/qwen2.5-coder">qwen2.5-coder</a>
<a href="/library/llama3.2">llama3.2</a>`)
	}))
	m := NewManager(&Fetcher{BaseURL: srv.URL}, filepath.Join(t.TempDir(), "cache.json"))
	return m, &hits, srv.Close
}

func TestManager_RefreshPopulatesCacheAndPersistsToDisk(t *testing.T) {
	m, _, cleanup := managerTestFixture(t)
	defer cleanup()

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	models := m.Models()
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if m.FetchedAt().IsZero() {
		t.Fatal("FetchedAt not set after Refresh")
	}
	// A fresh Manager on the same cache path should see the persisted state.
	m2 := NewManager(&Fetcher{BaseURL: "http://invalid.example"}, m.cachePath)
	if err := m2.LoadCache(); err != nil {
		t.Fatal(err)
	}
	if len(m2.Models()) != 2 {
		t.Fatalf("persisted cache lost models: %+v", m2.Models())
	}
}

func TestManager_ModelsReturnsDefensiveCopy(t *testing.T) {
	// Mutating the returned slice must NOT change what future Models()
	// calls return. Guards against a subtle aliasing bug where a caller
	// (e.g. a display renderer that sorts in place) silently corrupts
	// the shared cache.
	m, _, cleanup := managerTestFixture(t)
	defer cleanup()
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := m.Models()
	first[0].Name = "MUTATED"
	second := m.Models()
	if second[0].Name == "MUTATED" {
		t.Fatal("Models() aliased internal cache; external mutation leaked")
	}
}

func TestManager_ModelsBeforeAnyRefreshReturnsNil(t *testing.T) {
	// The first-run behavior: no cache on disk, no refresh yet. Models()
	// must not panic and must return an empty slice so the CLI can
	// render a "loading…" indicator.
	m, _, cleanup := managerTestFixture(t)
	defer cleanup()
	if got := m.Models(); got != nil {
		t.Errorf("expected nil before refresh, got %+v", got)
	}
	if fa := m.FetchedAt(); !fa.IsZero() {
		t.Errorf("expected zero FetchedAt before refresh, got %v", fa)
	}
}

func TestManager_LoadCacheMissingFileIsNotAnError(t *testing.T) {
	// Startup on a fresh install (no cache file yet). LoadCache must
	// succeed silently so the caller can go on to start the background
	// refresher, which will populate the cache.
	m := NewManager(&Fetcher{}, filepath.Join(t.TempDir(), "no-such-file.json"))
	if err := m.LoadCache(); err != nil {
		t.Fatalf("missing cache file must not be an error, got: %v", err)
	}
	if fa := m.FetchedAt(); !fa.IsZero() {
		t.Errorf("expected zero FetchedAt when cache missing, got %v", fa)
	}
}

func TestManager_RefresherLoopFetchesOnStartWhenStale(t *testing.T) {
	// Start()'s immediate check should fire a refresh when the cache is
	// missing / stale. Uses a very short TTL so the check is easy to
	// observe.
	m, hits, cleanup := managerTestFixture(t)
	defer cleanup()
	m.SetTTL(time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	defer func() { cancel(); m.Stop() }()

	// Give the initial refresh time to run. The refresher is
	// intentionally not synchronous; poll until we see the hit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(hits) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(hits) < 1 {
		t.Fatal("background refresher never fetched")
	}
	if len(m.Models()) == 0 {
		t.Fatal("background refresh completed but cache is empty")
	}
}

func TestManager_StartIsIdempotent(t *testing.T) {
	// Calling Start twice must not spawn two goroutines racing to
	// refresh — that would double our request rate and could double
	// -write the cache file.
	m, hits, cleanup := managerTestFixture(t)
	defer cleanup()
	m.SetTTL(time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	m.Start(ctx) // second call must be a no-op
	m.Start(ctx) // and a third, for good measure

	time.Sleep(200 * time.Millisecond)
	m.Stop()

	// One initial refresh from Start. The interval-based tick is longer
	// than our 200ms sleep so we shouldn't see additional hits from
	// the timer. What we're really asserting: no "3× hits from 3×
	// Start" pattern.
	if h := atomic.LoadInt64(hits); h > 2 {
		t.Fatalf("expected ≤2 hits after three Start() calls, got %d", h)
	}
}

func TestManager_RefreshErrorDoesNotWipePreviousCache(t *testing.T) {
	// A network blip during refresh must leave the previously-good
	// cache in place. Simulate by first refreshing against a live
	// fake server, then swapping the fetcher to an unreachable URL.
	m, _, cleanup := managerTestFixture(t)
	defer cleanup()
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := m.Models()

	// Point the fetcher at a URL that won't resolve.
	m.fetcher = &Fetcher{BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{Timeout: 100 * time.Millisecond}}
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected Refresh to fail with unreachable fetcher")
	}
	after := m.Models()
	if len(before) != len(after) {
		t.Fatalf("previous cache was wiped by failed refresh (%d → %d models)", len(before), len(after))
	}
}

func TestResolve_SucceedsWithModelLayerManifest(t *testing.T) {
	// httptest server that returns a manifest with a valid model layer
	// when asked for /v2/library/qwen2.5-coder/manifests/7b.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/qwen2.5-coder/manifests/7b" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.ollama.image.model","digest":"sha256:blob-model","size":4700000000}]}`)
	}))
	defer srv.Close()

	m := NewManager(&Fetcher{RegistryURL: srv.URL}, "")
	url, size, err := m.Resolve(context.Background(), "qwen2.5-coder:7b")
	if err != nil {
		t.Fatal(err)
	}
	want := srv.URL + "/v2/library/qwen2.5-coder/blobs/sha256:blob-model"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	if size != 4700000000 {
		t.Errorf("size = %d, want 4700000000", size)
	}
}

func TestResolve_ErrorsOnMalformedRef(t *testing.T) {
	// Guard: refs without a colon (or with an empty side) are unusable.
	// Resolve must reject them explicitly so the caller can distinguish
	// "you sent me a bad ref" from "the network is down".
	m := NewManager(&Fetcher{}, "")
	cases := []string{"", "qwen2.5-coder", ":7b", "qwen:", "only-family"}
	for _, ref := range cases {
		if _, _, err := m.Resolve(context.Background(), ref); err == nil {
			t.Errorf("expected error for ref %q, got nil", ref)
		}
	}
}

func TestResolve_ErrorsWhenManifestHasNoModelLayer(t *testing.T) {
	// Simulates a manifest that carries only template/system/license
	// layers (no model). Resolve must not fall through to a bogus blob
	// URL — that would download the template as if it were a GGUF.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.ollama.image.license","digest":"sha256:lic","size":100}]}`)
	}))
	defer srv.Close()
	m := NewManager(&Fetcher{RegistryURL: srv.URL}, "")
	if _, _, err := m.Resolve(context.Background(), "name:tag"); err == nil {
		t.Fatal("expected error when manifest has no model layer, got nil")
	}
}
