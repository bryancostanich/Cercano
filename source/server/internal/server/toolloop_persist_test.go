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
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/pkg/config"
	"cercano/source/server/internal/contextmeter"
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
	seen    [][]llm.Message // req.Messages captured per StreamChat call
	usage   [2]int          // [inputTokens, outputTokens] to emit; zero means skip
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
	p.seen = append(p.seen, req.Messages)
	evs := blocksToEvents(blocks)
	if p.usage[0] != 0 {
		evs[0].InputTokens = p.usage[0]              // EventMessageStart is always events[0]
		evs[len(evs)-1].OutputTokens = p.usage[1]    // EventMessageStop is always the last
	}
	p.calls++
	return &scriptedStream{events: evs}, nil
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

	reg := contextmeter.NewRegistry()
	a := agent.NewAgent(&mockRouter{}, &mockCoordinator{},
		agent.WithPersistentStore(store),
		agent.WithContextMeter(reg, "test-model"),
	)
	srv := NewServer(a, nil, nil, nil, nil, nil)
	srv.SetConfigPersistence("", config.Config{CloudModel: "test-model"})
	capReg := capabilities.NewRegistry(capabilities.Services{})
	builtins.Register(capReg)
	srv.SetToolRegistry(agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases()))
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
	// 1 user + assistant tool_use + user tool_result + assistant text.
	if len(turns) != 4 {
		t.Fatalf("expected 4 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" {
		t.Errorf("turn 0 role: %q", turns[0].Role)
	}
	if turns[1].Role != "assistant" || turns[1].BlocksJSON == "" {
		t.Errorf("turn 1 should be assistant with blocks, got role=%q", turns[1].Role)
	}
	if turns[2].Role != "user" || turns[2].BlocksJSON == "" {
		t.Errorf("turn 2 should be the user tool_result turn, got role=%q blocks=%q", turns[2].Role, turns[2].BlocksJSON)
	}
	if turns[3].Role != "assistant" || turns[3].Content != "All done." {
		t.Errorf("turn 3: role=%q content=%q", turns[3].Role, turns[3].Content)
	}
}

func TestPersistToolLoopTurns_DeltaOnly(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	req := &proto.ProcessRequestRequest{Input: "second", ConversationId: "conv-delta"}

	// Simulate a result whose History carries a 2-message injected prefix plus
	// 2 new messages. Only the 2 new ones should be persisted.
	result := agent.ToolLoopResult{History: []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "old-user"}}},      // injected
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "old-asst"}}}, // injected
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "second"}}},        // new
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "reply"}}},    // new
	}}

	srv.persistToolLoopTurns(ctx, req, result, 2, "qwen3-coder")

	turns, err := store.GetTurns(ctx, "conv-delta")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 persisted (delta only), got %d", len(turns))
	}
	if turns[0].Content != "second" || turns[1].Content != "reply" {
		t.Errorf("unexpected delta turns: %q, %q", turns[0].Content, turns[1].Content)
	}
	// The served-tier model name is recorded on the conversation row, not a
	// hardcoded cloud model.
	if info, err := store.Get(ctx, "conv-delta"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if info.Model != "qwen3-coder" {
		t.Errorf("conversation model = %q, want qwen3-coder", info.Model)
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

func TestStreamToolLoop_ReplaysHistoryNoDuplication(t *testing.T) {
	srv, store := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "one"}},
			{{Type: llm.BlockText, Text: "two"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	// Turn 1.
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "first", ConversationId: "conv-r"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	// Turn 2.
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "second", ConversationId: "conv-r"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	// (a) Replay: the provider's turn-2 input must include the prior turn
	// (user "first", assistant "one") ahead of the new user "second".
	if len(prov.seen) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(prov.seen))
	}
	turn2 := prov.seen[1]
	if len(turn2) != 3 {
		t.Fatalf("turn-2 provider input = %d messages, want 3 (first, one, second)", len(turn2))
	}
	if turn2[0].Blocks[0].Text != "first" || turn2[1].Blocks[0].Text != "one" || turn2[2].Blocks[0].Text != "second" {
		t.Errorf("turn-2 history wrong: %q / %q / %q",
			turn2[0].Blocks[0].Text, turn2[1].Blocks[0].Text, turn2[2].Blocks[0].Text)
	}

	// (b) No duplication: 2 messages per turn × 2 turns = 4, linear.
	turns, err := store.GetTurns(context.Background(), "conv-r")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("expected 4 turns (linear growth, no re-save), got %d", len(turns))
	}
	want := []string{"first", "one", "second", "two"}
	for i, w := range want {
		if turns[i].Content != w {
			t.Errorf("turn %d = %q, want %q", i, turns[i].Content, w)
		}
	}
}

// TestStreamToolLoop_UpdatesContextMeter verifies that after a tool-loop turn
// GetContextUsage returns a tokenizer-estimated sent size > 0 and the correct
// model-max for the active cloud model.
func TestStreamToolLoop_UpdatesContextMeter(t *testing.T) {
	srv, _ := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hello"}}},
		caps:    llm.Capabilities{SupportsTools: true},
		usage:   [2]int{4321, 99},
	}
	srv.SetCloudLLMProvider(prov)

	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "hi", ConversationId: "conv-meter"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	resp, err := srv.GetContextUsage(context.Background(), &proto.GetContextUsageRequest{ConversationId: "conv-meter"})
	if err != nil {
		t.Fatalf("GetContextUsage: %v", err)
	}
	// TokensUsed is now the tokenizer-estimated sent size (compaction-aware),
	// not the provider-reported count. This conversation has no compaction state,
	// so the sent view IS the full history → sent must equal raw, and be > 0.
	if resp.TokensUsed <= 0 {
		t.Errorf("TokensUsed = %d, want > 0 (tokenizer estimate of stored turns)", resp.TokensUsed)
	}
	if resp.TokensUsed != resp.RawTokens {
		t.Errorf("with no compaction state sent must equal raw: sent=%d raw=%d", resp.TokensUsed, resp.RawTokens)
	}
	if want := int32(contextmeter.ModelMax("test-model")); resp.ModelMax != want {
		t.Errorf("ModelMax = %d, want %d (cloud window)", resp.ModelMax, want)
	}
}

// TestStreamToolLoop_FinalResponseCarriesTokens guards the bug where the
// streaming final-response builder dropped the turn's token counts (the CLI's
// "last turn N↑/N↓" footer reads these, so it was stuck at 0/0 even though the
// context meter — fed by RecordContextUsage from the same values — worked).
func TestStreamToolLoop_FinalResponseCarriesTokens(t *testing.T) {
	srv, _ := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hello"}}},
		caps:    llm.Capabilities{SupportsTools: true},
		usage:   [2]int{4321, 99},
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "hi", ConversationId: "conv-tok"},
		stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	var fr *proto.ProcessRequestResponse
	for _, m := range stream.sent {
		if f := m.GetFinalResponse(); f != nil {
			fr = f
		}
	}
	if fr == nil {
		t.Fatal("no FinalResponse was sent")
	}
	if fr.GetInputTokens() != 4321 || fr.GetOutputTokens() != 99 {
		t.Fatalf("FinalResponse tokens = %d↑/%d↓, want 4321↑/99↓ (the CLI 'last turn' footer reads these)",
			fr.GetInputTokens(), fr.GetOutputTokens())
	}
}
