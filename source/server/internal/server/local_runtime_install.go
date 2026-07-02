// Package server — InstallLocalRuntime RPC handler.
//
// This is the server-side of the "user clicked Install now in the CLI's
// llama-server modal" flow. The client opens a stream, the server shells out
// to `brew install llama.cpp` (or the platform equivalent), each stdout/
// stderr line becomes an InstallProgress frame, and a terminal frame with
// done=true carries the outcome.
//
// On success the handler re-runs headless detection and broadcasts a
// LocalRuntimeStatusChanged event with the freshly-populated Binary +
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

// InstallLocalRuntime implements proto.AgentServer. Only "llama_server" is
// supported today; other runtime names return an InvalidArgument-shaped
// terminal frame rather than an RPC error so the client's log pane stays the
// single source of truth for user-visible outcomes.
func (s *Server) InstallLocalRuntime(req *proto.InstallLocalRuntimeRequest, stream proto.Agent_InstallLocalRuntimeServer) error {
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
		s.broadcastLocalRuntimeStatus(buildLocalRuntimeStatusFromDetectError(cfgCopy, err))
		return sendTerminalFrame(stream, false, fmt.Sprintf("install completed but detection still fails: %v", err))
	}
	s.currentConfig = cfgCopy
	s.cfgMu.Unlock()
	if s.configPath != "" {
		_ = config.Save(cfgCopy, s.configPath)
	}
	s.broadcastLocalRuntimeStatus(buildLocalRuntimeStatus("llama_server", cfgCopy, nil))
	return sendTerminalFrame(stream, true, "")
}

// sendTerminalFrame emits the closing InstallProgress frame with done=true.
// Returns whatever error stream.Send produces; the caller returns it so gRPC
// closes the stream cleanly.
func sendTerminalFrame(stream proto.Agent_InstallLocalRuntimeServer, ok bool, errMsg string) error {
	return stream.Send(&proto.InstallProgress{Done: true, Ok: ok, Error: errMsg})
}

// buildLocalRuntimeStatusFromDetectError is a narrow wrapper around
// buildLocalRuntimeStatus that only type-asserts once so callers don't have
// to. Kept private to this file since it exists only for the post-install
// detection-still-fails branch.
func buildLocalRuntimeStatusFromDetectError(cfg config.Config, err error) *proto.LocalRuntimeStatus {
	de, _ := err.(*llamaserver.DetectError)
	return buildLocalRuntimeStatus("llama_server", cfg, de)
}

// GetLocalRuntimeStatus implements proto.AgentServer — pull-side snapshot
// for CLI startup. Re-runs headless detection against the currently
// selected local runtime and returns the same LocalRuntimeStatus shape
// pushed by LocalRuntimeStatusChanged. Cheap (a couple filesystem checks)
// so no caching — always fresh.
func (s *Server) GetLocalRuntimeStatus(_ context.Context, _ *proto.GetLocalRuntimeStatusRequest) (*proto.GetLocalRuntimeStatusResponse, error) {
	s.cfgMu.RLock()
	cfg := s.currentConfig
	s.cfgMu.RUnlock()
	runtime := cfg.LocalRuntime
	if runtime == "" {
		runtime = "ollama"
	}
	if runtime != "llama_server" {
		// Ollama and future runtimes don't need setup surfacing today — we
		// return an ok=true snapshot so the client hides the chip.
		return &proto.GetLocalRuntimeStatusResponse{
			Status: buildLocalRuntimeStatus(runtime, cfg, nil),
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
	return &proto.GetLocalRuntimeStatusResponse{
		Status: buildLocalRuntimeStatus("llama_server", cfg, de),
	}, nil
}
