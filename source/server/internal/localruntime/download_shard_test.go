package localruntime

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDownloadModel_MultiShard exercises the multi-shard path end to end: a
// model with two DownloadURLs must fetch both files into the model's
// directory, report cumulative progress, and only reach "downloaded" once both
// shards are present — then DeleteModel must remove both, not just the first.
func TestDownloadModel_MultiShard(t *testing.T) {
	shard0 := bytes.Repeat([]byte("A"), 4096)
	shard1 := bytes.Repeat([]byte("B"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/GLM-Q4_K_M-00001-of-00002.gguf":
			_, _ = w.Write(shard0)
		case "/GLM-Q4_K_M-00002-of-00002.gguf":
			_, _ = w.Write(shard1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	url0 := srv.URL + "/GLM-Q4_K_M-00001-of-00002.gguf"
	url1 := srv.URL + "/GLM-Q4_K_M-00002-of-00002.gguf"
	total := int64(len(shard0) + len(shard1))
	model := ModelRecord{
		ID:                 "llama_server:test-multishard",
		DisplayName:        "Test Multishard",
		Runtime:            "llama_server",
		Path:               filepath.Join(dir, "GLM-Q4_K_M-00001-of-00002.gguf"),
		DownloadURLs:       []string{url0, url1},
		DownloadTotalBytes: total,
		DownloadState:      "not_downloaded",
	}

	m := NewManager()
	m.EnrollDownload(model)
	if _, err := m.DownloadModel(context.Background(), DownloadRequest{Runtime: "llama_server", ModelID: model.ID}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}

	final := waitDownloadDone(t, m, model.ID)
	if final.DownloadState != "downloaded" {
		t.Fatalf("state = %q (error %q), want downloaded", final.DownloadState, final.DownloadError)
	}
	if final.DownloadedBytes != total {
		t.Errorf("DownloadedBytes = %d, want %d (cumulative across shards)", final.DownloadedBytes, total)
	}

	// Both shards on disk with the right bytes — the second is proof the loop
	// didn't stop after the first.
	if got, err := os.ReadFile(filepath.Join(dir, "GLM-Q4_K_M-00001-of-00002.gguf")); err != nil || !bytes.Equal(got, shard0) {
		t.Fatalf("shard0: err %v, len %d want %d", err, len(got), len(shard0))
	}
	if got, err := os.ReadFile(filepath.Join(dir, "GLM-Q4_K_M-00002-of-00002.gguf")); err != nil || !bytes.Equal(got, shard1) {
		t.Fatalf("shard1: err %v, len %d want %d", err, len(got), len(shard1))
	}

	// DeleteModel must clear every shard.
	if err := m.DeleteModel(context.Background(), DeleteModelRequest{Runtime: "llama_server", ModelID: model.ID}); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	for _, name := range []string{"GLM-Q4_K_M-00001-of-00002.gguf", "GLM-Q4_K_M-00002-of-00002.gguf"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after delete (err=%v)", name, err)
		}
	}
}

// TestDownloadModel_SingleFileStillWorks guards the back-compatible path: a
// model with only DownloadURL (no DownloadURLs) downloads to Path unchanged.
func TestDownloadModel_SingleFileStillWorks(t *testing.T) {
	body := bytes.Repeat([]byte("Z"), 3000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	model := ModelRecord{
		ID:                 "llama_server:test-single",
		DisplayName:        "Test Single",
		Runtime:            "llama_server",
		Path:               filepath.Join(dir, "model.gguf"),
		DownloadURL:        srv.URL + "/model.gguf",
		DownloadTotalBytes: int64(len(body)),
		DownloadState:      "not_downloaded",
	}

	m := NewManager()
	m.EnrollDownload(model)
	if _, err := m.DownloadModel(context.Background(), DownloadRequest{Runtime: "llama_server", ModelID: model.ID}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}

	final := waitDownloadDone(t, m, model.ID)
	if final.DownloadState != "downloaded" {
		t.Fatalf("state = %q (error %q), want downloaded", final.DownloadState, final.DownloadError)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "model.gguf")); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("model.gguf: err %v, len %d want %d", err, len(got), len(body))
	}
}

// waitDownloadDone polls the manager's download record until it reaches a
// terminal state or the timeout fires.
func waitDownloadDone(t *testing.T, m *InMemoryManager, id string) ModelRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := m.download(id)
		if ok && (rec.DownloadState == "downloaded" || rec.DownloadState == "failed" || rec.DownloadState == "cancelled") {
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("download %q did not finish within timeout", id)
	return ModelRecord{}
}
