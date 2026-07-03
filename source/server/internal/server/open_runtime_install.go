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
	// up into currentConfig, then broadcast the ok=true status so the CLI
	// modal / chip settle into the "ready" state.
	s.cfgMu.Lock()
	cfgCopy := s.currentConfig
	if err := llamaserver.Detect(ctx, &cfgCopy.LlamaServer); err != nil {
		s.currentConfig = cfgCopy
		s.cfgMu.Unlock()
		s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatusFromDetectError(cfgCopy, err))
		return sendTerminalFrame(stream, false, fmt.Sprintf("install completed but detection still fails: %v", err))
	}
	s.currentConfig = cfgCopy
	s.cfgMu.Unlock()
	if s.configPath != "" {
		_ = config.Save(cfgCopy, s.configPath)
	}
	s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatus("llama_server", cfgCopy, nil))
	return sendTerminalFrame(stream, true, "")
}

// sendTerminalFrame emits the closing InstallProgress frame with done=true.
// Returns whatever error stream.Send produces; the caller returns it so gRPC
// closes the stream cleanly.
func sendTerminalFrame(stream proto.Agent_InstallOpenRuntimeServer, ok bool, errMsg string) error {
	return stream.Send(&proto.InstallProgress{Done: true, Ok: ok, Error: errMsg})
}

// buildOpenRuntimeStatusFromDetectError is a narrow wrapper around
// buildOpenRuntimeStatus that only type-asserts once so callers don't have
// to. Kept private to this file since it exists only for the post-install
// detection-still-fails branch.
func buildOpenRuntimeStatusFromDetectError(cfg config.Config, err error) *proto.OpenRuntimeStatus {
	de, _ := err.(*llamaserver.DetectError)
	return buildOpenRuntimeStatus("llama_server", cfg, de)
}

// GetOpenRuntimeStatus implements proto.AgentServer — pull-side snapshot
// for CLI startup. Re-runs headless detection against the currently
// selected local runtime and returns the same OpenRuntimeStatus shape
// pushed by OpenRuntimeStatusChanged. Cheap (a couple filesystem checks)
// so no caching — always fresh.
func (s *Server) GetOpenRuntimeStatus(_ context.Context, req *proto.GetOpenRuntimeStatusRequest) (*proto.GetOpenRuntimeStatusResponse, error) {
	s.cfgMu.RLock()
	cfg := s.currentConfig
	s.cfgMu.RUnlock()
	// Explicit request overrides the currently-selected runtime — the CLI's
	// settings-page gate uses this to probe a runtime it's about to switch
	// to. Empty falls back to what's currently active.
	runtime := req.GetRuntime()
	if runtime == "" {
		runtime = cfg.OpenRuntime
	}
	if runtime == "" {
		runtime = "ollama"
	}
	if runtime != "llama_server" {
		// Ollama and future runtimes don't need setup surfacing today — we
		// return an ok=true snapshot so the client hides the chip.
		return &proto.GetOpenRuntimeStatusResponse{
			Status: buildOpenRuntimeStatus(runtime, cfg, nil),
		}, nil
	}
	llamaCfg := cfg.LlamaServer
	detectErr := llamaserver.Detect(context.Background(), &llamaCfg)
	var de *llamaserver.DetectError
	if detectErr != nil {
		de, _ = detectErr.(*llamaserver.DetectError)
	}
	// Detect populated llamaCfg's Binary/DefaultModel in place — reflect
	// those into the snapshot so the client sees the resolved fields.
	cfg.LlamaServer = llamaCfg
	return &proto.GetOpenRuntimeStatusResponse{
		Status: buildOpenRuntimeStatus("llama_server", cfg, de),
	}, nil
}
