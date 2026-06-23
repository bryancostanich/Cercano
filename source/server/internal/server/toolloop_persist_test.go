package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/pkg/config"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeStream implements proto.Agent_StreamProcessRequestServer (which is
// grpc.ServerStreamingServer[StreamProcessResponse]) for tests. Only Context
// and Send are exercised by streamProcessRequestWithToolLoop.
type fakeStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*proto.StreamProcessResponse
}

func (f *fakeStream) Context() context.Context                  { return f.ctx }
func (f *fakeStream) Send(m *proto.StreamProcessResponse) error { f.sent = append(f.sent, m); return nil }
func (f *fakeStream) SetHeader(metadata.MD) error               { return nil }
func (f *fakeStream) SendHeader(metadata.MD) error              { return nil }
func (f *fakeStream) SetTrailer(metadata.MD)                    {}
func (f *fakeStream) SendMsg(m interface{}) error               { return nil }
func (f *fakeStream) RecvMsg(m interface{}) error               { return nil }

// scriptedProvider is a minimal llm.Provider for tests — replays a queue of
// block sequences as successive Chat responses.
type scriptedProvider struct {
	scripts [][]llm.Block
	calls   int
	caps    llm.Capabilities
}

func (p *scriptedProvider) Name() string                   { return "scripted" }
func (p *scriptedProvider) Capabilities() llm.Capabilities { return p.caps }
func (p *scriptedProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	out := llm.ChatResponse{Blocks: p.scripts[p.calls]}
	p.calls++
	return out, nil
}
func (p *scriptedProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if p.calls >= len(p.scripts) {
		return nil, &scriptExhaustedErr{call: p.calls}
	}
	blocks := p.scripts[p.calls]
	p.calls++
	return &scriptedStream{events: blocksToEvents(blocks)}, nil
}

type scriptExhaustedErr struct{ call int }

func (e *scriptExhaustedErr) Error() string {
	return "scriptedProvider: no script for call"
}

type scriptedStream struct {
	events []llm.StreamEvent
	idx    int
}

