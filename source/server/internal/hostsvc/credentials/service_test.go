package credentials

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func awaitWaiters(t *testing.T, s *Service, name string, n int) {
	t.Helper()
	ctx := testContext(t)
	p := s.profile(name)
	for {
		p.mu.Lock()
		ready := p.flight != nil && p.flight.waiters == n
		p.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("did not observe %d waiters", n)
		default:
			runtime.Gosched()
		}
	}
}
func seed(t *testing.T, s secrets.Store, kind, name, access string, expired bool) {
	t.Helper()
	expiry := time.Now().Add(time.Hour)
	if expired {
		expiry = time.Now().Add(-time.Hour)
	}
	var err error
	if kind == "anthropic" {
		err = anthropicauth.Save(s, name, anthropicauth.TokenSet{Access: access, Refresh: "single-use", ExpiresAt: expiry})
	} else {
		err = chatgptauth.Save(s, name, chatgptauth.TokenSet{Access: access, Refresh: "single-use", AccountID: "account", ExpiresAt: expiry})
	}
	if err != nil {
		t.Fatal(err)
	}
}
func tokenView(s *Service, kind, name, url string) func(context.Context) (string, error) {
	if kind == "anthropic" {
		return s.Anthropic(name, anthropicauth.Flow{TokenURL: url}).Token
	}
	view := s.ChatGPT(name, chatgptauth.Flow{Issuer: url})
	return func(ctx context.Context) (string, error) { access, _, err := view.Token(ctx); return access, err }
}
func TestRebuiltSourcesShareOneRefresh(t *testing.T) {
	for _, kind := range []string{"anthropic", "chatgpt"} {
		t.Run(kind, func(t *testing.T) {
			s := New(secrets.NewMemory())
			seed(t, s, kind, "work", "expired", true)
			var calls atomic.Int32
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token":"fresh","refresh_token":"rotated","expires_in":3600}`))
			}))
			defer srv.Close()
			defer unblock()
			ctx := testContext(t)
			done := make(chan error, 12)
			for i := 0; i < 12; i++ {
				view := tokenView(s, kind, "work", srv.URL)
				go func() {
					access, err := view(ctx)
					if err == nil && access != "fresh" {
						err = errors.New("wrong access token")
					}
					done <- err
				}()
			}
			awaitWaiters(t, s, "work", 12)
			unblock()
			for i := 0; i < 12; i++ {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("refreshes=%d", calls.Load())
			}
			if kind == "chatgpt" {
				_, account, err := s.ChatGPT("work", chatgptauth.Flow{Issuer: srv.URL}).Token(ctx)
				if err != nil || account != "account" {
					t.Fatalf("lost account: %q %v", account, err)
				}
			}
		})
	}
}

func TestLoginReplacementSupersedesRefreshWithoutWaiting(t *testing.T) {
	s := New(secrets.NewMemory())
	seed(t, s, "anthropic", "work", "expired", true)
	ctx := testContext(t)
	started := make(chan struct{})
	release := make(chan struct{})
	// Deliberately ignore transport cancellation to prove generation checks,
	// not merely HTTP cancellation, prevent the old result from committing.
	resolveOld := func(_ context.Context, tr *transaction) (token, error) {
		raw, _ := tr.Get("work")
		current, _ := anthropicauth.DecodeTokenSet(raw)
		if current.Access == "new-login" {
			return token{access: current.Access}, nil
		}
		close(started)
		<-release
		stale, _ := (anthropicauth.TokenSet{Access: "stale", ExpiresAt: time.Now().Add(time.Hour)}).Encode()
		tr.Set("work", stale)
		return token{access: "stale"}, nil
	}

	result := make(chan token, 1)
	go func() { value, _ := s.acquire(ctx, "work", "anthropic", resolveOld); result <- value }()
	<-started
	p := s.profile("work")
	p.mu.Lock()
	old := p.flight
	p.mu.Unlock()
	fresh := anthropicauth.TokenSet{Access: "new-login", ExpiresAt: time.Now().Add(time.Hour)}
	if err := anthropicauth.Save(s, "work", fresh); err != nil {
		t.Fatal(err)
	}
	// Use a real provider resolver for current waiters, rather than re-running
	// the test's intentionally non-cooperative old resolver on a new generation.
	value, err := s.Anthropic("work", anthropicauth.Flow{}).Token(ctx)
	if err != nil || value != "new-login" {
		t.Fatalf("replacement blocked or lost: %q %v", value, err)
	}
	close(release)
	// Check the superseded flight cannot publish stale data or regain ownership.
	<-old.done
	<-old.exited
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("old waiter did not exit")
	}
	raw, _ := s.Get("work")
	current, _ := anthropicauth.DecodeTokenSet(raw)
	if current.Access != "new-login" {
		t.Fatalf("stale write: %q", current.Access)
	}
}

