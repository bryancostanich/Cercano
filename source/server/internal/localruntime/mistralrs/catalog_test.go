package mistralrs

import (
	"path/filepath"
	"testing"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/mistralrscompat"
	"cercano/source/server/pkg/config"
)

// TestCuratedCatalogValid asserts the embedded catalog.json parses and every
// entry is gate-compatible and well-formed — a bad entry fails the build here,
// not a user's setup.
func TestCuratedCatalogValid(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	if len(cat.Models) == 0 {
		t.Fatal("curated catalog is empty")
	}
	for _, m := range cat.Models {
		if !mistralrscompat.Supported(m.Architecture) {
			t.Errorf("model %q arch %q not admitted by mistralrscompat", m.ID, m.Architecture)
		}
		if len(m.Files) == 0 {
			t.Errorf("model %q has no files", m.ID)
		}
		if m.SizeBytes <= 0 {
			t.Errorf("model %q has non-positive size_bytes", m.ID)
		}
		if m.Repo == "" {
			t.Errorf("model %q has no repo", m.ID)
		}
	}
}

// TestCatalogModelsShape checks the record catalogModels() produces for a
// multi-file safetensors entry: its own subdirectory, LoadTarget = that
// directory (mistral.rs -m <dir>), Path anchored on the first manifest file,
// one download URL per file, and not-downloaded when nothing is on disk.
func TestCatalogModelsShape(t *testing.T) {
	dir := t.TempDir()
	p := NewProvider(config.MistralRSConfig{ModelDirs: []string{dir}})

	var m4b *localruntime.ModelRecord
	for i, m := range p.catalogModels() {
		if m.ID == runtimeName+":catalog:qwen3-4b" {
			models := p.catalogModels()
			m4b = &models[i]
			break
		}
	}
	if m4b == nil {
		t.Fatal("qwen3-4b not surfaced by catalogModels")
	}
	if m4b.Format != "safetensors" {
		t.Errorf("Format = %q, want safetensors", m4b.Format)
	}
	wantDir := filepath.Join(dir, "qwen3-4b")
	if m4b.LoadTarget != wantDir {
		t.Errorf("LoadTarget = %q, want %q", m4b.LoadTarget, wantDir)
	}
	if want := filepath.Join(wantDir, "config.json"); m4b.Path != want {
		t.Errorf("Path = %q, want %q", m4b.Path, want)
	}
	if len(m4b.DownloadURLs) != 10 {
		t.Errorf("DownloadURLs count = %d, want 10", len(m4b.DownloadURLs))
	}
	if len(m4b.DownloadURLs) > 0 &&
		m4b.DownloadURLs[0] != "https://huggingface.co/Qwen/Qwen3-4B/resolve/main/config.json" {
		t.Errorf("first URL = %q", m4b.DownloadURLs[0])
	}
	if !m4b.SupportsTools {
		t.Error("SupportsTools should be true for Qwen3")
	}
	if m4b.DownloadState != localruntime.DownloadNotStarted {
		t.Errorf("DownloadState = %s, want not_downloaded (nothing on disk)", m4b.DownloadState)
	}
}
