package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.OllamaURL)
	}
	if cfg.LocalModel != "qwen3-coder" {
		t.Errorf("expected default LocalModel, got %q", cfg.LocalModel)
	}
	if cfg.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected default EmbeddingModel, got %q", cfg.EmbeddingModel)
	}
	if cfg.Port != "50052" {
		t.Errorf("expected default Port, got %q", cfg.Port)
	}
}

func TestLoad_NoFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.OllamaURL)
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("ollama_url: http://mac-studio.local:11434\nlocal_model: GLM-4.7-Flash\n"), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.OllamaURL != "http://mac-studio.local:11434" {
		t.Errorf("expected OllamaURL from file, got %q", cfg.OllamaURL)
	}
	if cfg.LocalModel != "GLM-4.7-Flash" {
		t.Errorf("expected LocalModel from file, got %q", cfg.LocalModel)
	}
	// Defaults should fill in unset fields
	if cfg.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected default EmbeddingModel, got %q", cfg.EmbeddingModel)
	}
	if cfg.Port != "50052" {
		t.Errorf("expected default Port, got %q", cfg.Port)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("ollama_url: http://from-file:11434\nlocal_model: file-model\n"), 0644)

	t.Setenv("OLLAMA_URL", "http://from-env:11434")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// Env var should override file
	if cfg.OllamaURL != "http://from-env:11434" {
		t.Errorf("expected env override for OllamaURL, got %q", cfg.OllamaURL)
	}
	// File value should remain where no env var exists
	if cfg.LocalModel != "file-model" {
		t.Errorf("expected LocalModel from file, got %q", cfg.LocalModel)
	}
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	t.Setenv("CERCANO_LOCAL_MODEL", "env-model")
	t.Setenv("CERCANO_PORT", "9999")

	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.LocalModel != "env-model" {
		t.Errorf("expected env LocalModel, got %q", cfg.LocalModel)
	}
	if cfg.Port != "9999" {
		t.Errorf("expected env Port, got %q", cfg.Port)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("expected no error for empty path, got %v", err)
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected defaults for empty path, got %q", cfg.OllamaURL)
	}
}

func TestLoad_GeminiKeySetsCloudDefaults(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key-123")

	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CloudAPIKey != "test-key-123" {
		t.Errorf("expected CloudAPIKey from env, got %q", cfg.CloudAPIKey)
	}
	if cfg.CloudProvider != "google" {
		t.Errorf("expected default CloudProvider 'google', got %q", cfg.CloudProvider)
	}
	if cfg.CloudModel != "gemini-3-flash" {
		t.Errorf("expected default CloudModel, got %q", cfg.CloudModel)
	}
}

func TestSave_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.yaml")

	cfg := Defaults()
	cfg.OllamaURL = "http://saved:11434"

	err := Save(cfg, path)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read back and verify
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save failed: %v", err)
	}
	if loaded.OllamaURL != "http://saved:11434" {
		t.Errorf("expected saved OllamaURL, got %q", loaded.OllamaURL)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := Config{
		OllamaURL:      "http://studio.local:11434",
		LocalModel:     "GLM-4.7-Flash",
		EmbeddingModel: "nomic-embed-text",
		CloudProvider:  "google",
		CloudModel:     "gemini-2.0-flash",
		Port:           "50053",
	}

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.OllamaURL != cfg.OllamaURL {
		t.Errorf("OllamaURL mismatch: %q vs %q", loaded.OllamaURL, cfg.OllamaURL)
	}
	if loaded.LocalModel != cfg.LocalModel {
		t.Errorf("LocalModel mismatch: %q vs %q", loaded.LocalModel, cfg.LocalModel)
	}
	if loaded.CloudProvider != cfg.CloudProvider {
		t.Errorf("CloudProvider mismatch: %q vs %q", loaded.CloudProvider, cfg.CloudProvider)
	}
	if loaded.Port != cfg.Port {
		t.Errorf("Port mismatch: %q vs %q", loaded.Port, cfg.Port)
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Skip("could not determine home directory")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("expected config.yaml filename, got %q", filepath.Base(path))
	}
}
