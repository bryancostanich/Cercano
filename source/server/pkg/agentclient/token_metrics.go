package agentclient

import (
	"cercano/source/server/pkg/proto"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetTokenMetrics queries actual usage, not the conversation context meter.
func (c *Client) GetTokenMetrics(ctx context.Context, r *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error) {
	conn := c.readConn()
	if conn == nil {
		return nil, status.Error(codes.Unavailable, "agent is disconnected")
	}
	return proto.NewAgentClient(conn).GetTokenMetrics(ctx, r)
}
