package contextmeter

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// localCfg builds a llama_server-backed config on a local locus mode.
func localCfg(mode string, ctxSize int) config.Config {
	var c config.Config
	c.LocusMode = mode
	c.OpenRuntime = "llama_server"
	c.LlamaServer.ContextSize = ctxSize
	c.LlamaServer.ContextSizeSet = true
	return c
}

// The whole point of the change: the local denominator comes from config, so
// editing context_size moves the meter with no code change. Asserting across a
// range (rather than one golden number) is what proves it is not hardcoded.
func TestMeterWindowTracksConfiguredContextSize(t *testing.T) {
	const model = "llama_server:catalog:glm-4.5-air-q4_k_m"
	for _, size := range []int{4096, 8192, 16384, 32768, 131072} {
		got := MeterWindow(localCfg("open_primary", size), model)
		if got.Tokens != size {
			t.Errorf("context_size=%d: window = %d, want %d", size, got.Tokens, size)
		}
		if !got.Known {
			t.Errorf("context_size=%d: Known = false, want true (config is authoritative)", size)
		}
	}
}

// Regression for the bug this fixes: GLM matches no known family, so the
// published table falls back to the 128K default. On a local route that
// over-reports headroom ~8x against a 16K runtime.
func TestMeterWindowLocalIgnoresPublishedDefault(t *testing.T) {
	const model = "llama_server:catalog:glm-4.5-air-q4_k_m"

	if pub := ModelWindowFor(model); pub.Tokens != 128_000 || pub.Known {
		t.Fatalf("precondition: ModelWindowFor = %+v, want {128000 false}", pub)
	}
	got := MeterWindow(localCfg("open_only", 16384), model)
	if got.Tokens != 16384 {
		t.Errorf("window = %d, want 16384 (configured), not the 128000 default", got.Tokens)
	}
}

// A local model matching a known family must still report the configured size:
// what the runtime serves beats what the family publishes. This is the case
// that a naive "known family wins" implementation would get wrong.
func TestMeterWindowLocalBeatsKnownFamilyWindow(t *testing.T) {
	const model = "llama_server:catalog:qwen3-coder-next"

	if pub, ok := KnownModelMax(model); !ok || pub != 262_144 {
		t.Fatalf("precondition: KnownModelMax = (%d,%v), want (262144,true)", pub, ok)
	}
	got := MeterWindow(localCfg("open_primary", 16384), model)
	if got.Tokens != 16384 {
		t.Errorf("window = %d, want 16384: the runtime serves the configured size", got.Tokens)
	}
}

// Cloud routes must be untouched — the remote window is a property of the
// remote model and has nothing to do with our local runtime config.
func TestMeterWindowCloudUsesPublishedTable(t *testing.T) {
	for _, mode := range []string{"cloud_primary", "cloud_only", ""} {
		c := localCfg(mode, 16384)
		got := MeterWindow(c, "claude-sonnet-4-5")
		if got.Tokens != 200_000 || !got.Known {
			t.Errorf("mode=%q: window = %+v, want {200000 true}", mode, got)
		}
	}
}

// Never hand the UI a zero denominator: if config yields nothing usable, fall
// back to the published table rather than rendering a divide-by-zero meter.
func TestMeterWindowLocalFallsBackWhenConfigEmpty(t *testing.T) {
	var c config.Config
	c.LocusMode = "open_primary" // no runtime, no sizes set

	got := MeterWindow(c, "claude-sonnet-4-5")
	if got.Tokens <= 0 {
		t.Fatalf("window = %d, want a positive fallback", got.Tokens)
	}
	if got.Tokens != 200_000 {
		t.Errorf("window = %d, want the published 200000 fallback", got.Tokens)
	}
}
