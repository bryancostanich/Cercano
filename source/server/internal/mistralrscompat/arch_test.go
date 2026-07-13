package mistralrscompat

import "testing"

func TestSupported_KnownArches(t *testing.T) {
	for _, arch := range []string{
		"llama", "mistral", "mixtral", "qwen2", "qwen3", "qwen3moe",
		"gemma2", "phi3", "deepseekv2", "deepseekv3", "glm4moe", "starcoder2",
		"smollm3", "granitemoehybrid", "gpt_oss", "lfm2_moe",
	} {
		if !Supported(arch) {
			t.Errorf("Supported(%q) = false, want true", arch)
		}
	}
}

// TestSupported_Qwen3Next is the headline: mistral.rs admits qwen3next — the
// hybrid-MoE architecture llamacompat deliberately gates out. This divergence
// is the reason mistral.rs exists as a second runtime.
func TestSupported_Qwen3Next(t *testing.T) {
	if !Supported("qwen3next") {
		t.Error(`Supported("qwen3next") = false, want true (mistral.rs loads it)`)
	}
}

// TestSupported_LlamaCppSpellingsGatedOut documents the model_type key space:
// mistral.rs spells some families differently from llama.cpp's GGUF arch, so
// llama.cpp's spellings are (correctly) unknown to this gate.
func TestSupported_LlamaCppSpellingsGatedOut(t *testing.T) {
	for _, arch := range []string{
		"deepseek2", // mistral.rs spells it deepseekv2
		"phimoe",    // mistral.rs spells it phi3.5moe
		"qwen3-next",
		"bogus-arch",
		"not-a-real-arch",
	} {
		if Supported(arch) {
			t.Errorf("Supported(%q) = true, want false", arch)
		}
	}
}

func TestSupported_EmptyIsUnsupported(t *testing.T) {
	if Supported("") {
		t.Error(`Supported("") = true, want false`)
	}
}

func TestSupported_Normalizes(t *testing.T) {
	for _, arch := range []string{"QWEN3NEXT", "  qwen3next  ", "Qwen3Next"} {
		if !Supported(arch) {
			t.Errorf("Supported(%q) = false, want true after normalization", arch)
		}
	}
}

func TestSupportedArches_NonEmpty(t *testing.T) {
	if len(SupportedArches()) == 0 {
		t.Fatal("SupportedArches() is empty; the seed set must not be blank")
	}
}
