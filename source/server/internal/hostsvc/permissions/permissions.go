// Package permissions provides the permission broker — the single source of
// truth for the active permission mode, pending tool-call decisions, and the
// file watcher that catches out-of-band edits to permissions.yaml.
//
// The Broker interface is what the front door (Server) depends on; the
// concrete broker type holds the agent.PermissionStore and
// agent.PendingDecisions and owns their synchronization.
package permissions

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"cercano/source/server/internal/agent"

	"github.com/fsnotify/fsnotify"
)

// Broker is the interface the front door (Server) depends on. All
// permission-related call sites go through this interface.
type Broker interface {
	// Mode returns the active permission mode.
	Mode() agent.PermissionMode
	// SetMode persists the new mode and broadcasts the change.
	SetMode(m agent.PermissionMode) error
	// AddMCPAllow persists an always-allow for the named MCP tool.
	AddMCPAllow(name string) error
	// HasPending reports whether there is a live PendingDecisions barrier.
	// The tool-loop requester uses this to skip the send+wait path when no
	// operator can answer.
	HasPending() bool
	// Wait blocks until the operator resolves the named tool-use ID within
	// the given conversation. The barrier is keyed per conversation so two
	// live conversations with colliding tool-use IDs stay isolated.
	Wait(ctx context.Context, conversationID, toolUseID string) (agent.Decision, error)
	// Resolve posts a human decision for the named tool-use ID within the
	// given conversation. See Wait for why the conversation scope matters.
	Resolve(conversationID, toolUseID string, d agent.Decision) bool
	// Store returns the underlying PermissionStore (required by RunToolLoop).
	Store() *agent.PermissionStore
	// StartWatcher watches permsPath for out-of-band edits and broadcasts on
	// change. Returns an error only if the watch cannot be established; the
	// caller may treat that as non-fatal (the RPC path still broadcasts).
	// The goroutine stops when ctx is cancelled.
	StartWatcher(ctx context.Context, permsPath string) error
}

// broker is the concrete implementation of Broker.
type broker struct {
	store   *agent.PermissionStore
	pending *agent.PendingDecisions

	permBcastMu   sync.Mutex
	lastBcastMode string
	broadcastFn   func(mode string) // called after a deduped mode change; may be nil
}

// New constructs a Broker wrapping the given store and pending-decisions
// barrier. broadcast is called (deduped by value) whenever the active
// permission mode changes; pass nil if no push is needed (e.g. tests).
func New(store *agent.PermissionStore, pending *agent.PendingDecisions, broadcast func(mode string)) Broker {
	return &broker{
		store:       store,
		pending:     pending,
		broadcastFn: broadcast,
	}
}

func (p *broker) Mode() agent.PermissionMode {
	if p.store == nil {
		return agent.ModePermissive
	}
	return p.store.Mode()
}

func (p *broker) SetMode(m agent.PermissionMode) error {
	if p.store == nil {
		return fmt.Errorf("permission store not configured")
	}
	if err := p.store.SetMode(m); err != nil {
		return err
	}
	p.broadcastMode(string(m))
	return nil
}

func (p *broker) AddMCPAllow(name string) error {
	if p.store == nil {
		return nil // silent no-op when no store (matches pre-refactor behavior)
	}
	return p.store.AddMCPAllow(name)
}

func (p *broker) HasPending() bool {
	return p.pending != nil
}

func (p *broker) Wait(ctx context.Context, conversationID, toolUseID string) (agent.Decision, error) {
	if p.pending == nil {
		return agent.Decision{}, nil // no barrier → zero decision (matches pre-refactor)
	}
	return p.pending.Wait(ctx, conversationID, toolUseID)
}

func (p *broker) Resolve(conversationID, toolUseID string, d agent.Decision) bool {
	if p.pending == nil {
		return false
	}
	return p.pending.Resolve(conversationID, toolUseID, d)
}

func (p *broker) Store() *agent.PermissionStore {
	return p.store
}

// StartWatcher watches permsPath for out-of-band edits and broadcasts
// PermissionModeChanged when the active mode changes. It watches the
// containing directory (not the file inode) so atomic-write editors and
// rename-over tools keep triggering. Returns an error only if the watch
// cannot be established. The goroutine stops when ctx is cancelled.
func (p *broker) StartWatcher(ctx context.Context, permsPath string) error {
	if p.store == nil {
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
	p.permBcastMu.Lock()
	p.lastBcastMode = string(p.store.Mode())
	p.permBcastMu.Unlock()

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
				p.broadcastMode(string(p.store.Mode()))
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

// broadcastMode pushes a mode-changed notification deduped by value. Both
// the SetMode (RPC) path and StartWatcher (file-edit) path call through
// here; only a mode that differs from the last broadcast propagates.
func (p *broker) broadcastMode(mode string) {
	if p.broadcastFn == nil {
		return
	}
	p.permBcastMu.Lock()
	if mode == p.lastBcastMode {
		p.permBcastMu.Unlock()
		return
	}
	p.lastBcastMode = mode
	p.permBcastMu.Unlock()
	p.broadcastFn(mode)
}
