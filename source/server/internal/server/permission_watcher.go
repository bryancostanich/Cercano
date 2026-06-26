// Package server — out-of-band permission-mode change detection.
//
// Most mode changes come through SetPermissionMode (a /strict-style command),
// which broadcasts directly. But a human or a tool may edit permissions.yaml
// by hand, bypassing the RPC entirely. This watcher catches those edits and
// pushes the change to clients so nothing has to poll.
//
// It watches the containing DIRECTORY, not the file inode: editors and tools
// commonly write a temp file and rename it over the target (atomic write),
// which makes a single-file watch go deaf after the first swap. A directory
// watch keeps catching create/rename/write on the target name.
package server

import (
	"context"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// StartPermissionWatcher begins watching permsPath for out-of-band edits and
// broadcasts PermissionModeChanged when the active mode changes. It returns an
// error only if the watch can't be established; the caller may treat that as
// non-fatal (the RPC path still broadcasts). The goroutine stops when ctx is
// cancelled.
func (s *Server) StartPermissionWatcher(ctx context.Context, permsPath string) error {
	if s.permStore == nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(permsPath)
	if err := w.Add(dir); err != nil {
		w.Close()
		return err
	}
	// Seed the dedupe baseline with the current (boot) mode so the first
	// real edit broadcasts but the watcher coming online does not.
	s.permBcastMu.Lock()
	s.lastBcastMode = string(s.permStore.Mode())
	s.permBcastMu.Unlock()

	target := filepath.Clean(permsPath)
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
					continue // some other file in the dir
				}
				// Re-read the authoritative mode and broadcast (deduped).
				s.broadcastPermissionMode(string(s.permStore.Mode()))
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
				// Transient watch error — keep going; the gate stays correct
				// regardless because Mode() reads the file on every decision.
			}
		}
	}()
	return nil
}
