package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type sharedSkillProbe struct {
	fixedProvider
	id, version, content string
	calls                int
}

func (p *sharedSkillProbe) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	if _, ok := managedsettings.FromContext(ctx); !ok {
		return nil, fmt.Errorf("worker lost pinned settings context")
	}
	if p.id == "" {
		if strings.Contains(req.System, "enterprise/") {
			return nil, fmt.Errorf("removed skills leaked into warm worker prompt")
		}
		return &echoReader{text: "no skills assigned"}, nil
	}
	if !strings.Contains(req.System, p.id) || !strings.Contains(req.System, `"Version":"`+p.version+`"`) {
		return nil, fmt.Errorf("worker prompt missing assigned skill metadata")
	}
	if p.calls == 1 {
		args, _ := json.Marshal(map[string]string{"id": p.id})
		return &echoReader{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart}, {Type: llm.EventToolUseStart, ToolName: "get_shared_skill", ToolUseID: "skill"},
			{Type: llm.EventToolUseInputDelta, TextDelta: string(args)}, {Type: llm.EventToolUseStop}, {Type: llm.EventMessageStop, StopReason: "tool_use"},
		}}, nil
	}
	data, _ := json.Marshal(req.Messages)
	if !strings.Contains(string(data), p.content) {
		return nil, fmt.Errorf("shared-skill text never reached model continuation")
	}
	return &echoReader{text: "used " + p.content}, nil
}
func TestManagedWorkerLoadsPinnedSharedSkill(t *testing.T) {
	ws := worker.NewWithFactories(func(start *proto.StartTurn) (providers.Resolver, error) {
		snapshot, err := managedsettings.Decode(start.GetEnterpriseSettingsJson())
		if err != nil {
			return nil, err
		}
		probe := &sharedSkillProbe{}
		if len(snapshot.Skills) > 0 {
			sk := snapshot.Skills[0]
			probe.id = managedsettings.SkillID(sk.ID)
			probe.version = sk.Version
			probe.content = sk.Content
		}
		return &echoResolver{prov: probe}, nil
	}, func(*proto.StartTurn) (runner.ToolSvc, error) {
		reg := agenttools.NewRegistry()
		reg.MustRegister(agentadapter.AsTool(builtins.GetSharedSkill(), "", capabilities.Services{}))
		return &hostTestToolSvc{reg: reg}, nil
	})
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, ws)
	go server.Serve(listener)
	defer server.Stop()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
	for i, org := range []string{"company-a", "company-b", "company-a"} {
		snapshot := settingstest.Snapshot(org, fmt.Sprint(i+1), "unique instruction "+org)
		want := "used " + snapshot.Skills[0].Content
		if i == 2 {
			snapshot.Skills = nil
			snapshot.Policy.Skills = []v1.SkillAssignment{}
			want = "no skills assigned"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ctx = managedsettings.WithSnapshot(ctx, snapshot)
		ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
		result, err := host.RunTurn(ctx, runner.Request{ConversationID: "skills", Gen: uint64(i + 1), Input: "Apply the review skill."}, nil, nil, nil)
		cancel()
		if err != nil || result.FinalText != want {
			t.Fatalf("turn %d: result=%q err=%v", i, result.FinalText, err)
		}
	}
}

// A previous worker can enforce model allow-lists but does not consume settings.
// The host must reject it, not silently lose managed defaults or skill versions.
type policyOnlyWorker struct {
	proto.UnimplementedWorkerServer
	calls atomic.Int32
}

type selectionProbe struct{ fixedProvider }

func (*selectionProbe) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return &echoReader{text: "selected " + req.Model}, nil
}

