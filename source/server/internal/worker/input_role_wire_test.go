package worker_test

// Input-role wire propagation: the host stamps StartTurn.InputRole (the author
// role for persistence — "system" for host-generated autonomous-continuation
// inputs, empty for human turns), and the worker must forward it into the
// runner Request so core.RunTurn persists the input turn with that exact role
// (runner/core.go: persist the user turn before calling the model, role =
// req.InputRole or the human default). This pins that a host continuation
// reaches the child as a system-authored turn and comes back over the persist
// wire with the role intact — MarshalMessage/UnmarshalMessage (wire.go) must
// never flatten it to user.

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/worker"
	proto "cercano/source/server/pkg/proto"
)

func TestWorkerRunTurn_InputRolePropagatesToPersistWire(t *testing.T) {
	cases := []struct {
		name         string
		inputRole    string
		wantInputMsg llm.Role
	}{
		{
			// Host-generated autonomous-continuation input: the runner must
			// persist it as system so history never impersonates a human.
			name:         "system-role host continuation",
			inputRole:    "system",
			wantInputMsg: llm.RoleSystem,
		},
		{
			// Normal human turn: no InputRole, runner defaults to user.
			name:         "empty role defaults to user",
			inputRole:    "",
			wantInputMsg: llm.RoleUser,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const inputText = "keep working on the run"
			provSvc := &fakeResolver{prov: &fixedProvider{text: "ok"}}
			toolSvc := &fakeToolSvc{reg: agenttools.NewRegistry()}

			lis := bufconn.Listen(1 << 20)
			grpcSrv := grpc.NewServer()
			ws := worker.NewWithFactories(
				func(_ *proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
				func(_ *proto.StartTurn) (runner.ToolSvc, error) { return toolSvc, nil },
			)
			proto.RegisterWorkerServer(grpcSrv, ws)
			go func() { _ = grpcSrv.Serve(lis) }()
			defer grpcSrv.Stop()

			conn, err := grpc.NewClient("passthrough:///bufconn",
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return lis.DialContext(ctx)
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if err != nil {
				t.Fatalf("grpc.NewClient: %v", err)
			}
			defer conn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := proto.NewWorkerClient(conn).RunTurn(ctx)
			if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}
			if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: &proto.StartTurn{
				ConversationId: "conv-input-role",
				Input:          inputText,
				InputRole:      tc.inputRole,
				WorkDir:        t.TempDir(),
				Config:         &proto.ConfigSnapshot{LocusMode: "open_primary"},
			}}}); err != nil {
				t.Fatalf("Send StartTurn: %v", err)
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatalf("CloseSend: %v", err)
			}

			var gotDone *proto.TurnDone
			var persistedInputRole, persistedAssistantRole llm.Role
			sawInput, sawAssistant := false, false
			for {
				msg, err := stream.Recv()
				if err != nil {
					t.Fatalf("Recv: %v", err)
				}
				switch m := msg.Msg.(type) {
				case *proto.WorkerToHost_Persist:
					pm, err := worker.UnmarshalMessage(m.Persist.GetMessage())
					if err != nil {
						t.Fatalf("UnmarshalMessage: %v", err)
					}
					if pm.Role == llm.RoleUser || pm.Role == llm.RoleSystem {
						// The first persisted turn is the input message.
						if !sawInput {
							persistedInputRole = pm.Role
							sawInput = true
						}
					} else if pm.Role == llm.RoleAssistant && !sawAssistant {
						persistedAssistantRole = pm.Role
						sawAssistant = true
					}
				case *proto.WorkerToHost_Event:
					// drain; nothing to assert here
				case *proto.WorkerToHost_Error:
					t.Fatalf("got TurnError: %s", m.Error.GetMessage())
				case *proto.WorkerToHost_Done:
					gotDone = m.Done
				}
				if gotDone != nil {
					break
				}
			}
			if gotDone == nil {
				t.Fatal("never received TurnDone")
			}
			if !sawInput {
				t.Fatal("no input-turn Persist message observed over the wire")
			}
			if persistedInputRole != tc.wantInputMsg {
				t.Errorf("persisted input role = %q, want %q (StartTurn.InputRole=%q)",
					persistedInputRole, tc.wantInputMsg, tc.inputRole)
			}
			if !sawAssistant || persistedAssistantRole != llm.RoleAssistant {
				t.Errorf("assistant turn role lost: sawAssistant=%v role=%q", sawAssistant, persistedAssistantRole)
			}
		})
	}
}
