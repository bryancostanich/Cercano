package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/compactiongen"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/secrets"
)

// waitUntilWaiterParked blocks until at least one waitForUpdateIdle caller has
// reached pauseWhenIdle's blocked wait. A fresh gate has changed == nil; the
// first blocked waiter creates it under the admission mutex. Observing it
// from this goroutine also gives a happens-before edge over the waiter's
// already-completed pre-wait coverage check (program order within the waiter:
// coverage check -> pauseWhenIdle -> mutex), so tests may safely mutate the
// observed coverage fields afterwards without a data race.
func waitUntilWaiterParked(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.updateWork.mu.Lock()
		parked := s.updateWork.changed != nil
		s.updateWork.mu.Unlock()
		if parked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("waitForUpdateIdle caller never reached the blocked wait")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWaitForUpdateIdleSealsGateAndReleaseIsIdempotent(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	// A freshly constructed server has every work source tracked: the resume
	// hydration hook binds at construction, and no runtime manager or
	// compaction generator means nothing to track for those sources.
	if s.updateHydrationTrackingErr != nil {
		t.Fatalf("hydration binding refused at construction: %v", s.updateHydrationTrackingErr)
	}
	if s.updateCredentialTrackingErr != nil {
		t.Fatalf("credential binding refused at construction: %v", s.updateCredentialTrackingErr)
	}
	cov := s.updateCoverageSnapshot()
	if cov.runtimeDownloads != nil || cov.compaction != nil || cov.resumeHydration != nil || cov.credentialRefresh != nil {
		t.Fatalf("fresh server coverage not clean: %+v", cov)
	}

	release, err := s.waitForUpdateIdle(context.Background())
	if err != nil {
		t.Fatalf("waitForUpdateIdle: %v", err)
	}
	if release == nil {
		t.Fatal("waitForUpdateIdle returned nil release on success")
	}
	if _, err := s.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("gate not sealed after successful wait: %v", err)
	}
	release()
	release() // idempotent: a second call must not unseal anything twice or panic.
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("gate did not reopen after release: %v", err)
	}
	work()
}

func TestWaitForUpdateIdleRefusesUntrackedCoverageBeforeWait(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	s.updateRuntimeTrackingErr = errors.New("downloads unbound")
	s.updateCompactionTrackingErr = errors.New("compaction unbound")
	s.updateHydrationTrackingErr = errors.New("hydration unbound")
	s.updateCredentialTrackingErr = errors.New("credentials unbound")

	release, err := s.waitForUpdateIdle(context.Background())
	if release != nil {
		t.Fatal("pre-wait refusal must not hand out a release")
	}
	if !errors.Is(err, errUpdateCoverageIncomplete) {
		t.Fatalf("refusal error = %v, want update coverage incomplete", err)
	}
	for _, want := range []string{"runtime model download", "background compaction", "resume hydration", "credential refresh"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name source %q: %v", want, err)
		}
	}
	// Fail fast: the refusal must not have paused admission.
	work, werr := s.updateWork.enter()
	if werr != nil {
		t.Fatalf("pre-wait refusal left the gate sealed: %v", werr)
	}
	work()
}

func TestWaitForUpdateIdleRefusesCoverageFailureAfterSealWithoutLeak(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("enter: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() {
		_, err := s.waitForUpdateIdle(context.Background())
		waitErr <- err
	}()
	// Wait until the waiter is parked inside pauseWhenIdle so its pre-wait
	// coverage check has already passed; only then break coverage.
	waitUntilWaiterParked(t, s)
	s.setInjectedUpdateTrackingErr("compaction", errors.New("hook rebound mid-wait"))
	work() // drain: the waiter seals, then re-checks coverage.

	err = <-waitErr
	if !errors.Is(err, errUpdateCoverageIncomplete) {
		t.Fatalf("post-seal refusal error = %v, want update coverage incomplete", err)
	}
	if !strings.Contains(err.Error(), "background compaction") {
		t.Errorf("post-seal refusal does not name the failed source: %v", err)
	}
	// No leaked pause: the post-seal refusal must have released the seal.
	rel, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("post-seal refusal leaked the pause: %v", err)
	}
	rel()
}

func TestWaitForUpdateIdleCancellationWhileBusyNeverCancelsWork(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("enter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() {
		_, err := s.waitForUpdateIdle(ctx)
		waitErr <- err
	}()
	waitUntilWaiterParked(t, s)
	cancel()

	err = <-waitErr
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait error = %v, want context.Canceled", err)
	}
	// The canceled wait sealed nothing and cancelled nothing: the work's own
	// lifetime is still counted and still releasable on its own terms.
	work()
	release, err := s.waitForUpdateIdle(context.Background())
	if err != nil {
		t.Fatalf("wait after work drained: %v", err)
	}
	if _, err := s.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("work was cancelled or the gate failed to seal: %v", err)
	}
	release()
}

