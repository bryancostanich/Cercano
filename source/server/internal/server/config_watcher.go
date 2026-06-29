// Package server — out-of-band config-file change detection.
//
// Most config changes come through UpdateConfig (the /config slash command,
// the cercano_config MCP tool, etc.). But a user or another tool may edit
// config.yaml by hand, bypassing the RPC. This watcher catches those edits,
// diffs against the agent's current in-memory config, and replays only the
// changed fields through UpdateConfig — so file edits and RPC edits share one
// apply path (validation, provider rebuild, persistence, broadcast).
//
// It watches the containing DIRECTORY, not the file inode, for the same
// reason the permissions watcher does: editors and `os.WriteFile` commonly
// land via temp-file + rename, which makes a single-file watch go deaf after
// the first swap. A directory watch keeps catching create/rename/write on the
// target name.
//
// Echo loop note: UpdateConfig re-saves the file after applying changes,
// which fires another fsnotify event. On the next pass the diff against
// currentConfig is empty, so UpdateConfig short-circuits at the
// "no changes requested" branch before re-saving — no infinite loop.
package server

import (
	"context"
	"fmt"
	"path/filepath"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"

	"github.com/fsnotify/fsnotify"
)

// StartConfigWatcher begins watching configPath for out-of-band edits and
// replays hot-reloadable field changes through UpdateConfig. Returns an error
// only if the watch can't be established; the caller may treat that as
// non-fatal (the RPC path still works). The goroutine stops when ctx is
// cancelled.
//
// Non-hot-reloadable diffs (port, embedding_model, llama_server.*,
// compaction.*) are logged as warnings telling the user a restart is needed.
func (s *Server) StartConfigWatcher(ctx context.Context, configPath string) error {
	if configPath == "" {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
	if err := w.Add(dir); err != nil {
		w.Close()
		return err
	}
	target := filepath.Clean(configPath)
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Clean(ev.Name) != target {
					continue
				}
				s.reloadConfigFromDisk(ctx)
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
				// Transient watch error — keep going; the RPC path still works.
			}
		}
	}()
	return nil
}

// reloadConfigFromDisk reads configPath, diffs against currentConfig, and
// replays the diff through UpdateConfig. Public-method-private-helper split
// keeps the watcher loop tiny and makes the apply path testable.
func (s *Server) reloadConfigFromDisk(ctx context.Context) {
	newCfg, err := config.Load(s.configPath)
	if err != nil {
		fmt.Printf("ConfigWatcher: failed to reload %s: %v\n", s.configPath, err)
		return
	}

	req := &proto.UpdateConfigRequest{}
	if newCfg.OllamaURL != s.currentConfig.OllamaURL {
		req.OllamaUrl = newCfg.OllamaURL
	}
	if newCfg.LocalModel != s.currentConfig.LocalModel {
		req.LocalModel = newCfg.LocalModel
	}
	if newCfg.LocalRuntime != s.currentConfig.LocalRuntime {
		req.LocalRuntime = newCfg.LocalRuntime
	}
	if newCfg.CloudProvider != s.currentConfig.CloudProvider {
		req.CloudProvider = newCfg.CloudProvider
	}
	if newCfg.CloudModel != s.currentConfig.CloudModel {
		req.CloudModel = newCfg.CloudModel
	}
	if newCfg.CloudAPIKey != s.currentConfig.CloudAPIKey {
		req.CloudApiKey = newCfg.CloudAPIKey
	}
	if newCfg.CloudBaseURL != s.currentConfig.CloudBaseURL {
		req.CloudBaseUrl = newCfg.CloudBaseURL
	}
	if newCfg.LocusMode != s.currentConfig.LocusMode {
		req.LocusMode = newCfg.LocusMode
	}

	// Warn on fields that UpdateConfig doesn't hot-reload. The agent keeps
	// running on the old in-memory values until a restart.
	if newCfg.Port != s.currentConfig.Port {
		fmt.Printf("ConfigWatcher: port change (%q → %q) requires a restart\n", s.currentConfig.Port, newCfg.Port)
	}
	if newCfg.EmbeddingModel != s.currentConfig.EmbeddingModel {
		fmt.Printf("ConfigWatcher: embedding_model change requires a restart\n")
	}

	if _, err := s.UpdateConfig(ctx, req); err != nil {
		fmt.Printf("ConfigWatcher: UpdateConfig rejected file change: %v\n", err)
	}
}
