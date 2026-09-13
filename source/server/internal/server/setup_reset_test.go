package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/setupresetcmd"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"cercano/source/server/pkg/setupstate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestSetupResetKeepsExistingRPCSession(t *testing.T) {
	s, _ := newTestServer()
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "old", Flavor: "chat_completions", TierOverrides: map[config.CostTier]string{config.CostPremium: "old-model"}}}
	c.ActiveCloudProfile = "old"
	c.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationLocal, Quality: config.CostEconomy}}
	if err := s.cfgSvc.Set(c); err != nil {
		t.Fatal(err)
	}
	s.cfgSvc.SetPath(path)
	if err := config.Save(c, path); err != nil {
		t.Fatal(err)
	}
	s.cfgSvc.Secrets().Set("old", "fixture")
	s.cfgSvc.Secrets().Set("orphan", "refresh-fixture")
	sentinel := filepath.Join(filepath.Dir(path), "conversation.db")
	os.WriteFile(sentinel, []byte("preserved"), 0600)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, c.Port, _ = net.SplitHostPort(listener.Addr().String())
	s.cfgSvc.Set(c)
	if err := config.Save(c, path); err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	proto.RegisterAgentServer(gs, s)
	go gs.Serve(listener)
	defer gs.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := proto.NewAgentClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = client.GetConfig(ctx, &proto.GetConfigRequest{}); err != nil {
		t.Fatal(err)
	}
	rejected, err := client.ResetSetup(ctx, &proto.ResetSetupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Ok || rejected.ConfigWritten || rejected.LiveApplied {
		t.Fatal("unconfirmed reset changed state")
	}
	names, _ := s.cfgSvc.Secrets().List()
	if len(names) != 2 {
		t.Fatal("unconfirmed reset touched keys")
	}
	wizardPath := filepath.Join(filepath.Dir(path), "wizard.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", wizardPath)
	setupstate.Save(setupstate.State{Step: setupstate.StepDone, Baseline: &setupstate.Baseline{ActiveProfile: "old"}})
	result, err := setupresetcmd.Perform(ctx, setupresetcmd.Dependencies{ConfigPath: path, WizardPath: wizardPath, TryAgent: setupresetcmd.TryExistingAgent, OpenStore: func() (secrets.Store, error) { t.Fatal("live command opened local keychain"); return nil, nil }})
	if err != nil || !result.UsedAgent || !result.WizardWritten || !result.LiveApplied || !result.ConfigWritten || result.CredentialsRemoved != 2 {
		t.Fatalf("reset=%+v err=%v", result, err)
	}
	if _, err = client.GetConfig(ctx, &proto.GetConfigRequest{}); err != nil {
		t.Fatalf("existing connection broken: %v", err)
	}
	got := s.cfgSvc.Get()
	if len(got.CloudProfiles) != 0 || got.ActiveCloudProfile != "" || len(got.TaskAssignments) != 0 {
		t.Fatalf("stale in-memory setup: %+v", got)
	}
	assignment := s.providerSvc.Candidates().TaskFor(config.TaskChat)
	if assignment != (config.Config{}).TaskAssignment(config.TaskChat) {
		t.Fatalf("stale routing graph: %+v", assignment)
	}
	state, ok := setupstate.Load()
	if !ok || state.Step != setupstate.StepLocus || state.Baseline != nil {
		t.Fatal("old wizard resumed")
	}
	names, _ = s.cfgSvc.Secrets().List()
	if len(names) != 0 {
		t.Fatal("credential survived")
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "preserved" {
		t.Fatal("history touched")
	}
}