func (s *scriptedStream) Next() (llm.StreamEvent, bool, error) {
	if s.idx >= len(s.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, true, nil
}

func (s *scriptedStream) Close() error { return nil }

func blocksToEvents(blocks []llm.Block) []llm.StreamEvent {
	events := []llm.StreamEvent{{Type: llm.EventMessageStart}}
	for _, b := range blocks {
		switch b.Type {
		case llm.BlockText:
			events = append(events, llm.StreamEvent{
				Type: llm.EventTextDelta, TextDelta: b.Text,
			})
		case llm.BlockToolUse:
			events = append(events, llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: b.ToolUseID, ToolName: b.ToolName,
			})
			events = append(events, llm.StreamEvent{
				Type: llm.EventToolUseInputDelta, TextDelta: string(b.ToolInput),
			})
			events = append(events, llm.StreamEvent{Type: llm.EventToolUseStop})
		}
	}
	events = append(events, llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn"})
	return events
}

func newServerWithStore(t *testing.T) (*Server, conversation.Store) {
	t.Helper()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	a := agent.NewAgent(&mockRouter{}, &mockCoordinator{}, agent.WithPersistentStore(store))
	srv := NewServer(a, nil, nil, nil, nil, nil)
	srv.SetConfigPersistence("", config.Config{CloudModel: "test-model"})
	srv.SetToolRegistry(agenttools.DefaultRegistry())
	return srv, store
}

// TestStreamToolLoop_PersistsTurns verifies F1: after RunToolLoop returns, the
// user turn and assistant turn(s) are written to the persistent store so
// /history and /resume cover tool-calling conversations.
func TestStreamToolLoop_PersistsTurns(t *testing.T) {
	srv, store := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockText, Text: "Hi there."},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{
		Input:          "hello",
		ConversationId: "conv-1",
		WorkDir:        "",
	}
	if err := srv.streamProcessRequestWithToolLoop(req, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	turns, err := store.GetTurns(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns (user + assistant), got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Content != "hello" {
		t.Errorf("user turn: got role=%q content=%q", turns[0].Role, turns[0].Content)
	}
	if turns[1].Role != "assistant" {
		t.Errorf("assistant turn role: %q", turns[1].Role)
	}
	if turns[1].Content != "Hi there." {
		t.Errorf("assistant text: %q", turns[1].Content)
	}
	if turns[1].BlocksJSON == "" {
		t.Error("assistant BlocksJSON should not be empty")
	}
	var blocks []llm.Block
	if err := json.Unmarshal([]byte(turns[1].BlocksJSON), &blocks); err != nil {
		t.Errorf("BlocksJSON unmarshal: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != llm.BlockText {
		t.Errorf("decoded blocks: %+v", blocks)
	}
}

// TestStreamToolLoop_PersistsMultiTurnHistory verifies that intermediate
// assistant messages (tool_use round trips) are persisted too — every
// assistant turn in result.History gets written.
func TestStreamToolLoop_PersistsMultiTurnHistory(t *testing.T) {
	srv, store := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS",
				ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "All done."}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{
		Input:          "list",
		ConversationId: "conv-2",
	}
	if err := srv.streamProcessRequestWithToolLoop(req, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	turns, err := store.GetTurns(context.Background(), "conv-2")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	// 1 user + 2 assistant (tool_use turn + final text turn).
	if len(turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" {
		t.Errorf("turn 0 role: %q", turns[0].Role)
	}
	if turns[1].Role != "assistant" || turns[1].BlocksJSON == "" {
		t.Errorf("turn 1 should be assistant with blocks, got role=%q blocks=%q", turns[1].Role, turns[1].BlocksJSON)
	}
	if turns[2].Role != "assistant" || turns[2].Content != "All done." {
		t.Errorf("turn 2: role=%q content=%q", turns[2].Role, turns[2].Content)
	}
}

// TestStreamToolLoop_NoConversationID_SkipsPersist verifies turns are NOT
// written when the client didn't supply a conversation id — matches legacy
// behavior (storeConversationTurn early-returns on empty id).
func TestStreamToolLoop_NoConversationID_SkipsPersist(t *testing.T) {
	srv, store := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hi"}}},
		caps:    llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{Input: "x"}
	if err := srv.streamProcessRequestWithToolLoop(req, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	infos, err := store.List(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected no conversations persisted, got %d", len(infos))
	}
}

// TestStreamToolLoop_PropagatesWorkDir verifies F2: req.WorkDir becomes the
// process cwd for the duration of RunToolLoop, so tools that read os.Getwd
// see the client-supplied project directory, and the prior cwd is restored
// after the call returns.
func TestStreamToolLoop_PropagatesWorkDir(t *testing.T) {
	srv, _ := newServerWithStore(t)

	tmp := t.TempDir()
	// macOS /var is a symlink to /private/var; resolve so the comparison is
	// against the canonical path that os.Getwd reports.
	wantWd, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	var observed string
	observerTool := &cwdObserver{out: &observed}
	reg := agenttools.NewRegistry()
	reg.MustRegister(observerTool)
	srv.SetToolRegistry(reg)

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "observe_cwd",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	priorWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	stream := &fakeStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{
		Input:          "x",
		WorkDir:        tmp,
		ConversationId: "conv-wd",
	}
	if err := srv.streamProcessRequestWithToolLoop(req, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	// Tool saw the requested WorkDir.
	if !strings.HasPrefix(observed, wantWd) {
		t.Errorf("tool observed cwd=%q, want prefix=%q", observed, wantWd)
	}

	// Prior cwd restored after the helper returned.
	afterWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after: %v", err)
	}
	if afterWd != priorWd {
		t.Errorf("cwd not restored: got %q, want %q", afterWd, priorWd)
	}
}

// cwdObserver is a test-only tool that records os.Getwd() at execution time.
type cwdObserver struct {
	out *string
}

func (cwdObserver) Name() string                      { return "observe_cwd" }
func (cwdObserver) Description() string               { return "records cwd for tests" }
func (cwdObserver) Permission() agenttools.Permission { return agenttools.PermR }
func (cwdObserver) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (o cwdObserver) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	wd, _ := os.Getwd()
	*o.out = wd
	return agenttools.NewTextResult("ok"), nil
}
