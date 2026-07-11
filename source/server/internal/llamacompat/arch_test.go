package llamacompat

import "testing"

func TestSupported_KnownArches(t *testing.T) {
	// A representative spread of architectures the pinned build must load:
	// the workhorse coder/chat families and the embedding encoders every
	// open tier depends on.
	for _, arch := range []string{
		"llama", "qwen2", "qwen3", "gemma3", "phi3",
		"deepseek2", "command-r", "granite", "bert", "nomic-bert",
	} {
		if !Supported(arch) {
			t.Errorf("Supported(%q) = false, want true", arch)
		}
	}
}

func TestSupported_GatedArches(t *testing.T) {
	// The whole point of the gate: brand-new architectures llama.cpp can't
	// yet load must be reported unsupported so they're never downloaded into
	// llama-server. qwen3-next is the concrete failure that motivated this.
	for _, arch := range []string{
		"qwen3next", "qwen3-next", "deepseek-v4", "ds4", "bogus-arch",
	} {
		if Supported(arch) {
			t.Errorf("Supported(%q) = true, want false (should be gated)", arch)
		}
	}
}

func TestSupported_EmptyIsUnsupported(t *testing.T) {
	if Supported("") {
		t.Error("Supported(\"\") = true, want false")
	}
}

func TestSupported_NormalizesInput(t *testing.T) {
	// Values from looser sources may carry case or surrounding whitespace.
	for _, arch := range []string{"QWEN2", "  qwen2  ", "Qwen2"} {
		if !Supported(arch) {
			t.Errorf("Supported(%q) = false, want true after normalization", arch)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"QWEN2":     "qwen2",
		"  llama  ": "llama",
		"Gemma3":    "gemma3",
		"":          "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSupportedArches_NonEmpty(t *testing.T) {
	// Guards against an accidental wipe of the seed set — the curated-catalog
	// validity test (in the llamaserver package) leans on this being populated.
	if len(SupportedArches()) == 0 {
		t.Fatal("SupportedArches() is empty; the seed set must not be blank")
	}
}
