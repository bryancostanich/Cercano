package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Exercise the actual adapter and permission gate, not just Execute: even in
// bypass mode completion requires exactly one confirmation, and denial is inert.
func TestAutonomousCompletionSingleConfirmation(t *testing.T) {
	for _, approve := range []bool{false, true} {
		name := "declined"
		if approve {
			name = "approved"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, err := conversation.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv", State: "running", BriefJSON: `{"goal":"ship"}`, ReviewJSON: "{}", DecisionsJSON: "[]"}); err != nil {
				t.Fatal(err)
			}
			profile := "autonomous"
			caps := capabilities.NewRegistry(capabilities.Services{Autonomy: store, EnterProfile: func(_ string, name string) error { profile = name; return nil }})
			caps.MustRegister(builtins.RequestAutonomousExit())
			reg := agentadapter.BuildAgentRegistry(caps, nil, nil)
			prov := &mockProvider{scripts: [][]llm.Block{
				{{Type: llm.BlockToolUse, ToolUseID: "exit", ToolName: "request_autonomous_exit", ToolInput: json.RawMessage(`{"summary":"done","verification":"tests passed"}`)}},
				{{Type: llm.BlockText, Text: "done"}},
			}, caps: inference.Capabilities{SupportsTools: true}}
			perms, err := LoadPermissionStore(t.TempDir() + "/perms.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if err := perms.SetMode(ModeBypass); err != nil {
				t.Fatal(err)
			}
			confirms := 0
			_, err = RunToolLoop(ctx, ToolLoopInput{Provider: prov, Registry: reg, Permissions: perms, ConversationID: "conv", UserInput: "finish", Profile: AutonomousProfile(), PermissionRequester: func(_ context.Context, _, name string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
				if name != "request_autonomous_exit" {
					t.Errorf("unexpected gate %s", name)
				}
				confirms++
				return approve, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := store.GetAutonomyRun(ctx, "conv")
			if err != nil {
				t.Fatal(err)
			}
			wantState, wantProfile := "running", "autonomous"
			if approve {
				wantState, wantProfile = "completed", "default"
			}
			if confirms != 1 || run.State != wantState || profile != wantProfile {
				t.Fatalf("confirmations=%d state=%s profile=%s", confirms, run.State, profile)
			}
		})
	}
}
