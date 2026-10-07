package brewrestart

import (
	"context"
	"fmt"
	"net/netip"
)

// restartOps separates lifecycle effects so sequencing and refusal paths can
// be tested without touching a live agent. Production adapters use kernel
// inspection, the shared CLI launch lock and the ShutdownAgentWhenIdle
// safe-stop RPC (never the legacy ShutdownAgent bounce; see safestop.go).
type restartOps struct {
	source    processSource
	capture   func(Identity) (LaunchState, error)
	preflight func(string, LaunchState) error
	lock      func(context.Context, LaunchState) (func(), error)
	shutdown  func(context.Context, Identity, netip.AddrPort) error
	waitExit  func(context.Context, Identity) error
	start     func(string, LaunchState) (Identity, error)
	ready     func(context.Context, Identity, netip.AddrPort) error
}

func coordinateRestart(ctx context.Context, ops restartOps, newExecutable string, uid uint32, endpoint netip.AddrPort) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	owner, err := discover(ops.source, newExecutable, uid, endpoint)
	if err != nil || owner == nil {
		return false, err
	}
	state, err := ops.capture(*owner)
	if err != nil {
		return false, err
	}
	if !state.Identity.SameProcess(*owner) {
		return false, fmt.Errorf("captured launch state belongs to a different process")
	}
	// All local validation must succeed before the destructive boundary.
	if err := ops.preflight(newExecutable, state); err != nil {
		return false, err
	}
	release, err := ops.lock(ctx, state)
	if err != nil {
		return false, err
	}
	defer release()
	// A reconnecting client or another upgrade may have won the launch lock.
	// Never apply a stale snapshot to whichever process happens to listen now.
	current, err := discover(ops.source, newExecutable, uid, endpoint)
	if err != nil {
		return false, err
	}
	if current == nil || !owner.SameProcess(*current) {
		return false, fmt.Errorf("agent changed while waiting for launch lock; retry the restart")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ops.shutdown(ctx, *owner, endpoint); err != nil {
		return false, fmt.Errorf("shutdown request failed; no replacement started: %w", err)
	}
	if err := ops.waitExit(ctx, *owner); err != nil {
		return false, fmt.Errorf("old agent has not confirmed exit; no replacement started: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("agent stopped but restart deadline expired: %w", err)
	}
	replacement, err := ops.start(newExecutable, state)
	if err != nil {
		return false, fmt.Errorf("agent stopped but replacement could not start; start cercano manually: %w", err)
	}
	if err := ops.ready(ctx, replacement, endpoint); err != nil {
		return false, fmt.Errorf("replacement started but readiness was not confirmed; check agent logs before retrying: %w", err)
	}
	return true, nil
}
