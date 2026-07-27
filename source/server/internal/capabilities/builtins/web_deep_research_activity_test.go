package builtins

import (
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/research"
)

func TestFormatDeepResearchProgressIncludesPhaseStepCounts(t *testing.T) {
	got := formatDeepResearchProgress(research.ProgressState{
		Phase:            "Analyzing",
		Step:             "Finding 3 of 12: ATIF v1.7 schema",
		Current:          2,
		Total:            12,
		FindingsAccepted: 5,
	})
	for _, want := range []string{"Analyzing 2/12", "Finding 3 of 12", "5 findings accepted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatDeepResearchProgress() = %q, missing %q", got, want)
		}
	}
}

func TestActivityReporterEmitsStructuredChildProgress(t *testing.T) {
	var events []agenttools.ProgressEvent
	call := &capabilities.Call{
		ConversationID: "parent-1",
		EmitProgress: func(ev agenttools.ProgressEvent) {
			events = append(events, ev)
		},
	}

	r := newActivityReporter(call, "deep_research", "research")
	r.Started("deep research start")
	r.Prompt("Topic: x")
	r.Progress("planning…")
	r.Done("complete")

	if len(events) != 4 {
		t.Fatalf("expected 4 lifecycle events, got %+v", events)
	}
	if !strings.HasPrefix(r.id, "activity:deep_research:") {
		t.Fatalf("activity id = %q, want activity:deep_research: prefix", r.id)
	}
	for _, ev := range events {
		if ev.SubAgentID != r.id {
			t.Fatalf("SubAgentID = %q, want %q", ev.SubAgentID, r.id)
		}
		if ev.SubAgentParentID != "parent-1" || ev.SubAgentTitle != "research" || ev.ToolName != "deep_research" {
			t.Fatalf("unexpected event fields: %+v", ev)
		}
	}
	if events[0].Kind != "started" || events[1].Kind != "prompt" || events[2].Kind != "progress" || events[3].Kind != "done" {
		t.Fatalf("unexpected event kinds: %+v", events)
	}
}

func TestActivityReporterFailedMarksError(t *testing.T) {
	var got agenttools.ProgressEvent
	call := &capabilities.Call{EmitProgress: func(ev agenttools.ProgressEvent) { got = ev }}
	r := newActivityReporter(call, "research", "research")
	r.Failed(errTestActivity)
	if got.Kind != "error" || !got.IsError || !strings.Contains(got.Summary, "research failed:") {
		t.Fatalf("unexpected error event: %+v", got)
	}
}

var errTestActivity = errTest("boom")

type errTest string

func (e errTest) Error() string { return string(e) }
