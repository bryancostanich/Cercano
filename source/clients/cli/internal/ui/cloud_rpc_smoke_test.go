package ui

import (
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"context"
	"google.golang.org/grpc"
	"net"
	"sync"
	"testing"
)

type cloudSettingsStub struct {
	proto.UnimplementedAgentServer
	mu     sync.Mutex
	calls  []*proto.UpsertCloudProfileRequest
	reject bool
}

func (s *cloudSettingsStub) UpsertCloudProfile(_ context.Context, r *proto.UpsertCloudProfileRequest) (*proto.UpsertCloudProfileResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r)
	if s.reject {
		return &proto.UpsertCloudProfileResponse{Error: "fixture rejection"}, nil
	}
	return &proto.UpsertCloudProfileResponse{Ok: true}, nil
}
func TestCloudSaveRPCSmokeAndErrorRetention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stub := &cloudSettingsStub{}
	proto.RegisterAgentServer(server, stub)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	client, err := agentclient.DialExisting(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	page := draftTestPage()
	page.agent = client
	page.commitCloud(classifyCloudCommit("cloud-quality-premium", "custom"))
	page.commitCloud(classifyCloudCommit("cloud-image", "custom-image"))
	stub.mu.Lock()
	count := len(stub.calls)
	stub.mu.Unlock()
	if count != 0 {
		t.Fatal("editing performed RPC")
	}
	_, _, err = page.commitCloud(classifyCloudCommit("cloud-save", ""))
	if err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	count = len(stub.calls)
	saved := stub.calls[0]
	stub.reject = true
	stub.mu.Unlock()
	if count != 1 || page.cloudDirty || saved.GetModelChoices().GetImageModel() != "custom-image" || saved.GetModelChoices().GetTierOverrides()["premium"] != "custom" || saved.GetStructure() == nil {
		t.Fatal("Save did not commit the complete draft once")
	}
	page.commitCloud(classifyCloudCommit("cloud-quality-premium", "retry-me"))
	_, _, err = page.commitCloud(classifyCloudCommit("cloud-save", ""))
	if err == nil || !page.cloudDirty || page.cloudDraft.Choices.TierOverrides["premium"] != "retry-me" {
		t.Fatal("failed Save lost editable draft")
	}
	page.commitCloud(classifyCloudCommit("cloud-discard", ""))
	stub.mu.Lock()
	count = len(stub.calls)
	stub.mu.Unlock()
	if page.cloudDirty || count != 2 {
		t.Fatal("Discard performed another RPC or kept dirty state")
	}
}
