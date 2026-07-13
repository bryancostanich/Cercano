package anthropicauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStore is a goroutine-safe in-memory Store for tests.
type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) Get(profile string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[profile], nil
}

func (f *fakeStore) Set(profile, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[profile] = value
	return nil
}

func seedToken(t *testing.T, store Store, profile string, ts TokenSet) {
	t.Helper()
	if err := Save(store, profile, ts); err != nil {
		t.Fatalf("seed token: %v", err)
	}
}

func TestSource_ReturnsCachedWhenFresh(t *testing.T) {
	store := newFakeStore()
	seedToken(t, store, "p", TokenSet{Access: "cached", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour)})

	// A token endpoint that fails the test if the source ever refreshes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("refresh should not be called for a fresh token")
	}))
	defer srv.Close()

	s := NewSource(store, "p", Flow{TokenURL: srv.URL})
	got, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "cached" {
		t.Errorf("Token = %q, want cached", got)
	}
}

func TestSource_RefreshesWhenExpired(t *testing.T) {
	store := newFakeStore()
	seedToken(t, store, "p", TokenSet{Access: "old", Refresh: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour)})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "refresh_token": "rotated", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	s := NewSource(store, "p", Flow{TokenURL: srv.URL})
	got, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "fresh" {
		t.Errorf("Token = %q, want fresh", got)
	}
	// The rotated token must be persisted so the next request uses it.
	raw, _ := store.Get("p")
	ts, _ := DecodeTokenSet(raw)
	if ts.Access != "fresh" || ts.Refresh != "rotated" {
		t.Errorf("persisted token = %+v, want fresh/rotated", ts)
	}
}

func TestSource_ErrorsWhenExpiredWithoutRefresh(t *testing.T) {
	store := newFakeStore()
	seedToken(t, store, "p", TokenSet{Access: "old", ExpiresAt: time.Now().Add(-time.Hour)})

	s := NewSource(store, "p", Flow{})
	if _, err := s.Token(context.Background()); err == nil {
		t.Fatal("expected an error when the token is expired and no refresh token is stored")
	}
}

// TestSource_ConcurrentSingleFlight proves the design's key concurrency
// claim: N requests hitting an expired token trigger exactly one refresh,
// because the mutex serializes load/refresh/persist and later callers re-load
// the persisted fresh token. Anthropic's rotating refresh tokens make more
// than one refresh a correctness bug, not just waste.
func TestSource_ConcurrentSingleFlight(t *testing.T) {
	store := newFakeStore()
	seedToken(t, store, "p", TokenSet{Access: "old", Refresh: "r0", ExpiresAt: time.Now().Add(-time.Hour)})

	var refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshes, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "refresh_token": "r1", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	s := NewSource(store, "p", Flow{TokenURL: srv.URL})

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Token(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if got != "fresh" {
				t.Errorf("Token = %q, want fresh", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Token: %v", err)
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Errorf("refresh count = %d, want exactly 1 (single-flight)", got)
	}
}
