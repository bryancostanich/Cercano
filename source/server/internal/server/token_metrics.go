package server

import (
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/pkg/proto"
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) GetTokenMetrics(ctx context.Context, r *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error) {
	s.attemptSinkMu.RLock()
	receiver := s.accountingReceiver
	s.attemptSinkMu.RUnlock()
	reader, ok := receiver.(interface {
		TokenMetrics(context.Context, *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unavailable, "token accounting is not enabled")
	}
	out, err := reader.TokenMetrics(ctx, r)
	if errors.Is(err, telemetry.ErrMetricsRequest) {
		return nil, status.Error(codes.InvalidArgument, "invalid metrics range, timezone, population, or filter")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "token metrics unavailable; accounting may be starting or storage may be degraded")
	}
	return out, nil
}
