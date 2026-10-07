package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// This file is the bounded, internal core of update preparation: waiting for
// every update-relevant work source to be idle and sealing the existing
// admission gate around it. There is deliberately NO RPC, listener, auth, or
// process-kill behavior here — the design stays internal until an update
// entrypoint is separately integrated.

// errUpdateCoverageIncomplete is the refusal returned when update preparation
// cannot PROVE a work source idle because its startup tracking hook is not
// healthy. Unprovable idle is refused outright: guessing would let background
// work (a runtime model download, a compaction pass, a resume hydration
// worker) race an in-flight update.
var errUpdateCoverageIncomplete = errors.New("update coverage incomplete")

// updateCoverage is the pure observed-field report of which asynchronous work
// sources update preparation can prove idle. Each field carries that source's
// startup binding error verbatim; nil is the only "tracked" signal. There is
// deliberately no derived completeness boolean — any non-nil field means idle
// is unprovable and must be refused. The errors originate from the fixed
// startup bind seams (BindDownloadWork, BindWorkAdmission,
// BindResumeHydrationWork) and never carry secret material.
type updateCoverage struct {
	runtimeDownloads  error
	compaction        error
	resumeHydration   error
	credentialRefresh error
}

// setInjectedUpdateTrackingErr atomically records an (unbound) tracking
// error for one coverage source. Used by the startup bind seams and tests.
// The lock (Server.injectMu) also guards these reads for tests that mutate
// coverage while a waiter runs.
func (s *Server) setInjectedUpdateTrackingErr(which string, err error) {
	s.injectMu.Lock()
	defer s.injectMu.Unlock()
	switch which {
	case "runtime":
		s.updateRuntimeTrackingErr = err
	case "compaction":
		s.updateCompactionTrackingErr = err
	case "hydration":
		s.updateHydrationTrackingErr = err
	case "credential":
		s.updateCredentialTrackingErr = err
	}
}

// updateCoverageSnapshot returns the observed startup coverage fields the
// Server recorded when its work-tracking hooks were bound.
func (s *Server) updateCoverageSnapshot() updateCoverage {
	s.injectMu.RLock()
	defer s.injectMu.RUnlock()
	return updateCoverage{
		runtimeDownloads:  s.updateRuntimeTrackingErr,
		compaction:        s.updateCompactionTrackingErr,
		resumeHydration:   s.updateHydrationTrackingErr,
		credentialRefresh: s.updateCredentialTrackingErr,
	}
}

// refusal reports nil when every work source is tracked, otherwise a clear
// error naming each untracked source.
func (c updateCoverage) refusal() error {
	var missing []string
	if c.runtimeDownloads != nil {
		missing = append(missing, "runtime model download lifetimes untracked: "+c.runtimeDownloads.Error())
	}
	if c.compaction != nil {
		missing = append(missing, "background compaction lifetimes untracked: "+c.compaction.Error())
	}
	if c.resumeHydration != nil {
		missing = append(missing, "resume hydration lifetimes untracked: "+c.resumeHydration.Error())
	}
	if c.credentialRefresh != nil {
		missing = append(missing, "credential refresh lifetimes untracked: "+c.credentialRefresh.Error())
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w (%s)", errUpdateCoverageIncomplete, strings.Join(missing, "; "))
}

// waitForUpdateIdle waits until every update-relevant work source is idle and
// seals update admission, returning the idempotent release that unseals it.
// It reuses the existing updateWork gate — no new admission framework.
//
// Coverage of the asynchronous sources (runtime model downloads, background
// compaction, resume hydration) is proven from the observed startup fields,
// checked BEFORE waiting — fail fast, no pause taken — and AGAIN after the
// seal, because a hook that fails or rebinds while we waited means background
// work could be invisible to the gate. Unprovable coverage is a clear
// incomplete-coverage refusal, never a guess; a post-seal refusal releases the
// seal before returning so no pause leaks.
//
// Ownership: the returned release is idempotent (safe to defer and call again),
// and the owner MUST call it — typically via defer. The ctx passed here
// governs only the wait itself: once this method has returned, canceling that
// ctx does NOT lift the seal, so a caller that abandoned the release would
// strand the server with new update-relevant work refused. Until release
// runs, new update-relevant work is refused. No real shutdown is performed
// here; cancelling work is never part of the contract.
func (s *Server) waitForUpdateIdle(ctx context.Context) (func(), error) {
	if err := s.updateCoverageSnapshot().refusal(); err != nil {
		return nil, err
	}
	release, err := s.updateWork.pauseWhenIdle(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.updateCoverageSnapshot().refusal(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// prepareUpdateIdle is the callback form of waitForUpdateIdle for internal
// callers that want to be notified the moment the gate is sealed (a future
// update entrypoint can report readiness from inside the callback before
// starting its long phase). The ready callback receives the idempotent
// release — or (nil, err) on refusal — and owns the sealed gate for the
// duration of its call: any release the callback does not invoke is invoked
// when it returns or panics, so a failure to notify ready can never leak the
// seal. No real shutdown happens here; a caller that needs the gate to
// outlive the callback must call waitForUpdateIdle directly and defer its
// release itself.
func (s *Server) prepareUpdateIdle(ctx context.Context, ready func(release func(), err error)) {
	release, err := s.waitForUpdateIdle(ctx)
	if release == nil {
		if ready != nil {
			ready(nil, err)
		}
		return
	}
	defer release()
	if ready == nil {
		return
	}
	ready(release, err)
}
