package server

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/pkg/config"
)

func TestBuildOpenRuntimeStatus_OllamaSuccess(t *testing.T) {
	cfg := config.Config{OpenRuntime: "ollama"}
	st := buildOpenRuntimeStatus("ollama", cfg, nil)

	if !st.Ok {
		t.Errorf("Ok should be true when runtime is ollama and no error, got %+v", st)
	}
	if st.Runtime != "ollama" {
		t.Errorf("Runtime = %q, want %q", st.Runtime, "ollama")
	}
	if st.Missing != "" {
		t.Errorf("Missing should be empty on success, got %q", st.Missing)
	}
	if st.SuggestedCommand != "" {
		t.Errorf("SuggestedCommand should be empty on success, got %q", st.SuggestedCommand)
	}
}

func TestBuildOpenRuntimeStatus_LlamaServerSuccess(t *testing.T) {
	cfg := config.Config{
		OpenRuntime: "llama_server",
		LlamaServer: config.LlamaServerConfig{
			Binary:       "/opt/homebrew/bin/llama-server",
			DefaultModel: "/models/qwen3.gguf",
		},
	}
	st := buildOpenRuntimeStatus("llama_server", cfg, nil)

	if !st.Ok {
		t.Errorf("Ok should be true on success, got %+v", st)
	}
	if st.BinaryPath != "/opt/homebrew/bin/llama-server" {
		t.Errorf("BinaryPath = %q, want the configured binary", st.BinaryPath)
	}
	if st.DefaultModel != "/models/qwen3.gguf" {
		t.Errorf("DefaultModel = %q, want the configured model", st.DefaultModel)
	}
}

func TestBuildOpenRuntimeStatus_MissingBinary(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	// Simulate a real detect error — no exec, we just construct the value.
	de := &llamaserver.DetectError{Missing: "binary", Cause: errors.New("exec: \"llama-server\": executable file not found in $PATH")}
	st := buildOpenRuntimeStatus("llama_server", cfg, de)

	if st.Ok {
		t.Errorf("Ok should be false when detection failed, got %+v", st)
	}
	if st.Missing != "binary" {
		t.Errorf("Missing = %q, want %q", st.Missing, "binary")
	}
	if st.SuggestedCommand != "brew install llama.cpp" {
		t.Errorf("SuggestedCommand = %q, want brew install", st.SuggestedCommand)
	}
	if !strings.Contains(st.Message, "binary") {
		t.Errorf("Message should mention what's missing, got %q", st.Message)
	}
}

func TestBuildMistralRSStatus_Ready(t *testing.T) {
	cfg := config.Config{
		OpenRuntime: "mistralrs",
		MistralRS: config.MistralRSConfig{
			Binary:       "/usr/local/bin/mistralrs-server",
			DefaultModel: "mistralrs:catalog:qwen3-14b",
		},
	}
	st := buildMistralRSStatus(cfg, false)

	if !st.Ok {
		t.Errorf("Ok should be true when model is present, got %+v", st)
	}
	if st.Runtime != "mistralrs" {
		t.Errorf("Runtime = %q, want %q", st.Runtime, "mistralrs")
	}
	if st.Missing != "" {
		t.Errorf("Missing should be empty when ready, got %q", st.Missing)
	}
	if st.BinaryPath != "/usr/local/bin/mistralrs-server" {
		t.Errorf("BinaryPath = %q, want the configured binary", st.BinaryPath)
	}
	if st.DefaultModel != "mistralrs:catalog:qwen3-14b" {
		t.Errorf("DefaultModel = %q, want the configured default", st.DefaultModel)
	}
}

func TestBuildMistralRSStatus_ModelNotDownloaded(t *testing.T) {
	cfg := config.Config{
		OpenRuntime: "mistralrs",
		MistralRS:   config.MistralRSConfig{DefaultModel: "mistralrs:catalog:qwen3-14b"},
	}
	st := buildMistralRSStatus(cfg, true)

	if st.Ok {
		t.Errorf("Ok should be false when model is missing, got %+v", st)
	}
	// Missing == "model" is what lights the same "(F1)" chip llama-server uses
	// for a missing GGUF — the CLI keys its label off this value.
	if st.Missing != "model" {
		t.Errorf("Missing = %q, want %q", st.Missing, "model")
	}
	if !strings.Contains(st.Message, "not downloaded") {
		t.Errorf("Message should say the model is not downloaded, got %q", st.Message)
	}
	if st.DefaultModel != "mistralrs:catalog:qwen3-14b" {
		t.Errorf("DefaultModel should carry through, got %q", st.DefaultModel)
	}
}

func TestBuildMistralRSStatus_NoDefaultConfigured(t *testing.T) {
	cfg := config.Config{OpenRuntime: "mistralrs"}
	st := buildMistralRSStatus(cfg, true)

	if st.Ok {
		t.Errorf("Ok should be false with no default configured, got %+v", st)
	}
	if st.Missing != "model" {
		t.Errorf("Missing = %q, want %q", st.Missing, "model")
	}
	// Distinct message from the "not downloaded" case so the diagnostic is
	// actionable — the user needs to pick a model, not wait on a download.
	if !strings.Contains(st.Message, "no default model") {
		t.Errorf("Message should say no default is configured, got %q", st.Message)
	}
}

func TestBuildOpenRuntimeStatus_MissingModelPreservesPartialConfig(t *testing.T) {
	// Even when detection fails at the model step, we've already found the
	// binary — cfg.LlamaServer.Binary is populated. The status must carry
	// that through so the CLI can render "found llama-server at X but no
	// GGUFs" instead of "nothing found."
	cfg := config.Config{
		OpenRuntime: "llama_server",
		LlamaServer:  config.LlamaServerConfig{Binary: "/opt/homebrew/bin/llama-server"},
	}
	de := &llamaserver.DetectError{Missing: "model", Cause: errors.New("no GGUF files")}
	st := buildOpenRuntimeStatus("llama_server", cfg, de)

	if st.Ok {
		t.Errorf("Ok should be false, got %+v", st)
	}
	if st.Missing != "model" {
		t.Errorf("Missing = %q, want %q", st.Missing, "model")
	}
	if st.BinaryPath != "/opt/homebrew/bin/llama-server" {
		t.Errorf("BinaryPath should be carried through even on model failure, got %q", st.BinaryPath)
	}
	// SuggestedCommand for a missing model is empty — nothing to auto-run.
	if st.SuggestedCommand != "" {
		t.Errorf("SuggestedCommand should be empty for missing model, got %q", st.SuggestedCommand)
	}
}