func TestWaitForUpdateIdleConcurrentWaitersCannotReleaseEachOther(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("enter: %v", err)
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	releaseB := make(chan func(), 1)
	go func() {
		_, err := s.waitForUpdateIdle(ctxA)
		errA <- err
	}()
	go func() {
		release, err := s.waitForUpdateIdle(context.Background())
		releaseB <- release
		errB <- err
	}()
	waitUntilWaiterParked(t, s)
	cancelA() // A's failure path must not touch B's eventual seal.
	work()    // drain: exactly B seals.

	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("surviving waiter error = %v, want nil", err)
	}
	relB := <-releaseB
	if relB == nil {
		t.Fatal("surviving waiter got nil release")
	}

	// While B holds the seal, another waiter is refused — and that refusal
	// must not release B's gate.
	if release, err := s.waitForUpdateIdle(context.Background()); release != nil || !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("waiter while sealed: release=%v err=%v, want refusal", release != nil, err)
	}
	if _, err := s.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("a failed concurrent waiter released another waiter's seal: %v", err)
	}

	relB()
	w, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("gate did not reopen after its owner released: %v", err)
	}
	w()
}

func TestWaitForUpdateIdleKeepsGateAfterCallerCtxCancel(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := s.waitForUpdateIdle(ctx)
	if err != nil {
		t.Fatalf("waitForUpdateIdle: %v", err)
	}
	// The ctx only governs the wait. Once the method has returned, the owner
	// (documented to defer the release) keeps the seal even if the request
	// context dies afterwards.
	cancel()
	if _, err := s.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("canceling the wait ctx lifted the seal: %v", err)
	}
	release()
	w, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("gate did not reopen after release: %v", err)
	}
	w()
}

func TestPrepareUpdateIdleNotifiesReadyAndCannotLeakSeal(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)

	// Happy path: ready owns the seal for its call; returning without
	// releasing must not leak it.
	s.prepareUpdateIdle(context.Background(), func(release func(), err error) {
		if err != nil {
			t.Fatalf("ready err = %v, want nil", err)
		}
		if release == nil {
			t.Fatal("ready got nil release on success")
		}
		if _, err := s.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
			t.Fatalf("gate not sealed inside ready: %v", err)
		}
		// Deliberately do NOT release: the deferred release must clean up.
	})
	w, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("prepareUpdateIdle leaked the seal when ready did not release: %v", err)
	}
	w()

	// Panic path: a ready callback that explodes must not leak the seal.
	func() {
		defer func() { _ = recover() }()
		s.prepareUpdateIdle(context.Background(), func(release func(), err error) {
			panic("ready exploded")
		})
	}()
	w, err = s.updateWork.enter()
	if err != nil {
		t.Fatalf("prepareUpdateIdle leaked the seal when ready panicked: %v", err)
	}
	w()

	// Refusal path: broken coverage is reported with a nil release.
	s.updateHydrationTrackingErr = errors.New("hydration unbound")
	var gotErr error
	s.prepareUpdateIdle(context.Background(), func(release func(), err error) {
		gotErr = err
		if release != nil {
			t.Error("refusal handed ready a release")
		}
	})
	if !errors.Is(gotErr, errUpdateCoverageIncomplete) {
		t.Fatalf("ready err = %v, want update coverage incomplete", gotErr)
	}
}

func TestWaitForUpdateIdleWaitsForRealRuntimeDownloadWork(t *testing.T) {
	payload := bytes.Repeat([]byte("D"), 2048)
	unblock := make(chan struct{})
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-unblock
		_, _ = w.Write(payload)
	}))
	defer src.Close()

	model := localruntime.ModelRecord{
		ID:                 "llama_server:idle-wait-download",
		DisplayName:        "Idle Wait Model",
		Runtime:            "llama_server",
		Path:               filepath.Join(t.TempDir(), "model.gguf"),
		DownloadURL:        src.URL + "/model.gguf",
		DownloadTotalBytes: int64(len(payload)),
		DownloadState:      localruntime.DownloadNotStarted,
	}
	mgr := localruntime.NewManager()
	mgr.EnrollDownload(model)
	s := NewServer(nil, nil, nil, nil, nil)
	s.SetRuntimeManager(mgr) // binds real download lifetimes to the gate
	if s.updateRuntimeTrackingErr != nil {
		t.Fatalf("runtime download tracking refused: %v", s.updateRuntimeTrackingErr)
	}

	if _, err := mgr.DownloadModel(context.Background(), localruntime.DownloadRequest{Runtime: "llama_server", ModelID: model.ID}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}

	// While the real download worker holds its admitted lifetime, the wait
	// blocks; aborting the wait must NOT cancel the download.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	release, err := s.waitForUpdateIdle(ctx)
	cancel()
	if release != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not block on the real download: release=%v err=%v", release != nil, err)
	}

	// The download was not cancelled: let it finish on its own terms.
	close(unblock)
	release, err = s.waitForUpdateIdle(context.Background()) // seals only once the job's lifetime retires
	if err != nil {
		t.Fatalf("wait after download drained: %v", err)
	}
	release()

	models, err := mgr.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	for _, m := range models {
		if m.ID != model.ID {
			continue
		}
		if m.DownloadState != localruntime.Downloaded {
			t.Fatalf("download state = %s (error %q), want downloaded — the aborted wait must not cancel work", m.DownloadState, m.DownloadError)
		}
		if m.DownloadedBytes != int64(len(payload)) {
			t.Errorf("downloaded bytes = %d, want %d", m.DownloadedBytes, len(payload))
		}
		return
	}
	t.Fatalf("model %s missing from inventory after download", model.ID)
}

