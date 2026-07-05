package localruntime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rangeAwareServer serves a fixed body honoring Range requests and
// records the Range header of the last request.
type rangeAwareServer struct {
	srv       *httptest.Server
	body      []byte
	lastRange string
}

func newRangeAwareServer(t *testing.T, body []byte) *rangeAwareServer {
	t.Helper()
	r := &rangeAwareServer{body: body}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.lastRange = req.Header.Get("Range")
		if r.lastRange == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(r.body)))
			_, _ = w.Write(r.body)
			return
		}
		var start int64
		fmt.Sscanf(r.lastRange, "bytes=%d-", &start)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(r.body)-1, len(r.body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(r.body[start:])
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func newResumeTestManager(url, target string, total int64) *InMemoryManager {
	manager := NewManager()
	manager.RegisterProvider(&fakeProvider{
		name: "llama_server",
		models: []ModelRecord{{
			ID:                 "catalog:resume-model",
			DisplayName:        "Resume Model",
			Runtime:            "llama_server",
			Source:             "catalog",
			Path:               target,
			Format:             "gguf",
			DownloadState:      "not_downloaded",
			DownloadURL:        url,
			DownloadTotalBytes: total,
			SizeBytes:          total,
			SupportsChat:       true,
		}},
	})
	return manager
}

func waitForState(t *testing.T, m *InMemoryManager, id, want string) ModelRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := m.Status(context.Background())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if got, ok := findModelRecord(status.Models, id); ok && got.DownloadState == want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("model never reached state %q", want)
	return ModelRecord{}
}

func TestDownloadResumesFromPartial(t *testing.T) {
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	server := newRangeAwareServer(t, body)
	target := filepath.Join(t.TempDir(), "model.gguf")

	// A previous attempt left the first 10 bytes.
	if err := os.WriteFile(target+".part", body[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	manager := newResumeTestManager(server.srv.URL, target, int64(len(body)))
	if _, err := manager.DownloadModel(context.Background(), DownloadRequest{
		Runtime: "llama_server", ModelID: "catalog:resume-model",
	}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	waitForState(t, manager, "catalog:resume-model", "downloaded")

	if server.lastRange != "bytes=10-" {
		t.Errorf("Range header = %q, want bytes=10-", server.lastRange)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(body) {
		t.Errorf("assembled file = %q, want full body", string(data))
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Error("partial should be renamed away after completion")
	}
}

func TestDownloadFailureKeepsPartial(t *testing.T) {
	// Server advertises more than it sends, then closes — a mid-stream
	// failure like a connection reset.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("only-a-fragment"))
		// Returning without writing the rest closes the connection short.
	}))
	t.Cleanup(srv.Close)
	target := filepath.Join(t.TempDir(), "model.gguf")

	manager := newResumeTestManager(srv.URL, target, 1000)
	if _, err := manager.DownloadModel(context.Background(), DownloadRequest{
		Runtime: "llama_server", ModelID: "catalog:resume-model",
	}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	failed := waitForState(t, manager, "catalog:resume-model", "failed")
	if failed.DownloadError == "" {
		t.Error("expected DownloadError to be set")
	}
	data, err := os.ReadFile(target + ".part")
	if err != nil {
		t.Fatalf("partial should survive failure: %v", err)
	}
	if len(data) == 0 {
		t.Error("partial is empty")
	}
}

func TestContentRangeTotal(t *testing.T) {
	cases := map[string]int64{
		"bytes 100-999/1000": 1000,
		"bytes 0-9/36":       36,
		"bytes 0-9/*":        0,
		"":                   0,
		"garbage":            0,
	}
	for in, want := range cases {
		if got := contentRangeTotal(in); got != want {
			t.Errorf("contentRangeTotal(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSweepStalePartials(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "old.gguf.part")
	fresh := filepath.Join(dir, "new.gguf.part")
	full := filepath.Join(dir, "done.gguf")
	for _, p := range []string{stale, fresh, full} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepStalePartials(dir, DefaultPartialMaxAge)
	if err != nil {
		t.Fatalf("SweepStalePartials: %v", err)
	}
	if len(removed) != 1 || !strings.HasSuffix(removed[0], "old.gguf.part") {
		t.Errorf("removed = %v, want just the stale partial", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh partial should survive the sweep")
	}
	if _, err := os.Stat(full); err != nil {
		t.Error("completed files must never be swept")
	}
}

func TestSweepStalePartials_MissingDirIsNotAnError(t *testing.T) {
	removed, err := SweepStalePartials(filepath.Join(t.TempDir(), "nope"), time.Hour)
	if err != nil || removed != nil {
		t.Errorf("missing dir: removed=%v err=%v", removed, err)
	}
}
