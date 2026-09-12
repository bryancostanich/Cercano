package server

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"context"
	"testing"
)

func TestRoutingSettingsAtomicPresence(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "p", Flavor: "messages"}, {Name: "pb", Flavor: "messages"}, {Name: "s", Flavor: "messages"}, {Name: "sb", Flavor: "messages"}}
	s.cfgSvc.Set(c)
	assignments := &proto.RoutingAssignments{Primary: "p", PrimaryBackup: "pb", Secondary: "s", SecondaryBackup: "sb", Tasks: map[string]*proto.TaskModelAssignment{"dispatch": {Destination: "secondary", Quality: "standard"}}}
	resp, err := s.UpdateRoutingAssignments(context.Background(), &proto.UpdateRoutingAssignmentsRequest{Assignments: assignments})
	if err != nil || !resp.Ok {
		t.Fatalf("save: %v %v", resp, err)
	}
	// Configured credential failures remain in the chain instead of being
	// dropped as absent providers. Saving succeeds; use reports the typed failure.
	if s.providerSvc.Cloud() == nil {
		t.Fatal("configured credential failure disappeared")
	}
	if _, callErr := s.providerSvc.Cloud().Chat(context.Background(), inference.Call{Tier: "most_capable"}); llm.ClassOf(callErr) != llm.ErrAuth {
		t.Fatalf("credential failure was hidden: %v", callErr)
	}
	view, err := s.GetCloudProviders(context.Background(), &proto.GetCloudProvidersRequest{})
	if err != nil || view.GetAssignments().GetSecondaryBackup() != "sb" {
		t.Fatalf("view: %v %v", view, err)
	}
	s.UpdateRoutingAssignments(context.Background(), &proto.UpdateRoutingAssignmentsRequest{})
	if s.cfgSvc.Get().SecondaryCloudProfile != "s" {
		t.Fatal("omitted assignments changed bindings")
	}
	invalid := &proto.RoutingAssignments{Primary: "p", Secondary: "s", SecondaryBackup: "s"}
	resp, err = s.UpdateRoutingAssignments(context.Background(), &proto.UpdateRoutingAssignmentsRequest{Assignments: invalid})
	if err != nil || resp.Ok || s.cfgSvc.Get().BackupCloudProfile != "pb" {
		t.Fatal("invalid draft partially applied")
	}
	resp, err = s.UpdateRoutingAssignments(context.Background(), &proto.UpdateRoutingAssignmentsRequest{Assignments: &proto.RoutingAssignments{}})
	if err != nil || !resp.Ok {
		t.Fatalf("clear: %v %v", resp, err)
	}
	got := s.cfgSvc.Get()
	if got.ActiveCloudProfile != "" || got.BackupCloudProfile != "" || got.SecondaryCloudProfile != "" || got.SecondaryBackupCloudProfile != "" || len(got.TaskAssignments) != 0 {
		t.Fatal("clear did not reset assignments")
	}
	if got.TaskAssignment(config.TaskDispatch).Quality != config.CostPremium {
		t.Fatal("cleared task did not inherit Premium")
	}
}

func TestProfileSaveReportsAvailabilityAsWarning(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.ActiveCloudProfile = "no-key"
	c.CloudProfiles = []config.CloudProfile{{Name: "no-key", Flavor: "messages"}}
	s.cfgSvc.Set(c)
	response, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "no-key", ModelChoices: &proto.ProfileModelChoices{TierOverrides: map[string]string{"premium": "custom"}}})
	saved := s.cfgSvc.Get().CloudProfiles[0].TierOverrides[config.CostPremium]
	if err != nil || !response.Ok || response.Warning == "" || saved != "custom" {
		t.Fatalf("saved choice=%q but response=%+v err=%v", saved, response, err)
	}
}