func TestWaitForUpdateIdleWaitsForRealCompactionWork(t *testing.T) {
	g := compactiongen.New(nil, nil, compactor.Config{}, nil, time.Hour)
	s := NewServer(nil, nil, nil, nil, nil)
	s.SetCompactionGenerator(g) // binds real compaction lifetimes via the server seam
	if s.updateCompactionTrackingErr != nil {
		t.Fatalf("compaction tracking refused: %v", s.updateCompactionTrackingErr)
	}
	g.SetEnabled(true)
	g.SetToolElisionOnly(true)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	g.SetElideOnlyFn(func(context.Context, string) (int, int, int, bool, error) {
		once.Do(func() { close(entered) })
		<-finish
		return 0, 0, 0, false, nil
	})

	if err := g.CompactAsync("conv-idle-wait"); err != nil {
		t.Fatalf("CompactAsync: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("compaction pass never started")
	}

	// While the real compaction pass holds its admitted lifetime, the wait
	// blocks; aborting the wait must NOT cancel the pass.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := s.waitForUpdateIdle(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not block on the real compaction pass: %v", err)
	}

	close(finish)                                             // the pass finishes on its own terms — nothing was cancelled
	release, err := s.waitForUpdateIdle(context.Background()) // seals only once the pass's lifetime retires
	if err != nil {
		t.Fatalf("wait after compaction drained: %v", err)
	}
	release()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), time.Second)
	defer cancelClose()
	if err := g.Close(closeCtx); err != nil {
		t.Fatalf("compaction generator close: %v", err)
	}
}

func TestWaitForUpdateIdleWaitsForRealCredentialRefresh(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	if s.updateCredentialTrackingErr != nil {
		t.Fatalf("credential tracking refused: %v", s.updateCredentialTrackingErr)
	}
	// Late store install, mirroring the front door: the credentials service
	// keeps its identity and swaps the backend.
	store := secrets.NewMemory()
	s.cfgSvc.SetSecrets(store)
	creds := s.cfgSvc.Credentials()
	if err := anthropicauth.Save(creds, "work", anthropicauth.TokenSet{Access: "expired", Refresh: "single-use", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	unblockNow := func() { unblockOnce.Do(func() { close(unblock) }) }
	defer unblockNow()
	entered := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-unblock // hold the real refresh open: the flight keeps its lease
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"rotated","expires_in":3600}`))
	}))
	defer srv.Close()

	// A real token refresh over the in-process test endpoint: one flight,
	// holding one admitted lifetime until the refresh goroutine returns.
	view := creds.Anthropic("work", anthropicauth.Flow{TokenURL: srv.URL})
	tokErr := make(chan error, 1)
	go func() {
		access, err := view.Token(context.Background())
		if err == nil && access != "fresh" {
			err = errors.New("wrong access token")
		}
		tokErr <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("credential refresh never reached the token endpoint")
	}

	// While the real refresh flight holds its admitted lifetime, the wait
	// blocks; aborting the wait must NOT cancel the refresh.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	release, err := s.waitForUpdateIdle(ctx)
	cancel()
	if release != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not block on the real credential refresh: release=%v err=%v", release != nil, err)
	}

	// The refresh finishes on its own terms — nothing was cancelled — and
	// commits the rotated token after the aborted wait returned.
	unblockNow()
	if err := <-tokErr; err != nil {
		t.Fatalf("refresh did not finish on its own terms: %v", err)
	}
	raw, err := store.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := anthropicauth.DecodeTokenSet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Access != "fresh" || ts.Refresh != "rotated" {
		t.Fatalf("rotated token not persisted after the aborted wait: %+v", ts)
	}

	release, err = s.waitForUpdateIdle(context.Background()) // seals only once the flight's lifetime retires
	if err != nil {
		t.Fatalf("wait after refresh drained: %v", err)
	}
	release()
}
