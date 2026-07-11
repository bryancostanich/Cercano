package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/catalog"
)

// shardedServer serves a list plus a detail for a model whose Q4_K_M ships as a
// two-shard split — the case ResolveDownload has to group.
func shardedServer(t *testing.T) *httptest.Server {
	t.Helper()
	listJSON := `[
		{"id":"unsloth/GLM-4.5-Air-GGUF","downloads":24000,"likes":178},
		{"id":"randomguy/junk-GGUF","downloads":999999,"likes":0}
	]`
	detailJSON := `{
		"id":"unsloth/GLM-4.5-Air-GGUF",
		"gguf":{"architecture":"glm4moe","context_length":131072,"chat_template":"has <tool_call> here"},
		"siblings":[
			{"rfilename":"README.md","size":10},
			{"rfilename":"Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf","size":50000000000},
			{"rfilename":"Q4_K_M/GLM-4.5-Air-Q4_K_M-00002-of-00002.gguf","size":23000000000},
			{"rfilename":"Q2_K/GLM-4.5-Air-Q2_K.gguf","size":40000000000}
		]
	}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/models/"):
			_, _ = w.Write([]byte(detailJSON))
		case r.URL.Path == "/api/models":
			_, _ = w.Write([]byte(listJSON))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestBackend_ListMapsToCatalogModel(t *testing.T) {
	srv := shardedServer(t)
	defer srv.Close()
	b := NewBackend(&Client{BaseURL: srv.URL})

	models, err := b.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1 (untrusted author filtered)", len(models))
	}
	if models[0].Backend != BackendName || models[0].ID != "unsloth/GLM-4.5-Air-GGUF" {
		t.Errorf("model = %+v, want backend=huggingface id=unsloth/GLM-4.5-Air-GGUF", models[0])
	}
}

func TestBackend_DetailMapsArchAndTools(t *testing.T) {
	srv := shardedServer(t)
	defer srv.Close()
	b := NewBackend(&Client{BaseURL: srv.URL})

	d, err := b.Detail(context.Background(), "unsloth/GLM-4.5-Air-GGUF")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.Backend != BackendName || d.Architecture != "glm4moe" {
		t.Errorf("detail backend/arch = %q/%q", d.Backend, d.Architecture)
	}
	if !d.SupportsTools {
		t.Error("SupportsTools = false, want true")
	}
	if len(d.Files) != 3 {
		t.Errorf("got %d files, want 3 (.gguf only, README filtered)", len(d.Files))
	}
}

func TestBackend_ResolveDownload_SingleFile(t *testing.T) {
	srv := shardedServer(t)
	defer srv.Close()
	b := NewBackend(&Client{BaseURL: srv.URL})

	plan, err := b.ResolveDownload(context.Background(), "unsloth/GLM-4.5-Air-GGUF", "Q2_K/GLM-4.5-Air-Q2_K.gguf")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if len(plan.URLs) != 1 {
		t.Fatalf("got %d URLs, want 1 for a normal quant", len(plan.URLs))
	}
	if plan.TotalBytes != 40000000000 {
		t.Errorf("total = %d, want 40000000000", plan.TotalBytes)
	}
	want := srv.URL + "/unsloth/GLM-4.5-Air-GGUF/resolve/main/Q2_K/GLM-4.5-Air-Q2_K.gguf"
	if plan.URLs[0] != want {
		t.Errorf("URL = %q, want %q", plan.URLs[0], want)
	}
}

func TestBackend_ResolveDownload_Sharded(t *testing.T) {
	srv := shardedServer(t)
	defer srv.Close()
	b := NewBackend(&Client{BaseURL: srv.URL})

	// Picking one shard must resolve the whole group, in shard order.
	plan, err := b.ResolveDownload(context.Background(), "unsloth/GLM-4.5-Air-GGUF",
		"Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if len(plan.URLs) != 2 {
		t.Fatalf("got %d URLs, want 2 (both shards)", len(plan.URLs))
	}
	if plan.TotalBytes != 73000000000 {
		t.Errorf("total = %d, want 73000000000 (sum of shards)", plan.TotalBytes)
	}
	if !strings.HasSuffix(plan.URLs[0], "00001-of-00002.gguf") {
		t.Errorf("URL[0] = %q, want shard 00001 first", plan.URLs[0])
	}
	if !strings.HasSuffix(plan.URLs[1], "00002-of-00002.gguf") {
		t.Errorf("URL[1] = %q, want shard 00002 second", plan.URLs[1])
	}
	if plan.PrimaryFile != "Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf" {
		t.Errorf("PrimaryFile = %q, want shard 1", plan.PrimaryFile)
	}
}
