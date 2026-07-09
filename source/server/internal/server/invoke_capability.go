package server

import (
	"context"

	"cercano/source/server/pkg/proto"
)

// InvokeCapability resolves a capability by canonical name and runs it. Used by
// the MCP adapter so every cercano_<name> tool forwards to one generic RPC.
// Errors are surfaced in the response (IsError/Error) rather than as gRPC
// errors so the caller can render them inline.
func (s *Server) InvokeCapability(ctx context.Context, req *proto.InvokeCapabilityRequest) (*proto.InvokeCapabilityResponse, error) {
	result, isError, errMsg := s.toolSvc.InvokeCapability(ctx, req.GetName(), req.GetArgsJson(), req.GetWorkDir())
	if isError {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: errMsg}, nil
	}
	return &proto.InvokeCapabilityResponse{ResultJson: result}, nil
}
