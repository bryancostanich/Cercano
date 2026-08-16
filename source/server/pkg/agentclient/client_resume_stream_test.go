package agentclient

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type resumeStreamTestServer struct {
	proto.UnimplementedAgentServer
	chunks        int
	bytesPerChunk int
}

func (s resumeStreamTestServer) StreamResumeConversation(req *proto.ResumeConversationRequest, stream proto.Agent_StreamResumeConversationServer) error {
	for i := 0; i < s.chunks; i++ {
		if err := stream.Send(&proto.ResumeConversationChunk{Turns: []*proto.PersistedTurn{{
			Id:             "turn",
			ConversationId: req.GetConversationId(),
			Role:           "assistant",
			Content:        strings.Repeat("x", s.bytesPerChunk),
		}}}); err != nil {
			return err
		}
	}
	return nil
}

func TestResumeConversationCollectsStreamLargerThanUnaryLimit(t *testing.T) {
	const maxMessage = 64 << 20
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxMessage), grpc.MaxSendMsgSize(maxMessage))
	proto.RegisterAgentServer(server, resumeStreamTestServer{chunks: 140, bytesPerChunk: 512 << 10})
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessage), grpc.MaxCallSendMsgSize(maxMessage)),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()

	client := &Client{agent: proto.NewAgentClient(conn)}
	turns, err := client.ResumeConversation(ctx, "conv-large")
	if err != nil {
		t.Fatalf("ResumeConversation() error = %v", err)
	}
	if len(turns) != 140 {
		t.Fatalf("got %d turns, want 140", len(turns))
	}
}
