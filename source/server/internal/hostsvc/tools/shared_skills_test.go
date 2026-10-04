package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
)

type subagentSkillProvider struct {
	historyProbeProvider
	calls int
}

func (p *subagentSkillProvider) StreamChat(_ context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	if !strings.Contains(r.System, "enterprise/company-a/review") {
		return nil, fmt.Errorf("subagent cannot discover shared skill")
	}
	if p.calls == 1 {
		found := false
		readFound := false
		for _, tool := range r.Tools {
			if tool.Name == "Read" {
				readFound = true
			}
			if tool.Name == "get_shared_skill" {
				found = true
			}
		}
		if !readFound {
			return nil, fmt.Errorf("managed skill reader displaced the normal read-only tool grant")
		}
		if !found {
			return nil, fmt.Errorf("subagent was not granted the skill reader")
		}
		return &sliceStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventToolUseStart, ToolName: "get_shared_skill", ToolUseID: "skill"}, {Type: llm.EventToolUseInputDelta, TextDelta: `{"id":"enterprise/company-a/review"}`}, {Type: llm.EventToolUseStop}, {Type: llm.EventMessageStop, StopReason: "tool_use"}}}, nil
	}
	data, _ := json.Marshal(r.Messages)
	if !strings.Contains(string(data), "Check migration rollback.") {
		return nil, fmt.Errorf("subagent never received skill text")
	}
	return &sliceStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: "The migration rollback needs review."}, {Type: llm.EventMessageStop, StopReason: "end_turn"}}}, nil
}
func TestSubagentLoadsSharedSkillFromParentTurn(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	reg := agenttools.NewRegistry()
	reg.MustRegister(agentadapter.AsTool(builtins.GetSharedSkill(), "", capabilities.Services{}))
	reg.MustRegister(agentadapter.AsTool(builtins.ReadFile(), "Read", capabilities.Services{}))
	svc.SetRegistry(reg)
	ctx := managedsettings.WithSnapshot(t.Context(), settingstest.Snapshot("company-a", "3", "Check migration rollback."))

	for _, grant := range [][]string{nil, {"Read"}, {"unknown-tool"}} {
		provider := &subagentSkillProvider{historyProbeProvider: historyProbeProvider{name: "anthropic"}}
		_, err := svc.RunAgenticDispatch(ctx, dispatch.Spec{Mode: dispatch.Agentic, Task: "Review the migration.", Tools: grant, MaxIterations: 4}, inference.Selection{Provider: provider, IsCloud: true}, "model")
		if len(grant) > 0 && grant[0] == "unknown-tool" {
			if err == nil || provider.calls != 0 {
				t.Fatal("skill reader hid an invalid tool grant")
			}
			continue
		}
		if err != nil || provider.calls != 2 {
			t.Fatalf("grant=%v calls=%d err=%v", grant, provider.calls, err)
		}
	}
}
