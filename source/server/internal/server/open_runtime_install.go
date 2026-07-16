// Package server — InstallOpenRuntime RPC handler.
//
// This is the server-side of the "user clicked Install now in the CLI's
// llama-server modal" flow. The client opens a stream, the server shells out
// to `brew install llama.cpp` (or the platform equivalent), each stdout/
// stderr line becomes an InstallProgress frame, and a terminal frame with
// done=true carries the outcome.
//
// On success the handler re-runs headless detection and broadcasts a
// OpenRuntimeStatusChanged event with the freshly-populated Binary +
// DefaultModel — the client's modal listens for that on its ambient
// SubscribeEvents stream, dismisses itself, and lets the status chip turn
// green. Client doesn't need to poll.
package server

import (
	"context"
	"fmt"

	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// InstallOpenRuntime implements proto.AgentServer. Only "llama_server" is
// supported today; other runtime names return an InvalidArgument-shaped
// terminal frame rather than an RPC error so the client's log pane stays the
// single source of truth for user-visible outcomes.
func (s *Server) InstallOpenRuntime(req *proto.InstallOpenRuntimeRequest, stream proto.Agent_InstallOpenRuntimeServer) error {
	ctx := stream.Context()

	if req.GetRuntime() != "llama_server" {
		return sendTerminalFrame(stream, false, fmt.Sprintf("install not supported for runtime %q (only llama_server)", req.GetRuntime()))
	}

	// Sink forwards each line to the stream as a done=false frame. A send
	// error (client hung up) cancels the sink's write path via the returned
	// error — Install will still finish its subprocess (subprocess is torn
	// down by ctx cancel from the stream close), but the sink stops trying
	// to write to a broken stream.
	sink := func(line string) {
		_ = stream.Send(&proto.InstallProgress{Line: line})
	}

	if err := llamaserver.Install(ctx, sink); err != nil {
		return sendTerminalFrame(stream, false, err.Error())
	}

	// Install succeeded — re-run detection so the freshly-installed binary
	// (and any GGUFs the user already had in ~/.cercano/models) get picked
	// up into the config, then broadcast the ok=true status so the CLI
	// modal / chip settle into the "ready" state.
	cfgCopy := s.cfgSvc.Get()
	if err := llamaserver.Detect(ctx, &cfgCopy.LlamaServer); err != nil {
		// Write back the partial detect result so the status shows it.
		s.cfgSvc.Set(cfgCopy)
		s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatusFromDetectError(cfgCopy, err))
		return sendTerminalFrame(stream, false, fmt.Sprintf("install completed but detection still fails: %v", err))
	}
	s.cfgSvc.Set(cfgCopy)
	s.cfgSvc.Persist()
	s.broadcastOpenRuntimeStatus(s.openRuntimeStatus(ctx, cfgCopy, "llama_server"))
	return sendTerminalFrame(stream, true, "")
}

// sendTerminalFrame emits the closing InstallProgress frame with done=true.
// Returns whatever error stream.Send produces; the caller returns it so gRPC
// closes the stream cleanly.
func sendTerminalFrame(stream proto.Agent_InstallOpenRuntimeServer, ok bool, errMsg string) error {
	return stream.Send(&proto.InstallProgress{Done: true, Ok: ok, Error: errMsg})
}

// buildOpenRuntimeStatusFromDetectError formats an install-completed-but-detect-
// still-fails diagnostic directly from a llama-server DetectError. It exists
// only for that post-install branch, where we have the raw error in hand and
// want its specific messaging (rather than re-running detection through the
// readiness path).
func buildOpenRuntimeStatusFromDetectError(cfg config.Config, err error) *proto.OpenRuntimeStatus {
	r := openRuntimeReadiness{
		State:        readyMissing,
		Missing:      "model",
		Message:      err.Error(),
		Binary:       cfg.LlamaServer.Binary,
		DefaultModel: cfg.LlamaServer.DefaultModel,
	}
	if de, ok := err.(*llamaserver.DetectError); ok {
		r.Missing = de.Missing
		r.SuggestedCommand = de.SuggestedCommand()
	}
	return openRuntimeStatusFrom("llama_server", r)
}

// GetOpenRuntimeStatus implements proto.AgentServer — pull-side snapshot
// for CLI startup. Re-runs headless detection against the currently
// selected local runtime and returns the same OpenRuntimeStatus shape
// pushed by OpenRuntimeStatusChanged. Cheap (a couple filesystem checks)
// so no caching — always fresh.
func (s *Server) GetOpenRuntimeStatus(ctx context.Context, req *proto.GetOpenRuntimeStatusRequest) (*proto.GetOpenRuntimeStatusResponse, error) {
	cfg := s.cfgSvc.Get()
	// Explicit request overrides the currently-selected runtime — the CLI's
	// settings-page gate uses this to probe a runtime it's about to switch
	// to. Empty falls back to what's currently active. Every runtime goes
	// through the same runtime-agnostic readiness path (openRuntimeStatus):
	// ollama → ready, llama_server → binary+model detect, mistralrs → model
	// download-state. This is the pull-side twin of the push broadcast, so a
	// cold-started or reconnecting CLI gets the SAME chip the switch emitted
	// (fixes the old `runtime != "llama_server"` gate that returned an
	// unconditional ok=true for mistralrs and hid the chip).
	return &proto.GetOpenRuntimeStatusResponse{
		Status: s.openRuntimeStatus(ctx, cfg, req.GetRuntime()),
	}, nil
}
