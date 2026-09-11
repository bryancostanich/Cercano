package agentclient

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func cloudLoginClient(t *testing.T, service proto.AgentServer) *Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	proto.RegisterAgentServer(server, service)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &Client{agent: proto.NewAgentClient(conn)}
}

type legacyCloudLoginServer struct {
	proto.UnimplementedAgentServer
	onboarding atomic.Int32
}

func (s *legacyCloudLoginServer) StartClaudeLogin(*proto.StartClaudeLoginRequest, proto.Agent_StartClaudeLoginServer) error {
	s.onboarding.Add(1)
	return nil
}
func (s *legacyCloudLoginServer) StartChatGPTLogin(*proto.StartChatGPTLoginRequest, proto.Agent_StartChatGPTLoginServer) error {
	s.onboarding.Add(1)
	return nil
}
func TestReauthenticationUnsupportedPeerNeverFallsBackToSetup(t *testing.T) {
	server := &legacyCloudLoginServer{}
	client := cloudLoginClient(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch, err := client.ReauthenticateCloud(ctx, "work", "id")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-ch:
		if status.Code(result.Err) != codes.Unimplemented {
			t.Fatalf("err=%v", result.Err)
		}
	case <-ctx.Done():
		t.Fatal("unsupported operation hung")
	}
	if server.onboarding.Load() != 0 {
		t.Fatal("unsafe onboarding fallback")
	}
}

type testCloudLoginServer struct {
	proto.UnimplementedAgentServer
	mismatch bool
	finished chan struct{}
}

func (s *testCloudLoginServer) ReauthenticateCloud(req *proto.CloudReauthenticationRequest, stream proto.Agent_ReauthenticateCloudServer) error {
	defer close(s.finished)
	id := req.GetAttemptId()
	if s.mismatch {
		id = "other-attempt"
	}
	if err := stream.Send(&proto.CloudLoginEvent{ProfileName: req.GetProfileName(), Provider: "anthropic", AttemptId: id, AuthorizeUrl: "https://example.invalid/authorize"}); err != nil {
		return err
	}
	if !s.mismatch {
		if err := stream.Send(&proto.CloudLoginEvent{ProfileName: req.GetProfileName(), Provider: "anthropic", AttemptId: id, Done: true, Ok: true}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}
func TestReauthenticationCorrelatesEventsAndClosesItsStream(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		server := &testCloudLoginServer{mismatch: mismatch, finished: make(chan struct{})}
		client := cloudLoginClient(t, server)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ch, err := client.ReauthenticateCloud(ctx, "work", "chosen-attempt")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		count := 0
		for result := range ch {
			count++
			if result.AttemptID != "chosen-attempt" || result.ProfileName != "work" {
				cancel()
				t.Fatalf("wrong owner: %+v", result)
			}
			if mismatch && result.Err == nil {
				cancel()
				t.Fatal("mismatched event forwarded")
			}
			if !mismatch && result.Err != nil {
				cancel()
				t.Fatal(result.Err)
			}
		}
		expected := 2
		if mismatch {
			expected = 1
		}
		if count != expected {
			cancel()
			t.Fatalf("events=%d", count)
		}
		select {
		case <-server.finished:
		case <-ctx.Done():
			cancel()
			t.Fatal("terminal stream was not canceled")
		}
		cancel()
	}
}
