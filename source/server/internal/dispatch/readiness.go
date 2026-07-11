package dispatch

import (
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/pkg/config"
)

// OpenModelReady reports whether the open runtime can serve a request right
// now. For the bundled llama-server runtime that means the configured
// default-model GGUF actually exists on disk — this is the routing-readiness
// contract behind "cloud covers the gap": while that file is still downloading
// (e.g. right after setup selects an open model set), the open tier registers
// as absent so Select crosses to cloud, then serves locally the moment the
// file lands. A not-yet-present model would otherwise be picked and fail at
// load time instead of falling back.
//
// Non-llama-server open runtimes (Ollama) manage their own model presence, so
// they are treated as ready and left to their own resolution.
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
