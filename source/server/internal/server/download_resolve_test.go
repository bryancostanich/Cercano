package server

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/catalog"
)

// fakeCatalogBackend stubs catalog.Backend for the download-resolution tests,
// capturing the file ResolveDownload is asked for.
type fakeCatalogBackend struct {
	arch         string
	tools        bool
	files        []catalog.File
	urls         []string
	primary      string
	total        int64
	resolvedFile string
}

func (f *fakeCatalogBackend) Name() string { return "fake" }
func (f *fakeCatalogBackend) List(context.Context, catalog.ListOptions) ([]catalog.Model, error) {
	return nil, nil
}
func (f *fakeCatalogBackend) Detail(_ context.Context, id string) (catalog.Detail, error) {
	return catalog.Detail{Backend: "fake", ID: id, Architecture: f.arch, SupportsTools: f.tools, Files: f.files}, nil
}
func (f *fakeCatalogBackend) ResolveDownload(_ context.Context, _, file string) (catalog.DownloadPlan, error) {
	f.resolvedFile = file
	return catalog.DownloadPlan{URLs: f.urls, PrimaryFile: f.primary, TotalBytes: f.total}, nil
}

// TestBuildCatalogDownloadRecord_GatesUnsupportedArch is the safety net: an
// architecture llama.cpp can't load is refused before any download, even from
// a backend that happily offers it.
func TestBuildCatalogDownloadRecord_GatesUnsupportedArch(t *testing.T) {
	b := &fakeCatalogBackend{arch: "qwen3next", files: []catalog.File{{Name: "x-Q4_K_M.gguf"}}}
	_, err := buildCatalogDownloadRecord(context.Background(), b, "some/repo", "mid", "llama_server", "/models")
	if err == nil {
		t.Fatal("expected refusal for unsupported architecture qwen3next")
	}
	if !strings.Contains(err.Error(), "architecture") {
		t.Errorf("error = %v, want an architecture-refusal message", err)
	}
}

// TestBuildCatalogDownloadRecord_Compatible checks the happy path: default
// quant chosen, URLs/size/tools carried, path placed under the model dir.
func TestBuildCatalogDownloadRecord_Compatible(t *testing.T) {
	b := &fakeCatalogBackend{
		arch:    "qwen2",
		tools:   true,
		files:   []catalog.File{{Name: "x-Q2_K.gguf"}, {Name: "x-Q4_K_M.gguf"}},
		urls:    []string{"https://hf/x-Q4_K_M.gguf"},
		primary: "x-Q4_K_M.gguf",
		total:   123,
	}
	rec, err := buildCatalogDownloadRecord(context.Background(), b, "unsloth/x-GGUF", "mid", "llama_server", "/models")
	if err != nil {
		t.Fatalf("buildCatalogDownloadRecord: %v", err)
	}
	if b.resolvedFile != "x-Q4_K_M.gguf" {
		t.Errorf("resolved file = %q, want x-Q4_K_M.gguf (default-quant preference over first)", b.resolvedFile)
	}
	if len(rec.DownloadURLs) != 1 || rec.DownloadURLs[0] != "https://hf/x-Q4_K_M.gguf" {
		t.Errorf("DownloadURLs = %v", rec.DownloadURLs)
	}
	if rec.Path != "/models/x-Q4_K_M.gguf" {
		t.Errorf("Path = %q, want /models/x-Q4_K_M.gguf", rec.Path)
	}
	if rec.DownloadTotalBytes != 123 {
		t.Errorf("total = %d, want 123", rec.DownloadTotalBytes)
	}
	if !rec.SupportsTools {
		t.Error("SupportsTools should carry through from Detail")
	}
}

func TestPickDefaultQuant(t *testing.T) {
	f, ok := pickDefaultQuant([]catalog.File{{Name: "m-IQ2.gguf"}, {Name: "m-Q4_K_M.gguf"}, {Name: "m-Q8_0.gguf"}})
	if !ok || f.Name != "m-Q4_K_M.gguf" {
		t.Errorf("pickDefaultQuant = %+v/%v, want m-Q4_K_M.gguf", f, ok)
	}
	f2, ok2 := pickDefaultQuant([]catalog.File{{Name: "a.gguf"}, {Name: "b.gguf"}})
	if !ok2 || f2.Name != "a.gguf" {
		t.Errorf("fallback = %+v, want first file a.gguf", f2)
	}
	if _, ok3 := pickDefaultQuant(nil); ok3 {
		t.Error("empty files should return ok=false")
	}
}
