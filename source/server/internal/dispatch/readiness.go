package dispatch

import (
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/pkg/config"
)

// OpenModelReady reports whether the configured open runtime can serve its
// legacy default model. Prefer OpenModelReadyFor when the caller already has the
// effective tier model (override else catalog default); this wrapper is kept for
// older tests/callers that still reason only from Config.
func OpenModelReady(c config.Config) bool {
	if c.OpenRuntime != "llama_server" {
		return true
	}
	return OpenModelReadyFor(c, strings.TrimSpace(c.LlamaServer.DefaultModel))
}

// OpenModelReadyFor reports whether the open runtime can serve effectiveModel
// right now. It is the routing-readiness contract behind "cloud covers the
// gap": when the open tier can't serve yet, Select crosses to cloud and then
// serves locally the moment the model lands, instead of picking a not-yet-
// present model that fails at load.
//
// This is a CONFIG-LEVEL gate — it deliberately has no access to the runtime
// manager's inventory (dispatch must not depend on it). So it only blocks when
// config can prove absence:
//
//   - disabled llama_server → not ready;
//   - empty effective model → not ready;
//   - filesystem-path GGUF → stat it;
//   - catalog IDs / model names (llama_server:catalog:..., mistralrs names,
//     ollama names) → config cannot prove presence, so do not block routing;
//     authoritative inventory/ensure/warm flows handle actual availability.
func OpenModelReadyFor(c config.Config, effectiveModel string) bool {
	if c.OpenRuntime != "llama_server" {
		return effectiveModel != ""
	}
	if !c.LlamaServer.Enabled {
		return false
	}
	model := strings.TrimSpace(effectiveModel)
	if model == "" {
		return false
	}
	if !looksLikePath(model) {
		return true
	}
	path := model
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}