func TestWaiterCancellationDoesNotCancelOtherWaiters(t *testing.T) {
	s := New(secrets.NewMemory())
	seed(t, s, "anthropic", "work", "expired", true)
	ctx := testContext(t)
	first, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	resolve := func(ctx context.Context, tr *transaction) (token, error) {
		close(started)
		select {
		case <-release:
			return token{access: "fresh"}, nil
		case <-ctx.Done():
			return token{}, ctx.Err()
		}
	}
	a := make(chan error, 1)
	b := make(chan error, 1)
	go func() { _, err := s.acquire(first, "work", "anthropic", resolve); a <- err }()
	<-started
	go func() { _, err := s.acquire(ctx, "work", "anthropic", resolve); b <- err }()
	awaitWaiters(t, s, "work", 2)
	cancel()
	if err := <-a; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unblock()
	if err := <-b; err != nil {
		t.Fatalf("other waiter canceled: %v", err)
	}
}

func TestLastWaiterCancelsRefresh(t *testing.T) {
	s := New(secrets.NewMemory())
	ctx, cancel := context.WithCancel(testContext(t))
	started := make(chan struct{})
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.acquire(ctx, "work", "anthropic", func(ctx context.Context, _ *transaction) (token, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return token{}, ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-testContext(t).Done():
		t.Fatal("abandoned refresh leaked")
	}
}

func TestUnrelatedProfileDoesNotWaitForNetwork(t *testing.T) {
	s := New(secrets.NewMemory())
	seed(t, s, "anthropic", "other", "ready", false)
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	started := make(chan struct{})
	go s.acquire(ctx, "blocked", "anthropic", func(ctx context.Context, _ *transaction) (token, error) {
		close(started)
		<-ctx.Done()
		return token{}, ctx.Err()
	})
	<-started
	if access, err := s.Anthropic("other", anthropicauth.Flow{}).Token(ctx); err != nil || access != "ready" {
		t.Fatalf("other profile blocked: %s %v", access, err)
	}
}

func TestExistingViewsFollowStoreReplacement(t *testing.T) {
	s := New(secrets.NewMemory())
	seed(t, s, "anthropic", "work", "before", false)
	view := s.Anthropic("work", anthropicauth.Flow{})
	next := secrets.NewMemory()
	seed(t, next, "anthropic", "work", "after", false)
	s.ReplaceStore(next)
	if value, err := view.Token(testContext(t)); err != nil || value != "after" {
		t.Fatalf("old backend retained: %q %v", value, err)
	}
	s.ReplaceStore(nil)
	if _, err := view.Token(testContext(t)); llm.ClassOf(err) != llm.ErrCredential {
		t.Fatalf("unavailable store: %v", err)
	}
}

type failingWrites struct{ secrets.Store }

func (failingWrites) Set(string, string) error { return errors.New("sensitive store failure") }
func TestFailedCommitDoesNotReturnUnpersistedToken(t *testing.T) {
	raw := secrets.NewMemory()
	raw.Set("work", "old")
	s := New(failingWrites{raw})
	value, err := s.acquire(testContext(t), "work", "anthropic", func(_ context.Context, tr *transaction) (token, error) {
		tr.Set("work", "new")
		return token{access: "unpersisted"}, nil
	})
	if value.access != "" || llm.ClassOf(err) != llm.ErrCredential {
		t.Fatalf("value=%q err=%v", value.access, err)
	}
	if stored, _ := raw.Get("work"); stored != "old" {
		t.Fatal("failed write mutated store")
	}
}

func TestNewWaiterDoesNotInheritAbandonedFlightCancellation(t *testing.T) {
	s := New(secrets.NewMemory())
	ctx := testContext(t)
	first, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	abandoned := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() {
		_, err := s.acquire(first, "work", "anthropic", func(ctx context.Context, _ *transaction) (token, error) {
			close(started)
			<-ctx.Done()
			close(abandoned)
			<-release
			return token{}, ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	<-done
	<-abandoned
	second := make(chan error, 1)
	go func() {
		value, err := s.acquire(ctx, "work", "anthropic", func(context.Context, *transaction) (token, error) { return token{access: "fresh"}, nil })
		if err == nil && value.access != "fresh" {
			err = errors.New("wrong token")
		}
		second <- err
	}()
	awaitWaiters(t, s, "work", 1)
	unblock()
	if err := <-second; err != nil {
		t.Fatalf("new live caller inherited another caller's cancellation: %v", err)
	}
}
