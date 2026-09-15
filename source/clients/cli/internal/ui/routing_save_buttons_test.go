package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	tea "charm.land/bubbletea/v2"
	"context"
	"google.golang.org/grpc"
	"net"
	"testing"
)

type routingSaveButtonsStub struct {
	proto.UnimplementedAgentServer
	requests chan *proto.RoutingAssignments
}

func (s *routingSaveButtonsStub) UpdateRoutingAssignments(_ context.Context, r *proto.UpdateRoutingAssignmentsRequest) (*proto.UpdateRoutingAssignmentsResponse, error) {
	s.requests <- r.Assignments
	return &proto.UpdateRoutingAssignmentsResponse{Ok: true}, nil
}
func TestRoutingBothSectionSaveButtons(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stub := &routingSaveButtonsStub{requests: make(chan *proto.RoutingAssignments, 2)}
	proto.RegisterAgentServer(server, stub)
	go server.Serve(l)
	defer server.Stop()
	client, err := agentclient.DialExisting(context.Background(), l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for index, key := range []string{"routing-save-tiers", "routing-save"} {
		t.Run(key, func(t *testing.T) {
			sp := draftTestPage()
			sp.scope = scopeRouting
			sp.agent = client
			find := func() *form.ButtonField {
				t.Helper()
				for _, g := range sp.buildRoutingSections()[index].Groups {
					for _, f := range g.Fields {
						if f.Key() == key {
							return f.(*form.ButtonField)
						}
					}
				}
				t.Fatal("section missing save button")
				return nil
			}
			if _, commit, _ := find().Update(tea.KeyPressMsg{Code: tea.KeyEnter}); commit {
				t.Fatal("clean Save enabled")
			}
			if _, _, err := sp.onCommit("routing-secondary", "fixture-secondary"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := sp.onCommit("routing-task-review-quality", "economy"); err != nil {
				t.Fatal(err)
			}
			button := find()
			_, commit, value := button.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !commit {
				t.Fatal("dirty Save disabled")
			}
			if _, _, err := sp.onCommit(button.Key(), value); err != nil {
				t.Fatal(err)
			}
			request := <-stub.requests
			if request.Secondary != "fixture-secondary" || request.Tasks["review"].Quality != "economy" {
				t.Fatal("Save did not send both sections' shared draft")
			}
			if sp.routingDirty || sp.cloudView.Assignments.Secondary != "fixture-secondary" {
				t.Fatal("saved state not updated")
			}
			if _, commit, _ := find().Update(tea.KeyPressMsg{Code: tea.KeyEnter}); commit {
				t.Fatal("Save still enabled after success")
			}
		})
	}
}
