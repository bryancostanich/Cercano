package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/llm"
)

// captureLog redirects the stdlib logger for the duration of fn and returns
// what was written.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// TestGrantedRegistry_LogsSuccessfulGrant verifies the success path is no
// longer silent: a granted catalog logs the resolved tool names, and any
// prefix-normalized names are called out. Absence of dispatch log lines must
// never again be mistakable for "no dispatch happened".
func TestGrantedRegistry_LogsSuccessfulGrant(t *testing.T) {
	srv := buildPermsServer(t)

	out := captureLog(t, func() {
		if _, _, _, err := srv.grantedRegistry([]string{"mcp__oc__r_read"}); err != nil {
			t.Fatalf("grantedRegistry: %v", err)
		}
	})

	if !strings.Contains(out, "subagent grant: granted") {
		t.Errorf("expected a success grant log line, got: %q", out)
	}
	if !strings.Contains(out, "r_read") {
		t.Errorf("expected granted tool name in log, got: %q", out)
	}
	if !strings.Contains(out, "mcp__oc__r_read→r_read") {
		t.Errorf("expected normalization mapping in log, got: %q", out)
	}
}

// observabilityDispatchRig builds a server wired with a persistent store, a
// stub R-tier tool, permissions, and a scripted provider that calls the tool
// then answers "done".
func observabilityDispatchRig(t *testing.T) (*Server, *scriptedProvider) {
	t.Helper()
	srv, _ := newServerWithStore(t)

	reg := agenttools.NewRegistry()
	reg.MustRegister(stubDispatchTool{name: "r_read", perm: agenttools.PermR, retText: "r-result"})
	srv.SetToolRegistry(reg)

	perms, err := agent.LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err != nil {
		t.Fatalf("LoadPermissionStore: %v", err)
	}
	srv.SetPermissions(perms, nil)

	prov := &scriptedProvider{
		caps: llm.Capabilities{SupportsTools: true},
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "tu1", ToolName: "r_read", ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
	}
	return srv, prov
}

