package localruntime

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDownloadModel_ConcurrentSameModelDownloadsOnce fires many DownloadModel
// calls for one model at once. Only one download must actually run: DownloadModel
// checks for an in-progress download, then resolves the model (possibly running
// Inventory) without holding the lock, then claims the slot — so without an
// atomic claim, several callers pass the stale check and each spawn a
// runDownload against the same .part file (corrupting it and orphaning cancel).
//
// The endpoint counts requests and holds each open until released, so every
// spawned download parks on it: with the bug N requests pile up; with the fix
// exactly one does.
func TestDownloadModel_ConcurrentSameModelDownloadsOnce(t *testing.T) {
	body := bytes.Repeat([]byte("A"), 4096)

	var reqs int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqs, 1)
		<-release // hold the download open so concurrent spawns overlap
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	model := ModelRecord{
		ID:                 "llama_server:test-race",
		DisplayName:        "Test Race",
		Runtime:            "llama_server",
		Path:               filepath.Join(dir, "model.gguf"),
		DownloadURL:        srv.URL + "/model.gguf",
		DownloadTotalBytes: int64(len(body)),
		DownloadState:      "not_downloaded",
	}

	m := NewManager()
	m.EnrollDownload(model)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = m.DownloadModel(context.Background(), DownloadRequest{Runtime: model.Runtime, ModelID: model.ID})
		}()
	}
	close(start)
	wg.Wait() // DownloadModel returns promptly; the spawned runDownloads are now in flight

	time.Sleep(200 * time.Millisecond) // let every spawned download reach the endpoint
	got := atomic.LoadInt64(&reqs)
	close(release) // let the download(s) complete so the manager reaches a terminal state

	waitDownloadDone(t, m, model.ID)

	if got != 1 {
		t.Fatalf("download endpoint hit %d times; want 1 (DownloadModel spawned duplicate concurrent downloads)", got)
	}
}
