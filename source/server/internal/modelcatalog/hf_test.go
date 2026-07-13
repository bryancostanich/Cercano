package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer stands in for the HuggingFace API: a list endpoint and a
// per-repo detail endpoint returning canned JSON shaped like the real API.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	listJSON := `[
		{"id":"unsloth/Qwen3-14B-GGUF","downloads":500000,"likes":300},
		{"id":"randomguy/Sketchy-Quant-GGUF","downloads":999999,"likes":1},
		{"id":"bartowski/Phi-4-mini-instruct-GGUF","downloads":67000,"likes":116}
	]`
	detailJSON := `{
		"id":"unsloth/Qwen3-14B-GGUF",
		"gguf":{"architecture":"qwen3","context_length":40960,"chat_template":"... <tool_call> ..."},
		"siblings":[
			{"rfilename":"README.md","size":1000},
			{"rfilename":"Qwen3-14B-Q4_K_M.gguf","size":9001753984,"lfs":{"size":9001753984}},
			{"rfilename":"Qwen3-14B-Q8_0.gguf","size":15698534784}
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

func TestListModels_FiltersToTrustedAuthors(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}

	models, err := c.ListModels(context.Background(), 10, "")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// The high-download "randomguy" repo is dropped; ranking order is kept.
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (untrusted author filtered out)", len(models))
	}
	if models[0].Repo != "unsloth/Qwen3-14B-GGUF" || models[1].Repo != "bartowski/Phi-4-mini-instruct-GGUF" {
		t.Errorf("unexpected models/order: %+v", models)
	}
	if models[0].Author != "unsloth" {
		t.Errorf("author = %q, want unsloth", models[0].Author)
	}
}

func TestListModels_UsesRequestedFormatFilter(t *testing.T) {
	var gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			http.NotFound(w, r)
			return
		}
		gotFilter = r.URL.Query().Get("filter")
		_, _ = w.Write([]byte(`[{"id":"Qwen/Qwen3-4B","downloads":100,"likes":10}]`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}

	models, err := c.ListModels(context.Background(), 10, "safetensors")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotFilter != "safetensors" {
		t.Errorf("filter = %q, want safetensors", gotFilter)
	}
	if len(models) != 1 || models[0].Repo != "Qwen/Qwen3-4B" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestModelDetail_ParsesGGUFAndFiles(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}

	d, err := c.ModelDetail(context.Background(), "unsloth/Qwen3-14B-GGUF")
	if err != nil {
		t.Fatalf("ModelDetail: %v", err)
	}
	if d.Architecture != "qwen3" {
		t.Errorf("arch = %q, want qwen3", d.Architecture)
	}
	if d.ContextLength != 40960 {
		t.Errorf("context = %d, want 40960", d.ContextLength)
	}
	if !d.SupportsTools {
		t.Error("SupportsTools = false, want true (template has tool_call)")
	}
	// Only .gguf siblings; README.md is filtered out.
	if len(d.Files) != 2 {
		t.Fatalf("got %d files, want 2 (.gguf only)", len(d.Files))
	}
	if d.Files[0].Name != "Qwen3-14B-Q4_K_M.gguf" || d.Files[0].SizeBytes != 9001753984 {
		t.Errorf("file0 = %+v, want Q4_K_M @ 9001753984", d.Files[0])
	}
	if d.Files[1].SizeBytes != 15698534784 {
		t.Errorf("file1 size = %d, want 15698534784 (from size when no lfs)", d.Files[1].SizeBytes)
	}
}

func TestModelDetail_ParsesSafetensorsManifest(t *testing.T) {
	detailJSON := `{
		"id":"Qwen/Qwen3-4B",
		"config":{
			"model_type":"qwen3",
			"architectures":["Qwen3ForCausalLM"],
			"tokenizer_config":{"chat_template":"... <tools> ..."}
		},
		"siblings":[
			{"rfilename":"README.md","size":1000},
			{"rfilename":"config.json","size":726},
			{"rfilename":"generation_config.json","size":239},
			{"rfilename":"model-00001-of-00002.safetensors","size":0,"lfs":{"size":3441185608}},
			{"rfilename":"model-00002-of-00002.safetensors","size":622329984},
			{"rfilename":"model.safetensors.index.json","size":25605},
			{"rfilename":"tokenizer.json","size":11422654},
			{"rfilename":"tokenizer_config.json","size":9732},
			{"rfilename":"vocab.json","size":2776833},
			{"rfilename":"figure.png","size":99}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/models/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(detailJSON))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}

	d, err := c.ModelDetail(context.Background(), "Qwen/Qwen3-4B")
	if err != nil {
		t.Fatalf("ModelDetail: %v", err)
	}
	if d.Format != "safetensors" || d.Architecture != "qwen3" {
		t.Fatalf("format/arch = %q/%q, want safetensors/qwen3", d.Format, d.Architecture)
	}
	if !d.SupportsTools {
		t.Error("SupportsTools = false, want true (template has <tools>)")
	}
	if len(d.Files) != 2 {
		t.Fatalf("Files count = %d, want 2 safetensors shards", len(d.Files))
	}
	if d.Files[0].SizeBytes != 3441185608 {
		t.Errorf("first shard size = %d, want LFS size", d.Files[0].SizeBytes)
	}
	if len(d.Manifest) != 8 {
		t.Fatalf("Manifest count = %d, want 8 (README/image excluded)", len(d.Manifest))
	}
	if d.Manifest[0].Name != "config.json" {
		t.Errorf("manifest[0] = %q, want config.json", d.Manifest[0].Name)
	}
}

func TestCompatible_GatesArchitecture(t *testing.T) {
	if !(HFModelDetail{Architecture: "qwen3"}).Compatible() {
		t.Error("qwen3 should be compatible")
	}
	// The whole point: qwen3-next is filtered before a user can pull it.
	if (HFModelDetail{Architecture: "qwen3next"}).Compatible() {
		t.Error("qwen3next should be gated as incompatible")
	}
}

func TestDownloadURL(t *testing.T) {
	got := DownloadURL("https://huggingface.co", "unsloth/Qwen3-14B-GGUF", "Qwen3-14B-Q4_K_M.gguf")
	want := "https://huggingface.co/unsloth/Qwen3-14B-GGUF/resolve/main/Qwen3-14B-Q4_K_M.gguf"
	if got != want {
		t.Errorf("DownloadURL = %q, want %q", got, want)
	}
}

func TestTemplateSupportsTools(t *testing.T) {
	if !templateSupportsTools("blah <tool_call> blah") {
		t.Error("tool_call marker not detected")
	}
	if !templateSupportsTools("has a <tools> block") {
		t.Error("<tools> marker not detected")
	}
	if templateSupportsTools("plain user/assistant template") {
		t.Error("false positive on a toolless template")
	}
}
