package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults_CatalogBackendHuggingFace(t *testing.T) {
	if got := Defaults().Catalog.Backend; got != "huggingface" {
		t.Errorf("default catalog backend = %q, want huggingface", got)
	}
}

// TestLoad_CatalogBackendDefaultsWhenAbsent guards the normalization fill: a
// config file with no catalog: block must load with the default backend, not
// an empty string (which would fail loud when the registry is wired).
func TestLoad_CatalogBackendDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("open_runtime: llama_server\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Catalog.Backend != "huggingface" {
		t.Errorf("backend = %q, want huggingface (filled from defaults)", cfg.Catalog.Backend)
	}
}

func TestLoad_CatalogBackendExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("catalog:\n  backend: ollama\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Catalog.Backend != "ollama" {
		t.Errorf("backend = %q, want ollama (explicit)", cfg.Catalog.Backend)
	}
}
