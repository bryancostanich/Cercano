package ui

import (
	"errors"
	"reflect"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// streamMsgToEvent is the pure StreamMsg → event map. Each StreamMsg type maps
// to exactly one agent-agnostic chat event with its payload transposed.
func TestStreamMsgToEvent(t *testing.T) {
	bashErr := errors.New("boom")
	cases := []struct {
		name string
		sm   agentclient.StreamMsg
		want interface{}
	}{
		{
			"token",
			agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "Hi"},
			chatAssistantDeltaMsg{token: "Hi"},
		},
		{
			"progress",
			agentclient.StreamMsg{Type: agentclient.TypeProgress, Note: "searching"},
			chatProgressMsg{note: normalizeProgress("searching")},
		},
		{
			"route",
			agentclient.StreamMsg{Type: agentclient.TypeRouteSelected, RouteModel: "claude", RouteCloud: true},
			chatStatusMsg{model: "claude", cloud: true},
		},
		{
			"tool-start",
			agentclient.StreamMsg{Type: agentclient.TypeToolUseStart, ToolUseID: "t1", ToolName: "Bash"},
			toolEntryStartMsg{id: "t1", name: "Bash"},
		},
		{
			"tool-stop",
			agentclient.StreamMsg{Type: agentclient.TypeToolUseStop, ToolUseID: "t1", ArgsSummary: "x"},
			toolEntryStopMsg{id: "t1", argsSummary: "x"},
		},
		{
			"exec-start",
			agentclient.StreamMsg{Type: agentclient.TypeToolExecStart, ToolUseID: "t1"},
			toolEntryExecStartMsg{id: "t1"},
		},
		{
			"exec-complete",
			agentclient.StreamMsg{Type: agentclient.TypeToolExecComplete, ToolUseID: "t1", Detail: "d", Summary: "s", IsError: true},
			toolEntryExecCompleteMsg{id: "t1", detail: "d", summary: "s", isError: true},
		},
		{
			"done",
			agentclient.StreamMsg{Type: agentclient.TypeDone, Final: "done", TokIn: 1, TokOut: 2, Notice: "n", Model: "m"},
			chatDoneMsg{text: "done", tokIn: 1, tokOut: 2, notice: "n", model: "m"},
		},
		{
			"permission",
			agentclient.StreamMsg{Type: agentclient.TypePermissionRequired, ToolUseID: "t1", ToolName: "Bash", ArgsJSON: "{}", Tier: "X"},
			permissionRequiredMsg{id: "t1", name: "Bash", argsJSON: "{}", tier: "X"},
		},
		{
			"task-change",
			agentclient.StreamMsg{Type: agentclient.TypeTaskChange, TaskChangeKind: "updated", Task: &agentclient.TaskNode{ID: "task-1", Title: "Do the thing"}},
			taskChangeMsg{kind: "updated", task: &agentclient.TaskNode{ID: "task-1", Title: "Do the thing"}},
		},
		{
			"error",
			agentclient.StreamMsg{Type: agentclient.TypeError, Err: bashErr},
			chatErrorMsg{err: bashErr},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := streamMsgToEvent(tc.sm)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("streamMsgToEvent(%s) = %#v, want %#v", tc.name, got, tc.want)
			}
		})
	}
}
