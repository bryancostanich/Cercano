package dispatch

import (
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/pkg/config"
)

// OpenModelReady reports whether the open runtime can serve a request right
// now. It is the routing-readiness contract behind "cloud covers the gap":
// when the open tier can't serve yet, Select crosses to cloud and then serves
// locally the moment the model lands, instead of picking a not-yet-present
// model that fails at load.
//
// This is a CONFIG-LEVEL gate — it deliberately has no access to the runtime
// manager's inventory (dispatch must not depend on it). So it can only answer
// for runtimes whose readiness is determinable from config alone:
//
//   - llama_server: the default-model GGUF is a filesystem PATH, so we stat it;
//     a still-downloading .part isn't the final file, so stat fails → not ready.
//   - ollama / mistralrs and other model-download runtimes: their default is a
//     model NAME/ID, not a path, so config can't prove presence here. We report
//     ready (don't block routing) and let the authoritative, inventory-aware
//     server-side readiness (open_runtime_readiness.go) drive the chip and the
//     auto-download/warm flow. Blocking routing on an unprovable state would
//     wrongly force cloud even when the model is present.
func OpenModelReady(c config.Config) bool {
	if c.OpenRuntime != "llama_server" {
		return true
	}
	if !c.LlamaServer.Enabled {
		return false
	}
	path := strings.TrimSpace(c.LlamaServer.DefaultModel)
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
