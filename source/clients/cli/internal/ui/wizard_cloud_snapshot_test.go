package ui

import (
	"cercano/source/clients/cli/internal/wizard"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"context"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
	"net"
	"reflect"
	"testing"
)

func TestWizardSnapshotPreservesChoicesWithoutCredentials(t *testing.T) {
	original := agentclient.CloudProfileInfo{Name: "profile", Flavor: "bedrock", Provider: "anthropic", Region: "region", AWSProfile: "aws-profile", HasKey: true, Choices: &agentclient.CloudModelChoices{TierOverrides: map[string]string{"premium": "custom"}, ImageModel: "image"}}
	snapshot := wizardSnapshotProfile(original)
	original.Choices.TierOverrides["premium"] = "changed"
	encoded, err := yaml.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded wizard.ProfileSnapshot
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	restored := wizardRestoreProfile(decoded)
	if !restored.ReplaceStructure || restored.HasKey || restored.Region != "region" || restored.AWSProfile != "aws-profile" || restored.Choices.ImageModel != "image" || restored.Choices.TierOverrides["premium"] != "custom" {
		t.Fatalf("incomplete snapshot restoration: %+v", restored)
	}
	restored.Choices.TierOverrides["premium"] = "other"
	if decoded.TierOverrides["premium"] != "custom" {
		t.Fatal("restore aliases saved baseline")
	}
	old := wizardRestoreProfile(wizard.ProfileSnapshot{Name: "old", Flavor: "messages", Model: "obsolete"})
	if old.ReplaceStructure || old.Choices != nil || old.Model != "" {
		t.Fatal("old snapshot invented unknown metadata or migrated a legacy pin")
	}
	empty := wizardRestoreProfile(wizardSnapshotProfile(agentclient.CloudProfileInfo{Name: "empty", Choices: &agentclient.CloudModelChoices{}}))
	if empty.Choices == nil || !reflect.DeepEqual(empty.Choices.TierOverrides, map[string]string{}) {
		t.Fatal("explicit empty captured choices lost")
	}
}

func (s *cloudSettingsStub) GetCloudProfiles(context.Context, *proto.GetCloudProfilesRequest) (*proto.GetCloudProfilesResponse, error) {
	return &proto.GetCloudProfilesResponse{Active: "work-profile", Profiles: []*proto.CloudProfileInfo{{Name: "work-profile", ModelChoices: &proto.ProfileModelChoices{TierOverrides: map[string]string{"economy": "kept"}, ImageModel: "kept-image"}}}}, nil
}
func TestWizardCloudChoiceRPCUsesActiveProfileAndExplicitEdits(t *testing.T) {
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
	wp := &wizardPage{agent: client, state: wizard.State{LocusMode: "cloud_only", CloudProvider: "preset-id", TierPicks: map[string]string{"most_capable.cloud": "autofilled", "everyday.cloud": "chosen"}}}
	if err := wp.applyCloudChoices(context.Background()); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	count := len(stub.calls)
	stub.mu.Unlock()
	if count != 0 {
		t.Fatal("autofill caused a profile mutation")
	}
	wp.state.CloudPicksEdited = map[string]bool{"everyday.cloud": true}
	if err := wp.applyCloudChoices(context.Background()); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.calls) != 1 {
		t.Fatalf("calls=%d", len(stub.calls))
	}
	req := stub.calls[0]
	if req.Name != "work-profile" || req.GetModel() != "" || req.GetStructure() != nil || req.GetModelChoices().GetImageModel() != "kept-image" || req.ModelChoices.TierOverrides["standard"] != "chosen" || req.ModelChoices.TierOverrides["economy"] != "kept" || req.ModelChoices.TierOverrides["premium"] != "" {
		t.Fatalf("incorrect wizard patch: %+v", req)
	}
}
