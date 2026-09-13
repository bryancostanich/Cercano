package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	wire "google.golang.org/protobuf/proto"
)

func TestRoutingSettingsClientPersistenceSnapshotExecution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	calls := []string{}
	endpoint := func(name string, fail bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, name)
			if r.Header.Get("Authorization") != "Bearer key-"+name {
				t.Errorf("wrong credential for %s", name)
			}
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Model != name+"-standard" {
				t.Errorf("model=%s want %s-standard", request.Model, name)
			}
			w.Header().Set("Content-Type", "application/json")
			if fail {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
				return
			}
			io.WriteString(w, `{"id":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
		}))
	}
	final := endpoint("final", true)
	defer final.Close()
	backup := endpoint("backup", false)
	defer backup.Close()
	source := endpoint("source", false)
	defer source.Close()
	c := config.Config{LocusMode: "cloud_only"}
	for name, url := range map[string]string{"final": final.URL, "backup": backup.URL, "source": source.URL} {
		c.CloudProfiles = append(c.CloudProfiles, config.CloudProfile{Name: name, Flavor: "chat_completions", BaseURL: url, TierOverrides: map[config.CostTier]string{config.CostStandard: name + "-standard", config.CostPremium: name + "-premium"}})
	}
	s, _ := newTestServer()
	s.cfgSvc.Set(c)
	for _, name := range []string{"final", "backup", "source"} {
		s.cfgSvc.Secrets().Set(name, "key-"+name)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	proto.RegisterAgentServer(gs, s)
	go gs.Serve(listener)
	defer gs.Stop()
	client, err := agentclient.DialExisting(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	assignments := &agentclient.RoutingAssignments{Primary: "source", Secondary: "final", SecondaryBackup: "backup", LocalRedirect: "secondary", Tasks: map[string]agentclient.TaskAssignment{"watchdog": {Destination: "local", Quality: "standard"}, "dispatch": {Destination: "primary", Quality: "premium"}}}
	if _, err = client.UpdateRoutingAssignments(context.Background(), assignments); err != nil {
		t.Fatal(err)
	}
	view, err := client.GetCloudProviders(context.Background())
	if err != nil || view.Assignments.LocalRedirect != "secondary" || view.Assignments.Tasks["watchdog"].Quality != "standard" {
		t.Fatalf("client round-trip %+v %v", view, err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = config.Save(s.cfgSvc.Get(), path); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := worker.SnapshotConfig(saved, "", nil)
	data, err := wire.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"key-final", "key-backup", "key-source"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("credential leaked into config snapshot")
		}
	}
	decoded := &proto.ConfigSnapshot{}
	if err = wire.Unmarshal(data, decoded); err != nil {
		t.Fatal(err)
	}
	restored := worker.ConfigFromSnapshot(decoded)
	evidence := func(p config.CloudProfile, _ string) modelmetadata.Evidence {
		window := 8192
		if p.Name == "backup" {
			window = 16384
		}
		return modelmetadata.Evidence{ContextWindow: window}
	}
	// The worker's shared destination-chain implementation consumes the restored
	// snapshot; separate worker-resolver tests cover its Main() handoff.
	chain, err := profilechain.Build(restored, config.DestinationSecondary, func(p config.CloudProfile) (inference.Provider, error) {
		provider, err := cloudfactory.BuildCloudProvider(p, "key-"+p.Name)
		if err != nil {
			return nil, err
		}
		return profilechain.GuardVision(provider, nil, func(model string) modelmetadata.Evidence { return evidence(p, model) }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := dispatch.NewEngine(func() inference.Tiers {
		return inference.Tiers{Mode: locus.CloudOnly, TaskFor: restored.TaskAssignment, ResolveDestination: restored.ResolveDestination, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: chain, Profile: "final", IsCloud: true}}, ModelFor: func(sel inference.Selection, tier config.Tier) string {
			p, _ := restored.Profile(sel.Profile)
			return restored.ModelProfiles.ResolveCloudModelForTier(p, tier)
		}}
	}, func() locus.Mode { return locus.CloudOnly }, nil)
	spec := dispatch.Spec{RoutingTask: config.TaskWatchdog, Prompt: "fixture"}
	target, err := engine.PreparedTarget(context.Background(), spec)
	if err != nil || target.Model != "final-standard" || target.ContextWindow != 8192 || !target.ContextWindowKnown {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile != "backup" || result.Model != "backup-standard" || result.Destination != "secondary" || result.ContextWindow != 16384 || !result.ContextWindowKnown {
		t.Fatalf("actual serving route=%+v", result)
	}
	if len(calls) != 2 || calls[0] != "final" || calls[1] != "backup" {
		t.Fatalf("wrong route calls=%v", calls)
	}
	if a := restored.TaskAssignment(config.TaskWatchdog); a.Destination != config.DestinationLocal || a.Quality != config.CostStandard {
		t.Fatalf("saved intent changed: %+v", a)
	}
}
