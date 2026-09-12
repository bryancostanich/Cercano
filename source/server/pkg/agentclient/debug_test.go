package agentclient

import (
	"cercano/source/server/pkg/proto"
	"context"
	"testing"
)

type debugFlagServer struct {
	proto.UnimplementedAgentServer
	seen chan bool
}

func (s *debugFlagServer) StreamProcessRequest(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	s.seen <- req.GetDebugMode()
	return nil
}
func TestDebugAdvertisementIsExplicitAndPerTurn(t *testing.T) {
	server := &debugFlagServer{seen: make(chan bool, 1)}
	client := cloudLoginClient(t, server)
	for _, debug := range []bool{false, true, false} {
		ctx := WithDebugAdvertisement(context.Background(), debug)
		events, err := client.StreamChatWithRecovery(ctx, "conv", "hello", "/same/workdir")
		if err != nil {
			t.Fatal(err)
		}
		for event := range events {
			if event.Err != nil {
				t.Fatal(event.Err)
			}
		}
		if got := <-server.seen; got != debug {
			t.Fatalf("debug flag leaked/lost: want=%v got=%v", debug, got)
		}
	}
}
