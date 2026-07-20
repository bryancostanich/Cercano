package builtins

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

type fakeResearchModel struct{}

func (fakeResearchModel) Call(context.Context, string) (string, error) { return "ok", nil }

func TestActivityModelCallerEmitsProgressAroundLocalCalls(t *testing.T) {
	var events []agenttools.ProgressEvent
	call := &capabilities.Call{EmitProgress: func(ev agenttools.ProgressEvent) { events = append(events, ev) }}
	model := &activityModelCaller{inner: fakeResearchModel{}, call: call, activityID: "activity:deep_research:1"}
	out, err := model.Call(context.Background(), "prompt")
	if err != nil || out != "ok" {
		t.Fatalf("Call() = %q, %v", out, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if !strings.Contains(events[0].Text, "local analysis call 1") || !strings.Contains(events[1].Text, "complete") {
		t.Fatalf("unexpected progress text: %+v", events)
	}
	for _, ev := range events {
		if ev.SubAgentID != "activity:deep_research:1" || ev.Kind != "progress" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	}
}

func TestEmitDeepResearchActivityUsesStructuredChildProgress(t *testing.T) {
	var got agenttools.ProgressEvent
	call := &capabilities.Call{
		ConversationID: "parent-1",
		EmitProgress: func(ev agenttools.ProgressEvent) {
			got = ev
		},
	}

	emitDeepResearchActivity(call, "activity:deep_research:1", "started", "deep research start")

	if got.SubAgentID != "activity:deep_research:1" {
		t.Fatalf("SubAgentID = %q", got.SubAgentID)
	}
	if got.SubAgentParentID != "parent-1" {
		t.Fatalf("SubAgentParentID = %q", got.SubAgentParentID)
	}
	if got.SubAgentTitle != "research" {
		t.Fatalf("SubAgentTitle = %q", got.SubAgentTitle)
	}
	if got.Kind != "started" || got.Summary != "deep research start" || got.ToolName != "deep_research" {
		t.Fatalf("unexpected progress event: %+v", got)
	}
}
