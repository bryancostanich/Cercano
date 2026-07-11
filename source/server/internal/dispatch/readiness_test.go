package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/pkg/config"
)

func llamaCfg(enabled bool, defaultModel string) config.Config {
	return config.Config{
		OpenRuntime: "llama_server",
		LlamaServer: config.LlamaServerConfig{Enabled: enabled, DefaultModel: defaultModel},
	}
}

func TestOpenModelReady_PresentGGUF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !OpenModelReady(llamaCfg(true, path)) {
		t.Error("model file present on disk should be ready")
	}
}

func TestOpenModelReady_MissingGGUF(t *testing.T) {
	dir := t.TempDir()
	// Points at a path that doesn't exist — the "still downloading" state.
	if OpenModelReady(llamaCfg(true, filepath.Join(dir, "not-downloaded-yet.gguf"))) {
		t.Error("a not-yet-present model must register absent so routing crosses to cloud")
	}
}

func TestOpenModelReady_EmptyOrDisabled(t *testing.T) {
	if OpenModelReady(llamaCfg(true, "")) {
		t.Error("empty default model is not ready")
	}
	if OpenModelReady(llamaCfg(false, "/anything")) {
		t.Error("disabled llama-server is not ready")
	}
}

func TestOpenModelReady_NonLlamaRuntimeAlwaysReady(t *testing.T) {
	// Ollama and other runtimes manage their own model presence.
	c := config.Config{OpenRuntime: "ollama"}
	if !OpenModelReady(c) {
		t.Error("non-llama-server runtime should be treated as ready")
	}
}
