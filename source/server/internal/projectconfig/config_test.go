package projectconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, dir, body string) {
	t.Helper()
	cercanoDir := filepath.Join(dir, ".cercano")
	if err := os.MkdirAll(cercanoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cercanoDir, "config.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Validator.Skip || cfg.Validator.Command != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestLoad_ParsesValidatorBlock(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator:\n  command: dotnet build src/App.fsproj\n  skip: false\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Validator.Command != "dotnet build src/App.fsproj" {
		t.Errorf("got command %q", cfg.Validator.Command)
	}
	if cfg.Validator.Skip {
		t.Errorf("expected skip=false")
	}
}

func TestLoad_SkipTrue(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator:\n  skip: true\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !cfg.Validator.Skip {
		t.Errorf("expected skip=true")
	}
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator: [::not yaml")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error on malformed yaml")
	}
	if !strings.Contains(err.Error(), "invalid .cercano/config.yaml") {
		t.Errorf("err = %q, want it to contain 'invalid .cercano/config.yaml'", err.Error())
	}
}
