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
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
)

// Multiple worker source views share the host credential owner. This preserves
// single-flight refresh without relying on cached adapter pointer identity.
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
		cfg:      cfgsvc.New("", pkgcfg.Config{}, store),
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
			// Mirror resolveCredential: obtain a fresh view of the shared owner.
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

// Provider views are intentionally replaceable; service ownership, not pointer
// identity of source adapters, is the synchronization contract.
func TestWorkerRunner_TokenSourcesUseConfigOwner(t *testing.T) {
	cfg := cfgsvc.New("", pkgcfg.Config{}, secrets.NewMemory())
	w := &workerRunner{cfg: cfg}
	if err := anthropicauth.Save(cfg.Secrets(), "p1", anthropicauth.TokenSet{Access: "fresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, view := range []interface {
		Token(context.Context) (string, error)
	}{w.anthropicSource("p1"), cfg.Credentials().Anthropic("p1", anthropicauth.Flow{})} {
		if token, err := view.Token(context.Background()); err != nil || token != "fresh" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
}
