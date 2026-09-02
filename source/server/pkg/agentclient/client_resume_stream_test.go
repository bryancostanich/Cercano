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

func (s resumeStreamTestServer) StreamResumeConversationViewportFirst(req *proto.ResumeConversationViewportFirstRequest, stream proto.Agent_StreamResumeConversationViewportFirstServer) error {
	events := []*proto.ResumeConversationViewportFirstEvent{
		{Kind: proto.ResumeConversationViewportFirstEvent_TAIL, ConversationId: req.GetConversationId(), StartIndex: 8, TotalTurns: 10, Turns: []*proto.PersistedTurn{{Id: "tail", ConversationId: req.GetConversationId(), Role: "assistant", Content: "tail"}}},
		{Kind: proto.ResumeConversationViewportFirstEvent_OLDER, ConversationId: req.GetConversationId(), StartIndex: 0, TotalTurns: 10, Turns: []*proto.PersistedTurn{{Id: "older", ConversationId: req.GetConversationId(), Role: "user", Content: "older"}}},
		{Kind: proto.ResumeConversationViewportFirstEvent_BACKFILL_COMPLETE, ConversationId: req.GetConversationId(), TotalTurns: 10},
		{Kind: proto.ResumeConversationViewportFirstEvent_HYDRATION_COMPLETE, ConversationId: req.GetConversationId(), TotalTurns: 10},
	}
	for _, ev := range events {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	return nil
}

func TestStreamResumeConversationViewportFirstDeliversProgressiveEvents(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	proto.RegisterAgentServer(server, resumeStreamTestServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()

	client := &Client{agent: proto.NewAgentClient(conn)}
	var got []ResumeViewportEvent
	if err := client.StreamResumeConversationViewportFirst(ctx, "conv-progressive", 2, 4, func(ev ResumeViewportEvent) error {
		got = append(got, ev)
		return nil
	}); err != nil {
		t.Fatalf("StreamResumeConversationViewportFirst() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4", len(got))
	}
	if got[0].Kind != ResumeViewportEventTail || got[0].Turns[0].Content != "tail" || got[0].StartIndex != 8 || got[0].TotalTurns != 10 {
		t.Fatalf("first event = %+v, want tail at 8/10", got[0])
	}
	if got[1].Kind != ResumeViewportEventOlder || got[1].Turns[0].Content != "older" {
		t.Fatalf("second event = %+v, want older page", got[1])
	}
	if got[2].Kind != ResumeViewportEventBackfillComplete || got[3].Kind != ResumeViewportEventHydrationComplete {
		t.Fatalf("completion events = %v, %v", got[2].Kind, got[3].Kind)
	}
}
