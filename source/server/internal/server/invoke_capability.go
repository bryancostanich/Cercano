package server

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/pkg/proto"
)

// InvokeCapability resolves a capability by canonical name and runs it. Used by
// the MCP adapter so every cercano_<name> tool forwards to one generic RPC.
// Errors are surfaced in the response (IsError/Error) rather than as gRPC
// errors so the caller can render them inline.
func (s *Server) InvokeCapability(ctx context.Context, req *proto.InvokeCapabilityRequest) (*proto.InvokeCapabilityResponse, error) {
	if s.capRegistry == nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: "capability registry not initialized"}, nil
	}
	cap, ok := s.capRegistry.Get(req.GetName())
	if !ok {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: fmt.Sprintf("unknown capability %q", req.GetName())}, nil
	}
	call := &capabilities.Call{
		Args:    req.GetArgsJson(),
		WorkDir: req.GetWorkDir(),
		// Allow-all: the host (MCP layer) is responsible for permission gating.
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
		Svc:               s.capRegistry.Services(),
	}
	res, err := cap.Execute(ctx, call)
	if err != nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: err.Error()}, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: "marshal result: " + err.Error()}, nil
	}
	return &proto.InvokeCapabilityResponse{ResultJson: b}, nil
}
