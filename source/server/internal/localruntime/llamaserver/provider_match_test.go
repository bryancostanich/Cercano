package llamaserver

import (
	"testing"

	"cercano/source/server/internal/localruntime"
)

// TestMatchesModel_LatestAlias covers the Ollama-name alias: tier/config
// values written as bare names (qwen3-coder-next) must resolve files whose
// stem carries the baked-in ":latest" tag (qwen3-coder-next-latest.gguf),
// and near-miss names must NOT match a different model's file.
func TestMatchesModel_LatestAlias(t *testing.T) {
	rec := func(path string) localruntime.ModelRecord {
		return localruntime.ModelRecord{
			ID:          "llama_server:abcdef123456",
			DisplayName: "qwen3-coder-next-latest",
			Path:        path,
		}
	}
	qwen := rec("/models/qwen3-coder-next-latest.gguf")

	for _, requested := range []string{
		"qwen3-coder-next",             // bare name → -latest stem
		"qwen3-coder-next:latest",      // ollama tag form
		"qwen3-coder-next-latest",      // exact display name (pre-existing)
		"qwen3-coder-next-latest.gguf", // exact filename (pre-existing)
		"llama_server:abcdef123456",    // hash ID (pre-existing)
	} {
		if !matchesModel(requested, qwen) {
			t.Errorf("matchesModel(%q) = false, want true", requested)
		}
	}

	phi := localruntime.ModelRecord{
		ID:          "llama_server:0123456789ab",
		DisplayName: "phi4-mini-latest",
		Path:        "/models/phi4-mini-latest.gguf",
	}
	for _, requested := range []string{
		"phi4",       // different model — must not fuzzy-match phi4-mini
		"phi4-mini-", // trailing dash typo
		"qwen3",      // prefix of another family
		"",           // empty never matches
	} {
		if matchesModel(requested, phi) {
			t.Errorf("matchesModel(%q) = true, want false", requested)
		}
	}
	if !matchesModel("phi4-mini", phi) {
		t.Error(`matchesModel("phi4-mini") = false, want true (alias for phi4-mini-latest.gguf)`)
	}
}
