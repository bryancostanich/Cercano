package server

import (
	"context"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestOrderedAccountsRoutingRPC(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "a", Flavor: "messages"}, {Name: "b", Flavor: "messages"}, {Name: "c", Flavor: "messages"}}
	s.cfgSvc.Set(c)
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	proto.RegisterAgentServer(server, s)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close() })
	conn, err := grpc.NewClient("passthrough:///accounts-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := proto.NewAgentClient(conn)
	ctx := context.Background()
	assignment := &proto.RoutingAssignments{Primary: "a", PrimaryBackup: "b", PrimaryBackups: []string{"b", "c"}}
	resp, err := client.UpdateRoutingAssignments(ctx, &proto.UpdateRoutingAssignmentsRequest{Assignments: assignment})
	if err != nil || !resp.GetOk() {
		t.Fatalf("save: %v %v", resp, err)
	}
	view, err := client.GetCloudProviders(ctx, &proto.GetCloudProvidersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.GetAssignments().GetPrimaryBackups(), []string{"b", "c"}) {
		t.Fatal("RPC lost ordered accounts", view.GetAssignments())
	}
	assignment.PrimaryBackups = []string{"c", "b"}
	assignment.PrimaryBackup = "c"
	resp, err = client.UpdateRoutingAssignments(ctx, &proto.UpdateRoutingAssignmentsRequest{Assignments: assignment})
	if err != nil || !resp.GetOk() {
		t.Fatalf("reorder: %v %v", resp, err)
	}
	if !reflect.DeepEqual(s.cfgSvc.Get().PrimaryBackups(), []string{"c", "b"}) {
		t.Fatal("reorder not saved")
	}
	assignment.PrimaryBackups = []string{"b", "b"}
	assignment.PrimaryBackup = "b"
	resp, err = client.UpdateRoutingAssignments(ctx, &proto.UpdateRoutingAssignmentsRequest{Assignments: assignment})
	if err != nil || resp.GetOk() {
		t.Fatal("duplicate accepted")
	}
	if !reflect.DeepEqual(s.cfgSvc.Get().PrimaryBackups(), []string{"c", "b"}) {
		t.Fatal("invalid edit was partially applied")
	}
	resp, err = client.UpdateRoutingAssignments(ctx, &proto.UpdateRoutingAssignmentsRequest{Assignments: &proto.RoutingAssignments{Primary: "a"}})
	if err != nil || !resp.GetOk() {
		t.Fatalf("clear: %v %v", resp, err)
	}
	if got := s.cfgSvc.Get(); len(got.PrimaryBackups()) != 0 || got.BackupCloudProfile != "" {
		t.Fatal("RPC clear resurrected legacy backup")
	}
}

func TestCreateOnlyAccountIsAtomic(t *testing.T) {
	s, _ := newTestServer()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "unique-account", Flavor: "messages", CreateOnly: true})
			if err != nil {
				t.Error(err)
				return
			}
			if response.Ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("created same identity %d times", successes.Load())
	}
	before, _ := s.cfgSvc.Get().Profile("unique-account")
	response, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "unique-account", Flavor: "responses", CreateOnly: true})
	if err != nil || response.Ok {
		t.Fatal("collision not rejected")
	}
	after, _ := s.cfgSvc.Get().Profile("unique-account")
	if !after.Equal(before) {
		t.Fatal("collision changed existing profile")
	}
}
