package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
)

// syncStream is a mutex-guarded fakeStream: the turn goroutine keeps sending
// (the permission prompt arrives mid-turn) while the test goroutine inspects
// what has been sent so far, so every read must be synchronized.
type syncStream struct {
	grpc.ServerStream
	ctx  context.Context
	mu   sync.Mutex
	sent []*proto.StreamProcessResponse
}

func (f *syncStream) Context() context.Context { return f.ctx }
func (f *syncStream) Send(m *proto.StreamProcessResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}
func (f *syncStream) snapshot() []*proto.StreamProcessResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*proto.StreamProcessResponse(nil), f.sent...)
}

// gatedExecTool is a test-only X-tier tool: under the permissive permission
// store an X-tier call raises a permission prompt, so executing it requires a
// human decision at the permBroker barrier.
type gatedExecTool struct {
	mu    sync.Mutex
	execs int
}

func (g *gatedExecTool) Name() string                      { return "gated_work" }
func (g *gatedExecTool) Description() string               { return "X-tier work tool for tests" }
func (g *gatedExecTool) Permission() agenttools.Permission { return agenttools.PermX }
func (g *gatedExecTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (g *gatedExecTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	g.mu.Lock()
	g.execs++
	g.mu.Unlock()
	return agenttools.NewTextResult("gated work done"), nil
}

func (g *gatedExecTool) execCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.execs
}

// TestStreamToolLoop_AutonomousContinuation_WaitsOnPendingPermission pins the
// permission-barrier semantics end-to-end: an autonomous chain turn that hits
// a permission-gated tool call BLOCKS on the pending prompt — it neither
// executes the tool, nor completes the turn, nor chains a continuation while
// the prompt is unresolved. The structured PermissionRequired event reaches
// the open stream; resolving it (allow) resumes the turn, the tool executes,
// and the host chains the continuation turn normally.
func TestStreamToolLoop_AutonomousContinuation_WaitsOnPendingPermission(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-permwait", "running")
	// The continuation turn ends the run so the chain deterministically stops
	// after exactly one continuation.
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-permwait", state: "review_pending"}
	gated := &gatedExecTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(gated)
	reg.MustRegister(completer)
	srv.SetToolRegistry(reg)

	// A real permission broker with a live PendingDecisions barrier: without a
	// pending barrier the requester short-circuits (deny) instead of waiting.
	srv.SetPermissions(agent.NewStaticPermissionStore(agent.ModePermissive), agent.NewPendingDecisions())

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "perm-u1", ToolName: "gated_work", ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "turn one done."}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "complete_run", ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "turn two done."}},
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &syncStream{ctx: context.Background()}
	done := make(chan error, 1)
	go func() {
		done <- srv.streamProcessRequestWithToolLoop(
			&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-permwait"}, stream)
	}()

	// Wait for the structured PermissionRequired prompt to reach the stream.
	var prompt *proto.PermissionRequired
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, m := range stream.snapshot() {
			if pr := m.GetPermissionRequired(); pr != nil && pr.GetToolName() == "gated_work" {
				prompt = pr
			}
		}
		if prompt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the PermissionRequired event on the stream")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The prompt is structured: tool id, name, and tier are all carried.
	if prompt.GetToolUseId() != "perm-u1" {
		t.Errorf("PermissionRequired.ToolUseId = %q, want perm-u1", prompt.GetToolUseId())
	}
	if prompt.GetTier() != string(llm.PermX) {
		t.Errorf("PermissionRequired.Tier = %q, want X", prompt.GetTier())
	}

	// While the prompt is pending the turn is genuinely blocked: the gated tool
	// has not executed, no final response has been sent, and no continuation
	// was announced.
	if got := gated.execCount(); got != 0 {
		t.Fatalf("gated tool executed %d time(s) while the permission prompt was still pending", got)
	}
	if frs := finalResponses(stream.snapshot()); len(frs) != 0 {
		t.Fatalf("FinalResponse sent while permission pending: %d", len(frs))
	}
	for _, n := range progressNotes(stream.snapshot()) {
		if strings.HasPrefix(n, "autonomous continuation:") {
			t.Fatalf("continuation announced while permission pending: %q", n)
		}
	}

	// Resolve the prompt: the turn resumes, executes the tool, completes, and
	// the host chains the autonomous continuation turn.
	if !srv.permBroker.Resolve("conv-permwait", "perm-u1", agent.Decision{Allow: true, Persist: true}) {
		t.Fatal("Resolve found no pending waiter — the turn was not waiting on the permission barrier")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("turn did not complete after the permission was resolved (barrier deadlock)")
	}

	// The allowed tool executed exactly once, inside the first turn.
	if got := gated.execCount(); got != 1 {
		t.Fatalf("gated tool executions after resolve = %d, want 1", got)
	}
	if prov.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (turn 1's two rounds + continuation's two)", prov.calls)
	}
	frs := finalResponses(stream.snapshot())
	if len(frs) != 2 {
		t.Fatalf("FinalResponse count = %d, want 2 (initial + continuation)", len(frs))
	}
	if frs[0].GetOutput() != "turn one done." || frs[1].GetOutput() != "turn two done." {
		t.Errorf("final outputs = %q, %q", frs[0].GetOutput(), frs[1].GetOutput())
	}
	// The continuation turn ran (run flipped to review_pending by its tool).
	got, err := store.GetActiveAutonomyRun(context.Background(), "conv-permwait")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.RunID != run.RunID || got.State != "review_pending" {
		t.Fatalf("run after chain = %q state=%q, want same run in review_pending", got.RunID, got.State)
	}
}
