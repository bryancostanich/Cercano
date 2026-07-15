package localruntime

import "fmt"

// This file defines the two lifecycle state machines that govern local runtime
// bookkeeping: DownloadState (how a model is acquired onto disk) and
// InstanceState (the health of a launched runtime process). Both are typed
// enums with explicit, table-driven legal transitions. Every mutation of these
// fields must go through the guarded setters on the manager, which reject
// illegal transitions rather than silently corrupting state, and notify
// registered Observers so downstream concerns (dashboard chips, auto-start of
// the active runtime once its default model finishes downloading) can react.
//
// Wire boundary: gRPC/agentclient carry these as plain strings. The enum
// String() methods reproduce exactly the historical wire strings, and the
// Parse* helpers read them back (e.g. from a provider's on-disk seed), so the
// proto surface is unchanged.

// DownloadState is the model-acquisition lifecycle.
type DownloadState uint8

const (
	// DownloadNotStarted — the model is known but nothing is on disk.
	DownloadNotStarted DownloadState = iota
	// Downloading — one or more shards are actively being fetched.
	Downloading
	// Downloaded — every shard is present; the model is ready to serve.
	Downloaded
	// DownloadFailed — a fetch exhausted retries; a .part may survive on disk.
	DownloadFailed
	// DownloadCancelled — the user cancelled an in-flight download.
	DownloadCancelled
)

var downloadStateStrings = map[DownloadState]string{
	DownloadNotStarted: "not_downloaded",
	Downloading:        "downloading",
	Downloaded:         "downloaded",
	DownloadFailed:     "failed",
	DownloadCancelled:  "cancelled",
}

// String returns the historical wire string for the state. An unknown value
// (should never occur for an in-range enum) renders as not_downloaded so the
// dashboard degrades to "offer to download" rather than showing garbage.
func (s DownloadState) String() string {
	if str, ok := downloadStateStrings[s]; ok {
		return str
	}
	return "not_downloaded"
}

// ParseDownloadState converts a wire/seed string into a typed state. The bool
// reports whether the string was recognized; an unrecognized value maps to
// DownloadNotStarted so a corrupt seed can't wedge the machine.
func ParseDownloadState(s string) (DownloadState, bool) {
	for state, str := range downloadStateStrings {
		if str == s {
			return state, true
		}
	}
	return DownloadNotStarted, false
}

// downloadTransitions is the legal-move table. A state maps to the set of
// states it may move to. Self-transitions (idempotent re-writes of the same
// state, e.g. a progress tick that re-stores "downloading") are always allowed
// and handled in CanTransitionTo without cluttering the table.
var downloadTransitions = map[DownloadState][]DownloadState{
	DownloadNotStarted: {Downloading},
	Downloading:        {Downloaded, DownloadFailed, DownloadCancelled},
	Downloaded:         {DownloadNotStarted},                // delete
	DownloadFailed:     {Downloading, DownloadNotStarted},   // retry or clear
	DownloadCancelled:  {Downloading, DownloadNotStarted},   // retry or clear
}

