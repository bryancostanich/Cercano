package server

import (
	"context"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// A model-only edit must not erase provider identity or AWS authentication metadata.
func TestRoutingContractProfileEditPreservesIdentity(t *testing.T) {
	s, _ := newTestServer()
	original := config.CloudProfile{Name: "aws", Flavor: "bedrock", Provider: "anthropic", Route: "direct", Region: "us-west-2", AWSProfile: "fixture", Model: "old"}
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{original}})
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "aws", Flavor: "bedrock", Model: "new"})
	if err != nil || !resp.GetOk() {
		t.Fatalf("upsert: %v, %v", resp, err)
	}
	got := s.cfgSvc.Get().CloudProfiles[0]
	t.Logf("profile after edit: %+v", got)
	if got.Region != original.Region || got.AWSProfile != original.AWSProfile || got.Provider != original.Provider || got.Route != original.Route {
		t.Errorf("identity changed: got %+v; original %+v", got, original)
	}
}

// Probe the old mutation's empty-string semantics before replacing it with
// presence-aware quality/image fields. This is baseline evidence, not a desired
// future contract for the retired model field.
func TestRoutingBaselineEmptyModelPreservesPin(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{{Name: "fixture", Flavor: "messages", Model: "legacy", ModelPinned: true}}})
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "fixture", Flavor: "messages"})
	if err != nil || !resp.GetOk() {
		t.Fatalf("upsert: %v, %v", resp, err)
	}
	got := s.cfgSvc.Get().CloudProfiles[0]
	t.Logf("empty model upsert: model=%q pinned=%v", got.Model, got.ModelPinned)
	if got.Model != "legacy" || !got.ModelPinned {
		t.Fatalf("baseline changed: %+v", got)
	}
}
