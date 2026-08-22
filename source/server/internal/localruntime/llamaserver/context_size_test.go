package llamaserver

import (
	"testing"

	"cercano/source/server/pkg/config"
)

func TestEffectiveContextSize_Precedence(t *testing.T) {
	in := ContextSizeInput{
		ConfigContextSize:  8192,
		ProfileContextSize: 65536,
		ModelExtraArgs:     []string{"--jinja", "--ctx-size", "32768"},
		DefaultContextSize: 4096,
	}
	if got := EffectiveContextSize(in); got != 65536 {
		t.Fatalf("profile context = %d, want 65536", got)
	}
	in.ConfigExplicit = true
	in.ConfigContextSize = 131072
	if got := EffectiveContextSize(in); got != 131072 {
		t.Fatalf("explicit config context = %d, want 131072", got)
	}
}

func TestEffectiveContextSize_ModelOverrideLegacyFallback(t *testing.T) {
	got := EffectiveContextSize(ContextSizeInput{
		ConfigContextSize:  8192,
		ModelExtraArgs:     []string{"--jinja", "--ctx-size", "32768", "--cache-type-k", "q8_0"},
		DefaultContextSize: 4096,
	})
	if got != 32768 {
		t.Fatalf("EffectiveContextSize = %d, want legacy model override 32768", got)
	}
}

func TestEffectiveContextSize_FallsBackToDefaultThenConfig(t *testing.T) {
	if got := EffectiveContextSize(ContextSizeInput{ConfigContextSize: 16384, DefaultContextSize: 8192}); got != 8192 {
		t.Fatalf("with default context = %d, want 8192", got)
	}
	if got := EffectiveContextSize(ContextSizeInput{ConfigContextSize: 16384}); got != 16384 {
		t.Fatalf("without default context = %d, want config fallback 16384", got)
	}
}

func TestEffectiveContextSize_LastModelOccurrenceWins(t *testing.T) {
	// Mirror llama-server's own legacy parsing: the last model flag on the line wins.
	got := EffectiveContextSize(ContextSizeInput{
		ConfigContextSize: 8192,
		ModelExtraArgs:    []string{"--ctx-size", "16384", "--ctx-size", "65536"},
	})
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
		if got := EffectiveContextSize(ContextSizeInput{ConfigContextSize: 16384, ModelExtraArgs: args}); got != 16384 {
			t.Fatalf("EffectiveContextSize(%v) = %d, want config fallback 16384", args, got)
		}
	}
}

func TestModelContextOverride_ProfileEntry(t *testing.T) {
	const gib = uint64(1024 * 1024 * 1024)
	if got := ModelContextOverride("glm-4.5-air-q4_k_m", 128*gib); got != 131072 {
		t.Fatalf("128GB GLM context = %d, want 131072", got)
	}
	if got := ModelContextOverride("glm-4.5-air-q4_k_m", 96*gib); got != 65536 {
		t.Fatalf("96GB GLM context = %d, want 65536", got)
	}
	if got := ModelContextOverride("definitely-not-a-model", 128*gib); got != 0 {
		t.Fatalf("ModelContextOverride(unknown) = %d, want 0", got)
	}
}

func TestArgsFor_NoDuplicateCtxSizeWhenModelPinsLegacy(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 16384})
	model := provider.modelRecord("/models/glm.gguf", fakeFileInfo{size: 42})
	model.ExtraArgs = []string{"--jinja", "--ctx-size", "32768"}

	args := provider.argsFor(provider.snapshot(), model, 8123)

	assertSingleCtxSize(t, args, 32768)
	if hasFlag(args, "--ctx-size") && hasFlag(model.ExtraArgs, "--ctx-size") {
		// The model still has legacy metadata, but launch args must carry only the
		// resolved single flag, not re-append the legacy duplicate later.
		if countFlag(args, "--ctx-size") != 1 {
			t.Fatalf("expected one --ctx-size in %v", args)
		}
	}
}

func TestArgsFor_ProfileCtxBeatsLegacyModelPin(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 8192})
	model := provider.modelRecord("/models/glm.gguf", fakeFileInfo{size: 42})
	model.ContextSize = 131072
	model.ExtraArgs = []string{"--jinja", "--ctx-size", "32768"}

	args := provider.argsFor(provider.snapshot(), model, 8123)

	assertSingleCtxSize(t, args, 131072)
}

func TestArgsFor_ExplicitConfigBeatsProfileCtx(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 65536, ContextSizeSet: true})
	model := provider.modelRecord("/models/glm.gguf", fakeFileInfo{size: 42})
	model.ContextSize = 131072
	model.ExtraArgs = []string{"--jinja"}

	args := provider.argsFor(provider.snapshot(), model, 8123)

	assertSingleCtxSize(t, args, 65536)
}

func TestArgsFor_EmitsDefaultConfigCtxSizeWhenNoOverride(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1", ContextSize: 16384})
	model := provider.modelRecord("/models/test.gguf", fakeFileInfo{size: 42})
	model.ExtraArgs = []string{"--jinja"}

	args := provider.argsFor(provider.snapshot(), model, 8123)
	assertSingleCtxSize(t, args, 8192)
}

func assertSingleCtxSize(t *testing.T, args []string, want int) {
	t.Helper()
	if n := countFlag(args, "--ctx-size"); n != 1 {
		t.Fatalf("--ctx-size appears %d times in %v, want exactly 1", n, args)
	}
	if got := ctxSizeFromArgs(args); got != want {
		t.Fatalf("effective ctx-size on command line = %d, want %d (args=%v)", got, want, args)
	}
}

func countFlag(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}