// CanTransitionTo reports whether moving from s to next is legal. A no-op
// transition to the same state is always legal (progress updates rewrite the
// current state repeatedly).
func (s DownloadState) CanTransitionTo(next DownloadState) bool {
	if s == next {
		return true
	}
	for _, allowed := range downloadTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// InstanceState is the runtime-process health lifecycle. It replaces the loose
// string constants that previously lived in types.go. The zero value is
// InstanceUnknown so a freshly-zeroed record reads as "unknown", matching the
// prior default.
type InstanceState uint8

const (
	// InstanceUnknown — health has not yet been determined (zero value).
	InstanceUnknown InstanceState = iota
	// InstanceStarting — the process was launched but is not yet ready.
	InstanceStarting
	// InstanceRunning — the process is up; readiness not yet confirmed healthy.
	InstanceRunning
	// InstanceHealthy — the process answered a health probe.
	InstanceHealthy
	// InstanceDegraded — reachable but reporting reduced capability.
	InstanceDegraded
	// InstanceUnhealthy — reachable but failing health checks.
	InstanceUnhealthy
	// InstanceUnreachable — no response on the expected endpoint.
	InstanceUnreachable
	// InstanceStopped — cleanly shut down (or never started).
	InstanceStopped
	// InstanceCrashed — exited unexpectedly with a non-zero code.
	InstanceCrashed
	// InstanceFailed — failed to start or was killed after failing readiness.
	InstanceFailed
)

var instanceStateStrings = map[InstanceState]string{
	InstanceUnknown:     "unknown",
	InstanceStarting:    "starting",
	InstanceRunning:     "running",
	InstanceHealthy:     "healthy",
	InstanceDegraded:    "degraded",
	InstanceUnhealthy:   "unhealthy",
	InstanceUnreachable: "unreachable",
	InstanceStopped:     "stopped",
	InstanceCrashed:     "crashed",
	InstanceFailed:      "failed",
}

// String returns the historical wire string for the instance state.
func (s InstanceState) String() string {
	if str, ok := instanceStateStrings[s]; ok {
		return str
	}
	return "unknown"
}

// ParseInstanceState converts a wire string into a typed state. The bool
// reports recognition; an empty or unknown string maps to InstanceUnknown.
func ParseInstanceState(s string) (InstanceState, bool) {
	for state, str := range instanceStateStrings {
		if str == s {
			return state, true
		}
	}
	return InstanceUnknown, false
}

// instanceTransitions is the legal-move table for the process lifecycle.
//
//	unknown     → any (bootstrap; we may learn any state first)
//	starting    → running, healthy, failed, stopped, crashed, unreachable
//	running     → healthy, degraded, unhealthy, unreachable, stopped, crashed
//	healthy     → degraded, unhealthy, unreachable, stopped, crashed, running
//	degraded    → healthy, unhealthy, unreachable, stopped, crashed
//	unhealthy   → healthy, degraded, unreachable, stopped, crashed
//	unreachable → healthy, degraded, unhealthy, stopped, crashed, running
//	stopped     → starting                     (relaunch)
//	crashed     → starting, stopped            (restart or give up)
//	failed      → starting, stopped            (retry or give up)
var instanceTransitions = map[InstanceState][]InstanceState{
	InstanceUnknown: {
		InstanceStarting, InstanceRunning, InstanceHealthy, InstanceDegraded,
		InstanceUnhealthy, InstanceUnreachable, InstanceStopped,
		InstanceCrashed, InstanceFailed,
	},
	InstanceStarting: {
		InstanceRunning, InstanceHealthy, InstanceFailed, InstanceStopped,
		InstanceCrashed, InstanceUnreachable,
	},
	InstanceRunning: {
		InstanceHealthy, InstanceDegraded, InstanceUnhealthy,
		InstanceUnreachable, InstanceStopped, InstanceCrashed,
	},
	InstanceHealthy: {
		InstanceDegraded, InstanceUnhealthy, InstanceUnreachable,
		InstanceStopped, InstanceCrashed, InstanceRunning,
	},
	InstanceDegraded: {
		InstanceHealthy, InstanceUnhealthy, InstanceUnreachable,
		InstanceStopped, InstanceCrashed,
	},
	InstanceUnhealthy: {
		InstanceHealthy, InstanceDegraded, InstanceUnreachable,
		InstanceStopped, InstanceCrashed,
	},
	InstanceUnreachable: {
		InstanceHealthy, InstanceDegraded, InstanceUnhealthy,
		InstanceStopped, InstanceCrashed, InstanceRunning,
	},
	InstanceStopped:  {InstanceStarting},
	InstanceCrashed:  {InstanceStarting, InstanceStopped},
	InstanceFailed:   {InstanceStarting, InstanceStopped},
}

// CanTransitionTo reports whether moving from s to next is legal. A no-op
// transition to the same state is always legal.
func (s InstanceState) CanTransitionTo(next InstanceState) bool {
	if s == next {
		return true
	}
	for _, allowed := range instanceTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// EndpointStateUnknown is the initial health value for a configured endpoint
// (Ollama, cloud). Endpoint health is a distinct concern from the process and
// download lifecycles above and is carried as a plain string on EndpointRecord.
const EndpointStateUnknown = "unknown"

// DownloadEvent is delivered to Observers when a model's download state
// changes. Prev and Next are the transition endpoints; Err carries the failure
// text when Next is DownloadFailed.
type DownloadEvent struct {
	Model ModelRecord
	Prev  DownloadState
	Next  DownloadState
	Err   string
}

// InstanceEvent is delivered to Observers when an instance's state changes.
type InstanceEvent struct {
	Instance InstanceRecord
	Prev     InstanceState
	Next     InstanceState
}

// Observer receives lifecycle transition notifications. Implementations must
// not block: the manager fires these synchronously while holding no lock, but a
// slow observer still stalls the transitioning goroutine. Fan out to a channel
// if work is heavy. The canonical server-side observer lights the dashboard
// chip on Downloaded and auto-starts the active runtime's sidecar when its
// default model finishes downloading.
type Observer interface {
	OnDownloadStateChange(DownloadEvent)
	OnInstanceStateChange(InstanceEvent)
}

// illegalTransition is returned/logged when a guarded setter is asked to make a
// move the table forbids. It is a programming error, surfaced loudly rather
// than silently corrupting the machine.
type illegalTransition struct {
	kind string
	from string
	to   string
}

func (e illegalTransition) Error() string {
	return fmt.Sprintf("illegal %s transition: %s → %s", e.kind, e.from, e.to)
}