func TestManagedWorkerPreservesExplicitModelSelection(t *testing.T) {
	ws := worker.NewWithFactories(func(*proto.StartTurn) (providers.Resolver, error) {
		return &echoResolver{prov: &selectionProbe{}}, nil
	}, func(*proto.StartTurn) (runner.ToolSvc, error) {
		return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
	})
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, ws)
	go server.Serve(listener)
	defer server.Stop()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
	snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
	alternate := snapshot.Policy.AllowedRoutes[0]
	alternate.ID, alternate.Model = "alternate", "alternate"
	snapshot.Policy.AllowedRoutes = append(snapshot.Policy.AllowedRoutes, alternate)
	snapshot.Policy.TaskDefaults[0].AllowDeveloperOverride = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = managedsettings.WithSnapshot(ctx, snapshot)
	ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	result, err := host.RunTurn(ctx, runner.Request{ConversationID: "explicit-model", Input: "Hello", ModelOverride: "alternate"}, nil, nil, nil)
	if err != nil || result.FinalText != "selected alternate" {
		t.Fatalf("worker lost explicit choice: result=%q err=%v", result.FinalText, err)
	}
}

// A settings-aware older worker would ignore an unknown model_override field.
// The selection RPC makes that compatibility boundary explicit.
type settingsOnlyWorker struct {
	proto.UnimplementedWorkerServer
	calls atomic.Int32
}

func (w *settingsOnlyWorker) RunManagedTurnWithSettings(proto.Worker_RunManagedTurnWithSettingsServer) error {
	w.calls.Add(1)
	return status.Error(codes.Internal, "must not downgrade")
}

func TestManagedHostRequiresSelectionAwareWorker(t *testing.T) {
	old := &settingsOnlyWorker{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, old)
	go server.Serve(listener)
	defer server.Stop()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = managedsettings.WithSnapshot(ctx, settingstest.Snapshot("company-a", "1", "Review carefully."))
	ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	_, err := host.RunTurn(ctx, runner.Request{ConversationID: "old-worker-selection", Input: "Hello", ModelOverride: "approved"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support enterprise") || old.calls.Load() != 0 {
		t.Fatalf("downgraded explicit choice: err=%v calls=%d", err, old.calls.Load())
	}
}

func (w *policyOnlyWorker) RunManagedTurn(proto.Worker_RunManagedTurnServer) error {
	w.calls.Add(1)
	return status.Error(codes.Internal, "must not downgrade")
}
func TestManagedHostRequiresSettingsAwareWorker(t *testing.T) {
	old := &policyOnlyWorker{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, old)
	go server.Serve(listener)
	defer server.Stop()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	if _, err := host.RunTurn(ctx, runner.Request{ConversationID: "missing-settings"}, nil, nil, nil); !modelpolicy.IsDenial(err) {
		t.Fatalf("missing settings did not fail closed: %v", err)
	}
	ctx = managedsettings.WithSnapshot(ctx, settingstest.Snapshot("company-a", "1", "Review carefully."))
	_, err := host.RunTurn(ctx, runner.Request{ConversationID: "old-worker", Input: "hello"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support enterprise") || old.calls.Load() != 0 {
		t.Fatalf("downgraded settings: err=%v calls=%d", err, old.calls.Load())
	}
}
func TestWorkerRejectsMissingOrInvalidSettingsBeforeBuildingProviders(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(`{}`), []byte(`{"policy":{}}`)} {
		var builds atomic.Int32
		ws := worker.NewWithFactories(func(*proto.StartTurn) (providers.Resolver, error) {
			builds.Add(1)
			return nil, fmt.Errorf("unexpected build")
		}, nil)
		listener := bufconn.Listen(1 << 20)
		server := grpc.NewServer()
		proto.RegisterWorkerServer(server, ws)
		go server.Serve(listener)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := worker.BufconnDial(listener)(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := proto.NewWorkerClient(conn).RunManagedTurnWithSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: &proto.StartTurn{EnterpriseManaged: true, EnterpriseSettingsJson: data}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = stream.Recv()
		conn.Close()
		cancel()
		server.Stop()
		if status.Code(err) != codes.FailedPrecondition || builds.Load() != 0 {
			t.Fatalf("invalid settings reached provider: err=%v builds=%d", err, builds.Load())
		}
	}
}