// TestRunAgenticDispatch_PersistsSubagentLoop verifies the sub-agent's whole
// loop lands in the conversation store: a subagent-kind conversation linked to
// the parent, holding the task, the tool_use turn, the tool_result turn, and
// the final answer — so failed dispatches can be post-mortemed from the DB.
func TestRunAgenticDispatch_PersistsSubagentLoop(t *testing.T) {
	srv, prov := observabilityDispatchRig(t)
	store := srv.agent.PersistentStore()

	res, err := srv.runAgenticDispatch(context.Background(),
		dispatch.Spec{Mode: dispatch.Agentic, Task: "probe task", ConversationID: "parent-conv"},
		dispatch.Selection{Provider: prov}, "test-model")
	if err != nil {
		t.Fatalf("runAgenticDispatch: %v", err)
	}
	if res.SubConversationID == "" {
		t.Fatal("expected Result.SubConversationID to be set when a store is available")
	}

	ctx := context.Background()
	info, err := store.Get(ctx, res.SubConversationID)
	if err != nil {
		t.Fatalf("Get(%s): %v", res.SubConversationID, err)
	}
	if info.Kind != "subagent" {
		t.Errorf("Kind = %q, want subagent", info.Kind)
	}
	if info.ParentID != "parent-conv" {
		t.Errorf("ParentID = %q, want parent-conv", info.ParentID)
	}

	turns, err := store.GetTurns(ctx, res.SubConversationID)
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if len(turns) < 4 {
		t.Fatalf("expected >= 4 persisted turns (task, tool_use, tool_result, answer), got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Content != "probe task" {
		t.Errorf("first turn = %q/%q, want user/\"probe task\"", turns[0].Role, turns[0].Content)
	}
	var sawToolUse, sawToolResult bool
	for _, tn := range turns {
		if strings.Contains(tn.BlocksJSON, `"tool_use"`) {
			sawToolUse = true
		}
		if strings.Contains(tn.BlocksJSON, `"tool_result"`) {
			sawToolResult = true
		}
	}
	if !sawToolUse {
		t.Error("no persisted turn carries a tool_use block")
	}
	if !sawToolResult {
		t.Error("no persisted turn carries a tool_result block")
	}
	last := turns[len(turns)-1]
	if last.Role != "assistant" || last.Content != "done" {
		t.Errorf("last turn = %q/%q, want assistant/\"done\"", last.Role, last.Content)
	}
}

// TestRunAgenticDispatch_LogsStartAndDone verifies the dispatch lifecycle is
// visible in the agent log with the sub-conversation id for cross-referencing.
func TestRunAgenticDispatch_LogsStartAndDone(t *testing.T) {
	srv, prov := observabilityDispatchRig(t)

	out := captureLog(t, func() {
		if _, err := srv.runAgenticDispatch(context.Background(),
			dispatch.Spec{Mode: dispatch.Agentic, Task: "probe task"},
			dispatch.Selection{Provider: prov}, "test-model"); err != nil {
			t.Fatalf("runAgenticDispatch: %v", err)
		}
	})

	if !strings.Contains(out, "subagent start: conv=") {
		t.Errorf("expected start log with conv id, got: %q", out)
	}
	if !strings.Contains(out, "subagent done: conv=") {
		t.Errorf("expected done log with conv id, got: %q", out)
	}
	if !strings.Contains(out, "model=test-model") {
		t.Errorf("expected model in start log, got: %q", out)
	}
}

// TestRunAgenticDispatch_EmitsProgress verifies sub-agent lifecycle and child
// tool-loop progress can be routed back to the parent turn.
func TestRunAgenticDispatch_EmitsProgress(t *testing.T) {
	srv, prov := observabilityDispatchRig(t)
	var notes []string

	_, err := srv.runAgenticDispatch(context.Background(),
		dispatch.Spec{Mode: dispatch.Agentic, Task: "probe task", Emit: func(ev agenttools.ProgressEvent) { notes = append(notes, ev.Text) }},
		dispatch.Selection{Provider: prov}, "test-model")
	if err != nil {
		t.Fatalf("runAgenticDispatch: %v", err)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{
		// grant/ignored no longer emit as separate progress lines; the toolset
		// rides on the "started" event (text includes tools=..., and the
		// structured event carries GrantedTools) so it lands in the sub tab.
		"sub-agent start:",
		"sub-agent planned tool: r_read",
		"sub-agent running tool: r_read",
		"sub-agent tool complete: r_read",
		"sub-agent done:",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress missing %q in:\n%s", want, joined)
		}
	}
}

// TestRunAgenticDispatch_NoStoreStillWorks pins the degraded path: with no
// persistent store wired (s.agent nil), dispatch runs fine and simply skips
// persistence, leaving SubConversationID empty.
func TestRunAgenticDispatch_NoStoreStillWorks(t *testing.T) {
	rCalled := false
	reg := agenttools.NewRegistry()
	reg.MustRegister(stubDispatchTool{name: "r_read", perm: agenttools.PermR, called: &rCalled, retText: "r-result"})

	srv := NewServer(nil, nil, nil, nil, nil, nil)
	srv.SetToolRegistry(reg)
	perms, err := agent.LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err != nil {
		t.Fatalf("LoadPermissionStore: %v", err)
	}
	srv.SetPermissions(perms, nil)

	prov := &scriptedProvider{
		caps: llm.Capabilities{SupportsTools: true},
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "tu1", ToolName: "r_read", ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
	}

	res, err := srv.runAgenticDispatch(context.Background(),
		dispatch.Spec{Mode: dispatch.Agentic, Task: "probe task"},
		dispatch.Selection{Provider: prov}, "test-model")
	if err != nil {
		t.Fatalf("runAgenticDispatch: %v", err)
	}
	if !rCalled {
		t.Error("expected the stub tool to run")
	}
	if res.SubConversationID != "" {
		t.Errorf("SubConversationID = %q, want empty with no store", res.SubConversationID)
	}
}
