package llamaserver

import (
	"testing"

	"cercano/source/server/pkg/config"
)

func TestEffectiveContextSize_ModelOverrideWins(t *testing.T) {
	// Regression: config said 16384 while the catalog pinned 32768. Because
	// per-model flags are appended last and llama-server honors the last
	// --ctx-size, the server really served 32768 — but the agent budgeted
	// against 16384 and falsely rejected requests that would have fit.
	got := EffectiveContextSize(16384, []string{"--jinja", "--ctx-size", "32768", "--cache-type-k", "q8_0"})
	if got != 32768 {
		t.Fatalf("EffectiveContextSize = %d, want 32768 (model override wins)", got)
	}
}

func TestEffectiveContextSize_FallsBackToConfig(t *testing.T) {
	if got := EffectiveContextSize(16384, []string{"--jinja"}); got != 16384 {
		t.Fatalf("EffectiveContextSize = %d, want 16384 (no override)", got)
	}
	if got := EffectiveContextSize(16384, nil); got != 16384 {
		t.Fatalf("EffectiveContextSize(nil) = %d, want 16384", got)
	}
}

func TestEffectiveContextSize_LastOccurrenceWins(t *testing.T) {
	// Mirror llama-server's own parsing: the last flag on the line wins.
	got := EffectiveContextSize(8192, []string{"--ctx-size", "16384", "--ctx-size", "65536"})
	if got != 65536 {
		t.Fatalf("EffectiveContextSize = %d, want 65536 (last wins)", got)
	}
}

func TestEffectiveContextSize_IgnoresMalformed(t *testing.T) {
	for _, args := range [][]string{
		{"--ctx-size"},            // trailing flag, no value
		{"--ctx-size", "notanum"}, // unparsable
		{"--ctx-size", "0"},       // non-positive
		{"--ctx-size", "-1"},
	} {
		if got := EffectiveContextSize(16384, args); got != 16384 {
			t.Fatalf("EffectiveContextSize(%v) = %d, want config fallback 16384", args, got)
		}
	}
}

func TestModelContextOverride_RealCatalogEntry(t *testing.T) {
	// GLM-4.5-Air is the model that exposed the bug; it pins 32768.
	if got := ModelContextOverride("glm-4.5-air-q4_k_m"); got != 32768 {
		t.Fatalf("ModelContextOverride(glm-4.5-air-q4_k_m) = %d, want 32768", got)
	}
	// A model with no pin, and an unknown ID, both report 0 so callers keep config.
	if got := ModelContextOverride("definitely-not-a-model"); got != 0 {
		t.Fatalf("ModelContextOverride(unknown) = %d, want 0", got)
	}
}

func TestArgsFor_NoDuplicateCtxSizeWhenModelPins(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 16384})
	model := provider.modelRecord("/models/glm.gguf", fakeFileInfo{size: 42})
	model.ExtraArgs = []string{"--jinja", "--ctx-size", "32768"}

	args := provider.argsFor(provider.snapshot(), model, 8123)

	n := 0
	for _, a := range args {
		if a == "--ctx-size" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("--ctx-size appears %d times in %v, want exactly 1", n, args)
	}
	if got := ctxSizeFromArgs(args); got != 32768 {
		t.Fatalf("effective ctx-size on command line = %d, want 32768", got)
	}
}

func TestArgsFor_EmitsConfigCtxSizeWhenModelDoesNotPin(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 16384})
	model := provider.modelRecord("/models/test.gguf", fakeFileInfo{size: 42})
	model.ExtraArgs = []string{"--jinja"}

	args := provider.argsFor(provider.snapshot(), model, 8123)
	if got := ctxSizeFromArgs(args); got != 16384 {
		t.Fatalf("effective ctx-size = %d, want config 16384", got)
	}
}
