package localruntime

import (
	"os"
	"path/filepath"
	"strings"
)

// MatchesModel reports whether a requested model identifier refers to the
// given model record. It is the single matching authority shared by the
// llama-server provider (resolve-and-spawn) and the inference engine
// (find-a-warm-instance). The two sides must agree: a name the provider can
// resolve but the engine cannot match means every request misses the warm
// instance and spawns a fresh llama-server — each one wiring the full model
// into GPU memory until the machine runs out of physical RAM.
//
// Accepted forms: the record ID, display name, filename, full path, and a
// bare Ollama-style name (or name:latest) whose "-latest" file stem matches
// (qwen3-coder-next → qwen3-coder-next-latest.gguf). Configs and model tiers
// written against the Ollama runtime use bare names; without the alias the
// same config silently stops resolving after a switch to llama_server.
// Exact-only otherwise: "phi4" does NOT match phi4-mini-latest.gguf —
// different model.
func MatchesModel(requested string, model ModelRecord) bool {
	if requested == "" {
		return false
	}
	expanded, _ := expandModelPath(requested)
	if requested == model.ID ||
		requested == model.DisplayName ||
		requested == filepath.Base(model.Path) ||
		expanded == model.Path {
		return true
	}
	stem := strings.TrimSuffix(filepath.Base(model.Path), filepath.Ext(model.Path))
	name := strings.TrimSuffix(requested, ":latest")
	return stem == name || stem == name+"-latest"
}

func expandModelPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}
