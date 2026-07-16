package server

import (
	"strings"
	"testing"
)

// These tests cover the single runtime-agnostic formatter openRuntimeStatusFrom
// (which replaced buildOpenRuntimeStatus + buildMistralRSStatus). The formatter
// maps a resolved openRuntimeReadiness onto the wire OpenRuntimeStatus. The
// readiness RESOLUTION (inventory-aware) is covered separately with a fake
// manager in open_runtime_readiness_test.go.

func TestOpenRuntimeStatusFrom_Ready(t *testing.T) {
	st := openRuntimeStatusFrom("ollama", openRuntimeReadiness{
		State:   readyToServe,
		Message: "ollama runtime active",
	})
	if !st.Ok {
		t.Errorf("Ok should be true for readyToServe, got %+v", st)
	}
	if st.Runtime != "ollama" {
		t.Errorf("Runtime = %q, want ollama", st.Runtime)
	}
	if st.Missing != "" || st.Downloading {
		t.Errorf("ready status must have empty Missing and Downloading=false, got %+v", st)
	}
}

func TestOpenRuntimeStatusFrom_Downloading(t *testing.T) {
	st := openRuntimeStatusFrom("mistralrs", openRuntimeReadiness{
		State:        readyDownloading,
		Message:      "mistralrs default model downloading…",
		DefaultModel: "mistralrs:catalog:qwen3-14b",
	})
	// The whole point of Phase 1: downloading is NOT ok, NOT missing — it's its
	// own state so the CLI shows "o: downloading" and never nags with (F1).
	if st.Ok {
		t.Errorf("Ok should be false while downloading, got %+v", st)
	}
	if !st.Downloading {
		t.Errorf("Downloading should be true, got %+v", st)
	}
	if st.Missing != "" {
		t.Errorf("Missing must be empty while downloading (no user action), got %q", st.Missing)
	}
	if st.DefaultModel != "mistralrs:catalog:qwen3-14b" {
		t.Errorf("DefaultModel should carry through, got %q", st.DefaultModel)
	}
}

func TestOpenRuntimeStatusFrom_MissingModel(t *testing.T) {
	st := openRuntimeStatusFrom("mistralrs", openRuntimeReadiness{
		State:        readyMissing,
		Missing:      "model",
		Message:      "mistralrs default model not downloaded",
		DefaultModel: "mistralrs:catalog:qwen3-14b",
	})
	if st.Ok || st.Downloading {
		t.Errorf("missing status must be Ok=false Downloading=false, got %+v", st)
	}
	if st.Missing != "model" {
		t.Errorf("Missing = %q, want model", st.Missing)
	}
}

func TestOpenRuntimeStatusFrom_MissingBinaryCarriesSuggestedCommand(t *testing.T) {
	st := openRuntimeStatusFrom("llama_server", openRuntimeReadiness{
		State:            readyMissing,
		Missing:          "binary",
		Message:          "llama-server binary not found",
		Binary:           "/opt/homebrew/bin/llama-server",
		SuggestedCommand: "brew install llama.cpp",
	})
	if st.Ok {
		t.Errorf("Ok should be false when binary missing, got %+v", st)
	}
	if st.Missing != "binary" {
		t.Errorf("Missing = %q, want binary", st.Missing)
	}
	if st.SuggestedCommand != "brew install llama.cpp" {
		t.Errorf("SuggestedCommand should carry through, got %q", st.SuggestedCommand)
	}
	// Even on failure, the resolved binary path is preserved so the CLI can say
	// "found llama-server at X but ...".
	if st.BinaryPath != "/opt/homebrew/bin/llama-server" {
		t.Errorf("BinaryPath should carry through on failure, got %q", st.BinaryPath)
	}
	if !strings.Contains(st.Message, "binary") {
		t.Errorf("Message should mention what's missing, got %q", st.Message)
	}
}
