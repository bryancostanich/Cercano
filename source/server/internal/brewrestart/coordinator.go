package brewrestart

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"time"
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

// stopConfirmation is what a fresh bounded LOCAL inspection can prove about
// the verified agent after a safe stop ended ambiguously. It never guesses:
// each value restates only what the kernel inspection actually showed.
type stopConfirmation int

const (
	// stopConfirmedGone: the verified PID no longer exists (ESRCH). The
	// agent positively exited, so the existing restart may continue under
	// the held launch lock.
	stopConfirmedGone stopConfirmation = iota + 1
	// stopSameAgentAlive: the exact same process (same PID, user, start
	// time, executable) is still alive. The stop did not complete; nothing
	// may be forced and the outcome is reported as unconfirmed.
	stopSameAgentAlive
	// stopUnknown: the inspection failed, the PID was reused by a different
	// process, or a foreign identity answered. The restart is refused; no
	// PID-reuse proof beyond the existing inspection is invented.
	stopUnknown
)

// uncertainStopInspectBound caps the fresh local inspection that resolves an
// ambiguous safe stop. The overall restart deadline still bounds it, so a
// post-install deadline that already expired prohibits recovery.
const uncertainStopInspectBound = 2 * time.Second

// confirmStopAfterUncertainty resolves an ambiguous safe-stop ending with a
// fresh bounded LOCAL inspection of the verified identity — never another RPC
// to the possibly-dying agent, never a forced stop. It polls briefly so an
// agent that committed the stop and is draining can be observed to exit:
//
//   - the PID is gone (ESRCH) => stopConfirmedGone: the agent positively
//     exited and the existing restart may continue under the held lock;
//   - the exact same process is still alive => stopSameAgentAlive: nothing
//     is forced and the caller reports the unconfirmed state;
//   - inspection failure, PID reuse or a foreign identity => stopUnknown:
//     the restart is refused rather than guessing an exit.
//
// If the bounded post-install deadline prohibits this recovery (it already
// expired when the ambiguity surfaced), the outcome is stopUnknown with the
// deadline error: the caller reports the unconfirmed state — never "left
// running".
func confirmStopAfterUncertainty(ctx context.Context, source processSource, id Identity) (stopConfirmation, error) {
	if err := ctx.Err(); err != nil {
		// The post-install deadline prohibits recovery. Do not act or guess
		// after the caller's deadline; report the unconfirmed state.
		return stopUnknown, err
	}
	bound := time.NewTimer(uncertainStopInspectBound)
	defer bound.Stop()
	tick := time.NewTimer(25 * time.Millisecond)
	defer tick.Stop()
	sameAlive := false
	for {
		now, err := source.Inspect(id.PID)
		switch {
		case errors.Is(err, syscall.ESRCH):
			return stopConfirmedGone, nil
		case err == nil && !id.SameProcess(now):
			// A different process now holds the PID. The existing
			// inspection cannot prove the original exited, so refuse
			// instead of inventing a PID-reuse proof.
			return stopUnknown, fmt.Errorf("PID %d now identifies a different process", id.PID)
		case err != nil && !errors.Is(err, syscall.EAGAIN):
			return stopUnknown, err
		case err == nil:
			sameAlive = true
		}
		// err == nil && same process, or a transient EAGAIN snapshot: probe
		// again after a short pause until the bound or the overall deadline.
		if !tick.Stop() {
			select {
			case <-tick.C:
			default:
			}
		}
		tick.Reset(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if sameAlive {
				return stopSameAgentAlive, ctx.Err()
			}
			return stopUnknown, ctx.Err()
		case <-bound.C:
			if sameAlive {
				return stopSameAgentAlive, nil
			}
			return stopUnknown, fmt.Errorf("local inspection could not observe the agent within %s", uncertainStopInspectBound)
		case <-tick.C:
		}
	}
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
		if !isSafeStopUncertain(err) {
			return false, fmt.Errorf("shutdown request failed; no replacement started: %w", err)
		}
		// A deadline or transport ending does not prove the agent was busy or
		// left alive: the stop may have committed with the confirmation lost.
		// Resolve it with a fresh bounded local inspection of the VERIFIED
		// identity (no new RPC, no forced stop, no legacy fallback), and
		// only a positive observation may continue the existing restart.
		confirmation, inspectErr := confirmStopAfterUncertainty(ctx, ops.source, *owner)
		switch confirmation {
		case stopSameAgentAlive:
			return false, fmt.Errorf("%w: shutdown ended ambiguously (%v); the same agent process is still running, was not stopped, and no replacement was started",
				ErrSafeStopUncertain, err)
		case stopUnknown:
			// Includes the case where the bounded post-install deadline
			// prohibited recovery: report the unconfirmed state, never
			// "left running".
			return false, fmt.Errorf("%w: shutdown ended ambiguously (%v); the agent's state could not be confirmed and no replacement was started: %v",
				ErrSafeStopUncertain, err, inspectErr)
		}
		// stopConfirmedGone: the verified agent positively exited. Fall
		// through to the existing exit wait and restart under the held lock.
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
