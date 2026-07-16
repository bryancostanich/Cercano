package localruntime

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// ensureModel builds a downloadable ModelRecord served by the given URL, in the
// DownloadNotStarted state, under a temp dir.
func ensureModel(t *testing.T, runtime, id, url string, size int) ModelRecord {
	t.Helper()
	return ModelRecord{
		ID:                 id,
		DisplayName:        id,
		Runtime:            runtime,
		Path:               filepath.Join(t.TempDir(), "model.gguf"),
		DownloadURL:        url,
		DownloadTotalBytes: int64(size),
		DownloadState:      DownloadNotStarted,
	}
}

// TestEnsureModelsPresent_EnqueuesMissing proves the core Phase-2 behavior: a
// wanted model that isn't downloaded gets fetched, engine-agnostically, via the
// manager — no per-runtime code path.
func TestEnsureModelsPresent_EnqueuesMissing(t *testing.T) {
	body := bytes.Repeat([]byte("A"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	model := ensureModel(t, "mistralrs", "mistralrs:catalog:qwen3-1.7b", srv.URL+"/model.gguf", len(body))
	// Give it a friendly display name so the ref below is genuinely a non-ID
	// value that MatchesModel must resolve (as a config default would be).
	model.DisplayName = "Qwen3 1.7B"
	m := NewManager()
	m.RegisterProvider(&fakeProvider{name: "mistralrs", models: []ModelRecord{model}})

	// Ask by the display name, not the canonical ID — ensure must resolve it
	// the same way Start does.
	if err := m.EnsureModelsPresent(context.Background(), "mistralrs", []string{"Qwen3 1.7B"}); err != nil {
		t.Fatalf("EnsureModelsPresent: %v", err)
	}
	waitDownloadDone(t, m, model.ID)

	inv, _ := m.Inventory(context.Background())
	rec, ok := resolveInInventory(inv, "mistralrs", model.ID)
	if !ok {
		t.Fatalf("model vanished from inventory")
	}
	if rec.DownloadState != Downloaded {
		t.Errorf("DownloadState = %v, want Downloaded after ensure", rec.DownloadState)
	}
}

// TestEnsureModelsPresent_SkipsDownloaded proves an already-present model is a
// no-op — no download slot is claimed. We assert by pointing the URL at a
// server that would fail the test if hit.
func TestEnsureModelsPresent_SkipsDownloaded(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	model := ensureModel(t, "mistralrs", "mistralrs:catalog:qwen3-1.7b", srv.URL+"/model.gguf", 10)
	model.DownloadState = Downloaded
	m := NewManager()
	m.RegisterProvider(&fakeProvider{name: "mistralrs", models: []ModelRecord{model}})

	if err := m.EnsureModelsPresent(context.Background(), "mistralrs", []string{model.ID}); err != nil {
		t.Fatalf("EnsureModelsPresent: %v", err)
	}
	if hit {
		t.Errorf("ensure fetched an already-downloaded model")
	}
}

// TestEnsureModelsPresent_UnresolvedRefErrorsButContinues proves a partial tier
// set is still ensured: an unknown ref is reported as an error, but a valid ref
// alongside it is still fetched.
func TestEnsureModelsPresent_UnresolvedRefErrorsButContinues(t *testing.T) {
	body := bytes.Repeat([]byte("B"), 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	good := ensureModel(t, "mistralrs", "mistralrs:catalog:good", srv.URL+"/model.gguf", len(body))
	m := NewManager()
	m.RegisterProvider(&fakeProvider{name: "mistralrs", models: []ModelRecord{good}})

	err := m.EnsureModelsPresent(context.Background(), "mistralrs", []string{good.ID, "does-not-exist"})
	if err == nil {
		t.Fatalf("expected an error for the unresolved ref, got nil")
	}
	// The good one must still have been enqueued and completed.
	waitDownloadDone(t, m, good.ID)
	inv, _ := m.Inventory(context.Background())
	rec, _ := resolveInInventory(inv, "mistralrs", good.ID)
	if rec.DownloadState != Downloaded {
		t.Errorf("good model DownloadState = %v, want Downloaded despite sibling error", rec.DownloadState)
	}
}

// TestEnsureModelsPresent_EmptyWantIsNoop proves ollama-style runtimes (nothing
// wanted) and empty/whitespace refs are a clean no-op, not an error.
func TestEnsureModelsPresent_EmptyWantIsNoop(t *testing.T) {
	m := NewManager()
	m.RegisterProvider(&fakeProvider{name: "mistralrs"})
	if err := m.EnsureModelsPresent(context.Background(), "mistralrs", nil); err != nil {
		t.Errorf("nil want should be a no-op, got %v", err)
	}
	if err := m.EnsureModelsPresent(context.Background(), "mistralrs", []string{"", "  "}); err != nil {
		t.Errorf("blank refs should be a no-op, got %v", err)
	}
}

// TestEnsureModelsPresent_RequiresRuntime guards the one hard precondition.
func TestEnsureModelsPresent_RequiresRuntime(t *testing.T) {
	m := NewManager()
	if err := m.EnsureModelsPresent(context.Background(), "", []string{"x"}); err == nil {
		t.Errorf("empty runtime should error")
	}
}
