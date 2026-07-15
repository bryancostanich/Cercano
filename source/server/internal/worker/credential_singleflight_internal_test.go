package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/secrets"
)

// TestWorkerRunner_TokenSourceSingleFlightPerProfile proves that concurrent
// credential resolutions for one subscription profile perform a single token
// refresh. anthropicauth.Source is single-flight by construction, but that only
// holds if the workerRunner reuses one Source per profile — a fresh Source per
// request gives each caller its own mutex, and Anthropic's refresh tokens
// rotate, so racing refreshes invalidate each other.
//
// The refresh endpoint holds every request open until released, so if N
// independent Sources each refresh, N requests pile up on the server; a shared
// Source lets exactly one caller refresh while the rest block on its mutex and
// then read the freshly-persisted token.
func TestWorkerRunner_TokenSourceSingleFlightPerProfile(t *testing.T) {
	store := secrets.NewMemory()
	expired := anthropicauth.TokenSet{Access: "old", Refresh: "r", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := anthropicauth.Save(store, "sub-1", expired); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	var refreshes int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&refreshes, 1)
		<-release // hold the refresh open so concurrent callers overlap
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "refresh_token": "rotated", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	w := &workerRunner{
		secrets:  store,
		anthFlow: anthropicauth.Flow{TokenURL: srv.URL},
	}

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Mirror resolveCredential: acquire the per-profile source, then Token().
			_, _ = w.anthropicSource("sub-1").Token(context.Background())
		}()
	}
	close(start)
	time.Sleep(100 * time.Millisecond) // let goroutines converge on the in-flight refresh
	close(release)
	wg.Wait()

	if got := atomic.LoadInt64(&refreshes); got != 1 {
		t.Fatalf("refresh endpoint hit %d times; want 1 (single-flight defeated — a new Source per request)", got)
	}
}

// TestWorkerRunner_TokenSourceCachedPerProfile pins the caching contract both
// accessors rely on: same profile returns the same instance; different profiles
// get distinct instances.
func TestWorkerRunner_TokenSourceCachedPerProfile(t *testing.T) {
	w := &workerRunner{secrets: secrets.NewMemory()}

	if a, b := w.anthropicSource("p1"), w.anthropicSource("p1"); a != b {
		t.Error("anthropicSource returned a new instance for the same profile")
	}
	if a, b := w.anthropicSource("p1"), w.anthropicSource("p2"); a == b {
		t.Error("anthropicSource shared one instance across different profiles")
	}
	if a, b := w.chatgptSource("c1"), w.chatgptSource("c1"); a != b {
		t.Error("chatgptSource returned a new instance for the same profile")
	}
	if a, b := w.chatgptSource("c1"), w.chatgptSource("c2"); a == b {
		t.Error("chatgptSource shared one instance across different profiles")
	}
}
