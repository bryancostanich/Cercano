package agentclient

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"
)

type authOptInServer struct {
	proto.UnimplementedAgentServer
	seen chan bool
}

func (s *authOptInServer) StreamProcessRequest(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	s.seen <- req.GetSupportsAuthRecovery()
	if req.GetSupportsAuthRecovery() {
		if err := stream.Send(&proto.StreamProcessResponse{Payload: &proto.StreamProcessResponse_AuthenticationRequired{AuthenticationRequired: &proto.AuthenticationRequired{ConversationId: "conv", RequestId: "gate", Provider: "anthropic", ProfileName: "named", RetrySafe: true}}}); err != nil {
			return err
		}
		if err := stream.Send(&proto.StreamProcessResponse{Payload: &proto.StreamProcessResponse_AuthenticationRequired{AuthenticationRequired: &proto.AuthenticationRequired{ConversationId: "conv", RequestId: "gate", Provider: "anthropic", ProfileName: "named", Resolved: true}}}); err != nil {
			return err
		}
	}
	return nil
}
func TestAuthenticationStreamRequiresExplicitClientOptIn(t *testing.T) {
	server := &authOptInServer{seen: make(chan bool, 2)}
	client := cloudLoginClient(t, server)
	for _, enabled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var ch <-chan StreamMsg
		var err error
		if enabled {
			ch, err = client.StreamChatWithRecovery(ctx, "conv", "hello", "")
		} else {
			ch, err = client.StreamChat(ctx, "conv", "hello", "")
		}
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		count := 0
		resolved := false
		for event := range ch {
			if event.Err != nil {
				cancel()
				t.Fatal(event.Err)
			}
			if event.Type == TypeAuthentication {
				count++
				if event.Authentication.Profile != "named" || event.Authentication.RequestID != "gate" {
					cancel()
					t.Fatal("identity dropped")
				}
				resolved = event.Authentication.Resolved
			}
		}
		if got := <-server.seen; got != enabled {
			cancel()
			t.Fatal("opt-in lost")
		}
		if enabled && (count != 2 || !resolved) || !enabled && count != 0 {
			cancel()
			t.Fatalf("events=%d resolved=%v", count, resolved)
		}
		cancel()
	}
}
