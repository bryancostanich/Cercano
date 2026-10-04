package enterprise

import (
	"context"
	"net"
	"time"

	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type RPCServer struct {
	proto.UnimplementedEnterpriseServer
	host        *Host
	openBrowser func(context.Context, string) error
}

func NewRPCServer(host *Host, open func(context.Context, string) error) *RPCServer {
	return &RPCServer{host: host, openBrowser: open}
}
func localControl(ctx context.Context) error {
	p, ok := peer.FromContext(ctx)
	if ok {
		switch addr := p.Addr.(type) {
		case *net.TCPAddr:
			if addr.IP.IsLoopback() {
				return nil
			}
		case *net.UnixAddr:
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "enterprise controls require a local host connection")
}
func controlError(err error) error {
	if err == nil {
		return nil
	}
	return status.Error(codes.FailedPrecondition, err.Error())
}
func (s *RPCServer) result() *proto.EnterpriseStatus {
	state := s.host.Status()
	expiry := ""
	if !state.ValidUntil.IsZero() {
		expiry = state.ValidUntil.UTC().Format(time.RFC3339)
	}
	return &proto.EnterpriseStatus{Managed: state.Managed, EnforcementActive: state.EnforcementActive, Connected: state.Connected, Usable: state.Usable, OrganizationId: state.OrganizationID, HostId: state.HostID, Revision: state.Revision, ValidUntil: expiry, Error: state.Error, Changing: state.Changing}
}
func (s *RPCServer) Login(ctx context.Context, req *proto.EnterpriseLoginRequest) (*proto.EnterpriseStatus, error) {
	if err := localControl(ctx); err != nil {
		return nil, err
	}
	if s.openBrowser == nil {
		return nil, status.Error(codes.Unavailable, "system browser unavailable")
	}
	if err := s.host.Login(ctx, req.GetServer(), req.GetOrganizationId(), func(target string) error { return s.openBrowser(ctx, target) }); err != nil {
		return nil, controlError(err)
	}
	return s.result(), nil
}
func (s *RPCServer) GetStatus(ctx context.Context, _ *proto.EnterpriseControlRequest) (*proto.EnterpriseStatus, error) {
	if err := localControl(ctx); err != nil {
		return nil, err
	}
	return s.result(), nil
}
func (s *RPCServer) Synchronize(ctx context.Context, _ *proto.EnterpriseControlRequest) (*proto.EnterpriseStatus, error) {
	if err := localControl(ctx); err != nil {
		return nil, err
	}
	if err := s.host.Sync(ctx); err != nil {
		return nil, controlError(err)
	}
	return s.result(), nil
}
func (s *RPCServer) Logout(ctx context.Context, _ *proto.EnterpriseControlRequest) (*proto.EnterpriseStatus, error) {
	if err := localControl(ctx); err != nil {
		return nil, err
	}
	if err := s.host.Logout(ctx); err != nil {
		return nil, controlError(err)
	}
	return s.result(), nil
}
func (s *RPCServer) UseStandalone(ctx context.Context, _ *proto.EnterpriseControlRequest) (*proto.EnterpriseStatus, error) {
	if err := localControl(ctx); err != nil {
		return nil, err
	}
	if err := s.host.UseStandalone(); err != nil {
		return nil, controlError(err)
	}
	return s.result(), nil
}

// Scopes pin the policy/default/skill bundle for user-triggered inference work.
// Metadata and repair controls remain accessible when managed work is blocked.
func inferenceRPC(method string) bool {
	switch method {
	case proto.Agent_ProcessRequest_FullMethodName, proto.Agent_StreamProcessRequest_FullMethodName, proto.Agent_InvokeTool_FullMethodName, proto.Agent_InvokeCapability_FullMethodName, proto.Agent_RegenerateContext_FullMethodName, proto.Agent_SuggestNextPrompt_FullMethodName, proto.Agent_ElideContext_FullMethodName:
		return true
	default:
		return false
	}
}
func (h *Host) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !inferenceRPC(info.FullMethod) {
			return handler(ctx, req)
		}
		scoped, finish, err := h.Begin(ctx)
		if err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		defer finish()
		return handler(scoped, req)
	}
}

type enterpriseStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *enterpriseStream) Context() context.Context { return s.ctx }
func (h *Host) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !inferenceRPC(info.FullMethod) {
			return handler(srv, stream)
		}
		scoped, finish, err := h.Begin(stream.Context())
		if err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
		defer finish()
		return handler(srv, &enterpriseStream{ServerStream: stream, ctx: scoped})
	}
}
