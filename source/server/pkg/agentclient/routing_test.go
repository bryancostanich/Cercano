package agentclient

import (
	"cercano/source/server/pkg/proto"
	"context"
	"google.golang.org/grpc"
	"testing"
)

type routingClientStub struct {
	proto.AgentClient
	profile *proto.UpsertCloudProfileRequest
	routing *proto.UpdateRoutingAssignmentsRequest
}

func (s *routingClientStub) UpsertCloudProfile(_ context.Context, r *proto.UpsertCloudProfileRequest, _ ...grpc.CallOption) (*proto.UpsertCloudProfileResponse, error) {
	s.profile = r
	return &proto.UpsertCloudProfileResponse{Ok: true}, nil
}
func (s *routingClientStub) UpdateRoutingAssignments(_ context.Context, r *proto.UpdateRoutingAssignmentsRequest, _ ...grpc.CallOption) (*proto.UpdateRoutingAssignmentsResponse, error) {
	s.routing = r
	return &proto.UpdateRoutingAssignmentsResponse{Ok: true, Warning: "unavailable"}, nil
}
func TestClientChoicesAndRoutingPresence(t *testing.T) {
	stub := &routingClientStub{}
	c := &Client{agent: stub}
	profile := CloudProfileInfo{Name: "p", Choices: &CloudModelChoices{TierOverrides: map[string]string{"premium": "custom"}, ImageModel: "image"}, Region: "region", AWSProfile: "aws"}
	if err := c.UpsertCloudProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	if stub.profile.GetModelChoices().GetImageModel() != "image" || stub.profile.GetAwsProfile() != "aws" {
		t.Fatal("profile choices or AWS metadata lost")
	}
	profile.Choices.TierOverrides["premium"] = "changed"
	if stub.profile.ModelChoices.TierOverrides["premium"] != "custom" {
		t.Fatal("mutation aliases caller")
	}
	profile.Choices = nil
	c.UpsertCloudProfile(context.Background(), profile)
	if stub.profile.ModelChoices != nil {
		t.Fatal("omission lost")
	}
	profile.Choices = &CloudModelChoices{}
	c.UpsertCloudProfile(context.Background(), profile)
	if stub.profile.ModelChoices == nil || len(stub.profile.ModelChoices.TierOverrides) != 0 {
		t.Fatal("clear lost")
	}
	a := &RoutingAssignments{Primary: "p", PrimaryBackup: "pb", Secondary: "s", SecondaryBackup: "sb", Tasks: map[string]TaskAssignment{"dispatch": {Destination: "secondary", Quality: "standard"}}}
	warning, err := c.UpdateRoutingAssignments(context.Background(), a)
	if err != nil || warning != "unavailable" {
		t.Fatal("save warning conflated with failure")
	}
	got := assignmentsFromProto(stub.routing.Assignments)
	if got.SecondaryBackup != "sb" || got.Tasks["dispatch"].Quality != "standard" {
		t.Fatal("routing lost")
	}
	clone := a.Clone()
	clone.Tasks["dispatch"] = TaskAssignment{}
	if a.Tasks["dispatch"].Quality != "standard" {
		t.Fatal("draft aliases loaded assignments")
	}
}

func TestCompleteStructurePresence(t *testing.T) {
	stub := &routingClientStub{}
	client := &Client{agent: stub}
	p := CloudProfileInfo{Name: "p", Flavor: "messages", ReplaceStructure: true}
	if err := client.UpsertCloudProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if stub.profile.Structure == nil || stub.profile.Structure.BaseUrl != "" {
		t.Fatal("explicit structural clear lost")
	}
	p.ReplaceStructure = false
	if err := client.UpsertCloudProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if stub.profile.Structure != nil || stub.profile.Region != nil || stub.profile.AwsProfile != nil {
		t.Fatal("omitted structure/metadata sent as clears")
	}
}

func TestDestinationRedirectClientRoundTrip(t *testing.T) {
	a := &RoutingAssignments{SecondaryRedirect: "local", LocalRedirect: "primary"}
	got := assignmentsFromProto(assignmentsToProto(a.Clone()))
	if got.SecondaryRedirect != "local" || got.LocalRedirect != "primary" {
		t.Fatal("client roundtrip lost redirects")
	}
	got.LocalRedirect = ""
	if a.LocalRedirect != "primary" {
		t.Fatal("clone aliases original")
	}
	if assignmentsToProto(nil) != nil || assignmentsFromProto(nil) != nil {
		t.Fatal("absence lost")
	}
	cleared := assignmentsFromProto(assignmentsToProto(&RoutingAssignments{}))
	if cleared == nil || cleared.SecondaryRedirect != "" || cleared.LocalRedirect != "" {
		t.Fatal("explicit clear lost")
	}
}
