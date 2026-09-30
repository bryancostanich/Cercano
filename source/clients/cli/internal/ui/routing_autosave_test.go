package ui

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
)

type routingAutosaveServer struct {
	proto.UnimplementedAgentServer
	requests chan *proto.RoutingAssignments
	reject   atomic.Bool
	warning  atomic.Bool
}

func (s *routingAutosaveServer) UpdateRoutingAssignments(_ context.Context, req *proto.UpdateRoutingAssignmentsRequest) (*proto.UpdateRoutingAssignmentsResponse, error) {
	s.requests <- req.Assignments
	if s.reject.Load() {
		return &proto.UpdateRoutingAssignmentsResponse{Error: "fixture rejected update"}, nil
	}
	warning := ""
	if s.warning.Load() {
		warning = "fixture provider unavailable"
	}
	return &proto.UpdateRoutingAssignmentsResponse{Ok: true, Warning: warning}, nil
}
func attachRoutingAutosaveAgent(t *testing.T, sp *settingsPage) *routingAutosaveServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stub := &routingAutosaveServer{requests: make(chan *proto.RoutingAssignments, 64)}
	proto.RegisterAgentServer(server, stub)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client, err := agentclient.DialExisting(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	sp.agent = client
	return stub
}
func TestModelTierChangeSavesImmediatelyWithoutTaskDraft(t *testing.T) {
	sp := draftTestPage()
	stub := attachRoutingAutosaveAgent(t, sp)
	if _, _, err := sp.commitRouting("routing-task-review-quality", "economy"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sp.commitRouting("routing-secondary", "second-account"); err != nil {
		t.Fatal(err)
	}
	var saved *proto.RoutingAssignments
	select {
	case saved = <-stub.requests:
	default:
		t.Fatal("model-tier change did not save immediately")
	}
	if saved.Secondary != "second-account" || len(saved.Tasks) != 0 {
		t.Fatal("auto-save included unrelated task draft")
	}
	if sp.cloudView.Assignments.Secondary != "second-account" || !sp.routingDirty || sp.routingDraft.Tasks["review"].Quality != "economy" {
		t.Fatal("saved baseline or pending task edit lost")
	}
	for _, g := range sp.buildRoutingSections()[0].Groups {
		for _, field := range g.Fields {
			if field.Key() == "routing-save-tiers" {
				t.Fatal("model tiers still has Save routing")
			}
		}
	}
	if _, _, err := sp.commitRouting("routing-discard", ""); err != nil {
		t.Fatal(err)
	}
	sp.ensureRoutingDraft()
	if sp.routingDraft.Secondary != "second-account" || len(sp.routingDraft.Tasks) != 0 {
		t.Fatal("task discard undid auto-saved tier")
	}
}
func TestFailedModelTierAutosaveRestoresPreviousValue(t *testing.T) {
	sp := draftTestPage()
	stub := attachRoutingAutosaveAgent(t, sp)
	sp.commitRouting("routing-task-review-quality", "economy")
	sp.ensureRoutingDraft()
	before := sp.routingDraft.Clone()
	stub.reject.Store(true)
	if _, _, err := sp.commitRouting("routing-secondary", "rejected"); err == nil {
		t.Fatal("failed save hidden")
	}
	if sp.routingDraft.Secondary != before.Secondary || !sp.routingDirty || sp.routingDraft.Tasks["review"].Quality != "economy" {
		t.Fatal("failure retained unsaved tier or lost task draft")
	}
	sp.agent = nil
	if _, _, err := sp.commitRouting("routing-secondary", "disconnected"); err == nil {
		t.Fatal("offline save accepted")
	}
	if sp.routingDraft.Secondary != before.Secondary {
		t.Fatal("offline change was not reverted")
	}
}

func TestModelTierNoOpValidationAndAvailabilityWarning(t *testing.T) {
	sp := draftTestPage()
	stub := attachRoutingAutosaveAgent(t, sp)
	commit := func(field, value string) string {
		t.Helper()
		message, _, err := sp.commitRouting(field, value)
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	commit("routing-secondary-redirect", "primary")
	<-stub.requests
	commit("routing-secondary-redirect", "primary")
	select {
	case <-stub.requests:
		t.Fatal("unchanged selection sent another update")
	default:
	}
	commit("routing-local-redirect", "secondary")
	<-stub.requests
	if _, _, err := sp.commitRouting("routing-secondary-redirect", "local"); err == nil {
		t.Fatal("redirect cycle accepted")
	}
	select {
	case <-stub.requests:
		t.Fatal("invalid redirect sent to server")
	default:
	}
	if sp.routingDraft.SecondaryRedirect != "primary" || sp.routingDirty {
		t.Fatal("failed validation retained draft")
	}
	stub.warning.Store(true)
	if message := commit("routing-secondary", "available-later"); !strings.Contains(message, "provider unavailable") {
		t.Fatal("availability warning hidden")
	}
	<-stub.requests
	if sp.routingDirty || sp.cloudView.Assignments.Secondary != "available-later" {
		t.Fatal("warning treated as an unsaved failure")
	}
}
func TestModelTierSnapshotRefreshKeepsOnlyTaskDraft(t *testing.T) {
	sp := draftTestPage()
	sp.commitRouting("routing-task-review-quality", "economy")
	sp.cloudView.Assignments = &agentclient.RoutingAssignments{Secondary: "server-account", PrimaryBackups: []string{"backup"}}
	sp.refreshRoutingModelTiers()
	if sp.routingDraft.Secondary != "server-account" || !sp.routingDirty || sp.routingDraft.Tasks["review"].Quality != "economy" {
		t.Fatal("fresh model tiers or pending task edit lost")
	}
	sp.routingDraft.PrimaryBackups[0] = "edited-copy"
	if sp.cloudView.Assignments.PrimaryBackups[0] != "backup" {
		t.Fatal("snapshot aliases draft")
	}
}
