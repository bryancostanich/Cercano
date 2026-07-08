// Package server — out-of-band permission-mode change detection.
//
// The watcher logic lives in hostsvc/permissions.Broker. This file keeps the
// Server.StartPermissionWatcher method so callers (e.g. main.go) don't need
// to know about the broker directly.
package server

import "context"

// StartPermissionWatcher begins watching permsPath for out-of-band edits and
// broadcasts PermissionModeChanged when the active mode changes. It returns an
// error only if the watch can't be established; the caller may treat that as
// non-fatal (the RPC path still broadcasts). The goroutine stops when ctx is
// cancelled.
func (s *Server) StartPermissionWatcher(ctx context.Context, permsPath string) error {
	if s.permBroker == nil {
		return nil
	}
	return s.permBroker.StartWatcher(ctx, permsPath)
}
