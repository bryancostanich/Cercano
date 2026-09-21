package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/compaction"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/dispatch"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loopcompact"
	"cercano/source/server/internal/toolstack"
	pkgcfg "cercano/source/server/pkg/config"
)

// scriptedSummaryProvider is a deterministic inference.Provider standing in for
// the worker's open (local) provider. Chat answers with a fixed, parseable
// structured-summary body so the shared compaction summarizer can be exercised
// end-to-end without any real model.
type scriptedSummaryProvider struct {
	name  string
	fail  bool
	chats int
	reqs  []llm.ChatRequest
}

const scriptedSummaryOutput = "GOAL: finish the delegated task\n" +
	"DECISIONS:\n- keep the working set\n" +
	"STATE: compacted and continuing\n"

func (p *scriptedSummaryProvider) Name() string { return p.name }
func (p *scriptedSummaryProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{}
}

func (p *scriptedSummaryProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.reqs = append(p.reqs, req)
	p.chats++
	if p.fail {
		return llm.ChatResponse{}, errors.New("summarizer backend unavailable")
	}
	return llm.ChatResponse{
		Blocks:       []llm.Block{{Type: llm.BlockText, Text: scriptedSummaryOutput}},
		InputTokens:  111,
		OutputTokens: 22,
		Model:        req.Model,
	}, nil
}

func (p *scriptedSummaryProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("scriptedSummaryProvider: streaming not supported")
}

// compactionTestConfig snapshots a small-but-real compaction config so a pass
// crosses the activation floor deterministically inside a test budget. The
// VALUES are test-only (production defaults are untouched); the SHAPE is the
// same config surface the host reads.
func compactionTestConfig() pkgcfg.Config {
	cfg := pkgcfg.Config{}
	cfg.OpenRuntime = "mistralrs"
	cfg.MistralRS.MaxSeqLen = 32768
	cfg.Compaction.Enabled = true
	cfg.Compaction.ActivationFloorTokens = 2000
	cfg.Compaction.SegmentTokens = 400
	cfg.Compaction.VerbatimRecent = 2
	cfg.Compaction.SummarizerModel = "sum-model"
	return cfg
}

// bigDispatchHistory builds an in-memory dispatch history well past the test
// activation floor, alternating user/assistant turns.
func bigDispatchHistory(pairs, filler int) []llm.Message {
	body := strings.Repeat("lorem ipsum dolor sit amet consectetur ", filler)
	var out []llm.Message
	for i := 0; i < pairs; i++ {
		out = append(out, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: fmt.Sprintf("request %d %s", i, body)}}})
		out = append(out, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: fmt.Sprintf("answer %d %s", i, body)}}})
	}
	return out
}

// TestBuildWorkerToolSvc_WiresLoopCompactor is the regression for the worker
// dispatch-compaction parity bug: the host front door (cmd/cercano/main.go)
// installs a per-dispatch LoopCompactor factory on the tool service, but the
// worker assembled its tool service WITHOUT one, so every sub-agent dispatch
// in worker mode ran uncompacted no matter how large its in-memory history
// grew. The worker must install a factory built from the SAME shared
// config/summarizer construction the host uses (no separate worker policy).
func TestBuildWorkerToolSvc_WiresLoopCompactor(t *testing.T) {
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})
	open := &scriptedSummaryProvider{name: "mistralrs"}
	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, open, compactionTestConfig(), nil,
		func(context.Context, string) error { return nil }, nil, nil, nil)

	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	factory := ts.LoopCompactorFactory()
	if factory == nil {
		t.Fatal("worker tool service has no per-dispatch LoopCompactor factory — sub-agent dispatches in worker mode run uncompacted (host/worker parity bug)")
	}
	compactor := factory()
	if compactor == nil {
		t.Fatal("factory returned a nil compactor although a summarizer provider is available")
	}

	// The factory-built compactor must actually compact through the worker's
	// own provider wiring (the scripted open provider above), not just exist.
	cfg := compactionTestConfig()
	hist := bigDispatchHistory(40, 30)
	tok := contextmeter.Default()
	before := compaction.TotalTokens(tok, hist)
	if before < cfg.Compaction.ActivationFloorTokens {
		t.Fatalf("fixture below activation floor: %d tokens", before)
	}
	out, spent, err := compactor.CompactLoopHistory(t.Context(), hist)
	if err != nil {
		t.Fatalf("compaction pass through worker wiring failed: %v", err)
	}
	after := compaction.TotalTokens(tok, out)
	if after >= before {
		t.Fatalf("worker-wired compactor did not reduce history: before=%d after=%d", before, after)
	}
	if spent <= 0 {
		t.Fatal("summarizer spend not reported")
	}
	if open.chats == 0 {
		t.Fatal("worker summarizer provider never called — factory is not wired to the worker's open provider")
	}
}

// Compaction disabled in the snapshotted config must yield NO factory — the
// worker must not invent a policy of its own.
func TestBuildWorkerToolSvc_LoopCompactorRespectsDisabledConfig(t *testing.T) {
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})
	cfg := compactionTestConfig()
	cfg.Compaction.Enabled = false
	open := &scriptedSummaryProvider{name: "mistralrs"}
	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, open, cfg, nil,
		func(context.Context, string) error { return nil }, nil, nil, nil)
	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	if ts.LoopCompactorFactory() != nil {
		t.Fatal("factory installed although cfg.Compaction.Enabled is false")
	}
}

// A failing summarizer backend must be advisory: the worker-wired compactor
// returns the ORIGINAL history (plus the error), so a sub-agent dispatch keeps
// its uncompacted working context and continues instead of losing it.
func TestBuildWorkerToolSvc_LoopCompactorFailurePreservesHistory(t *testing.T) {
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})
	// Failing open provider and NO cloud fallback: every summarizer lane is
	// down, which is the worst case the contract must survive.
	open := &scriptedSummaryProvider{name: "mistralrs", fail: true}
	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, open, compactionTestConfig(), nil,
		func(context.Context, string) error { return nil }, nil, nil, nil)
	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	factory := ts.LoopCompactorFactory()
	if factory == nil {
		t.Fatal("worker tool service has no per-dispatch LoopCompactor factory")
	}
	compactor := factory()
	if compactor == nil {
		t.Fatal("factory returned a nil compactor")
	}
	hist := bigDispatchHistory(40, 30)
	out, _, err := compactor.CompactLoopHistory(t.Context(), hist)
	if err == nil {
		t.Fatal("expected an advisory error when every summarizer lane fails")
	}
	if len(out) != len(hist) {
		t.Fatalf("history lost on summarizer failure: got %d messages, want %d", len(out), len(hist))
	}
	for i := range out {
		if out[i].Role != hist[i].Role {
			t.Fatalf("message %d role altered: %v vs %v", i, out[i].Role, hist[i].Role)
		}
	}
	if open.chats == 0 {
		t.Fatal("summarizer provider never called — failure path not exercised")
	}
}

// Per-dispatch isolation: each factory() call must yield a FRESH compactor.
// Frozen-segment state is per-dispatch; if it leaked between factory calls,
// one sub-agent's compacted summary would contaminate a concurrent dispatch.
func TestBuildWorkerToolSvc_LoopCompactorIsolation(t *testing.T) {
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})
	open := &scriptedSummaryProvider{name: "mistralrs"}
	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, open, compactionTestConfig(), nil,
		func(context.Context, string) error { return nil }, nil, nil, nil)
	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	factory := ts.LoopCompactorFactory()
	if factory == nil {
		t.Fatal("worker tool service has no per-dispatch LoopCompactor factory")
	}
	first, second := factory(), factory()
	if first == second {
		t.Fatal("factory reused a compactor instance — frozen state can leak between concurrent dispatches")
	}
	hist := bigDispatchHistory(40, 30)
	out1, _, err := first.CompactLoopHistory(t.Context(), hist)
	if err != nil {
		t.Fatalf("first compactor failed: %v", err)
	}
	// Advance the first compactor's state further (a second pass would reuse
	// its frozen summaries); the second must behave like a brand-new dispatch
	// over the SAME input — identical first-pass result, no shared state.
	grown := append(append([]llm.Message{}, hist...),
		llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "one more turn"}}})
	if _, _, err := first.CompactLoopHistory(t.Context(), grown); err != nil {
		t.Fatalf("first compactor second pass failed: %v", err)
	}
	out2, _, err := second.CompactLoopHistory(t.Context(), hist)
	if err != nil {
		t.Fatalf("second compactor failed: %v", err)
	}
	if len(out2) != len(out1) {
		t.Fatalf("state leaked between factory-built compactors: %d vs %d messages", len(out2), len(out1))
	}
	a, _ := json.Marshal(out1)
	b, _ := json.Marshal(out2)
	if string(a) != string(b) {
		t.Fatal("state leaked between factory-built compactors: same input produced different first-pass output")
	}
}

func TestWorkerCompactionNoProviders(t *testing.T) {
	if f := loopcompact.NewFactory(workerLoopCompactDeps(compactionTestConfig(), nil, nil)); f != nil {
		t.Fatal("factory installed without providers")
	}
}

// Inspect the actual request produced by the loop with the worker-built factory.
type compactRequestProbe struct {
	scriptedSummaryProvider
	sent []llm.ChatRequest
}

func (p *compactRequestProbe) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *compactRequestProbe) StreamChat(_ context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	p.sent = append(p.sent, r)
	return &compactProbeStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: "verified"}, {Type: llm.EventMessageStop, StopReason: "end_turn"}}}, nil
}

type compactProbeStream struct{ events []llm.StreamEvent }

func (s *compactProbeStream) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) == 0 {
		return llm.StreamEvent{}, false, nil
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, true, nil
}
func (*compactProbeStream) Close() error { return nil }
func TestWorkerCompactionOutgoingRequest(t *testing.T) {
	open := &scriptedSummaryProvider{name: "ollama"}
	svc := buildWorkerToolSvc(nil, nil, projectctx.NewLoader(), nil, open, compactionTestConfig(), nil, nil, nil, nil, nil).(*toolssvc.Service)
	hist := bigDispatchHistory(40, 30)
	hist = append(hist, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "recent-read", ToolName: "Read", ToolInput: []byte(`{}`)}}}, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "recent-read", Content: "exact recent evidence"}}})
	p := &compactRequestProbe{scriptedSummaryProvider: scriptedSummaryProvider{name: "probe"}}
	_, err := agent.RunToolLoop(t.Context(), agent.ToolLoopInput{Provider: p, Model: "probe", System: "preserve task", UserInput: "finish this exact task", ConvHistory: hist, Registry: agenttools.NewRegistry(), Permissions: agent.NewStaticPermissionStore(agent.ModeBypass), LoopCompactor: svc.LoopCompactorFactory()(), MaxIterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 1 {
		t.Fatalf("requests=%d", len(p.sent))
	}
	r := p.sent[0]
	if compaction.TotalTokens(contextmeter.Default(), r.Messages) >= compaction.TotalTokens(contextmeter.Default(), hist) {
		t.Fatal("outgoing history not reduced")
	}
	data, _ := json.Marshal(r.Messages)
	for _, want := range []string{"finish this exact task", "exact recent evidence", "recent-read"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost %q", want)
		}
	}
	calls, results := 0, 0
	for _, m := range r.Messages {
		for _, b := range m.Blocks {
			if b.ToolUseID == "recent-read" || b.ToolUseRef == "recent-read" {
				if b.Type == llm.BlockToolUse {
					calls++
				}
				if b.Type == llm.BlockToolResult {
					results++
				}
			}
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("recent pair broken: calls=%d results=%d", calls, results)
	}
}
func TestWorkerCompactionCloudFallbackDefault(t *testing.T) {
	cfg := compactionTestConfig()
	cfg.ActiveCloudProfile = "cloud"
	cfg.CloudProfiles = []pkgcfg.CloudProfile{{Name: "cloud", Model: "fallback-model"}}
	cloud := &scriptedSummaryProvider{name: "cloud"}
	open := &scriptedSummaryProvider{name: "ollama", fail: true}
	summarize := loopcompact.BuildSummarizer(workerLoopCompactDeps(cfg, cloud, open))
	if _, err := summarize(t.Context(), bigDispatchHistory(2, 2)); err != nil {
		t.Fatal(err)
	}
	if len(cloud.reqs) != 1 || cloud.reqs[0].Model != "fallback-model" {
		t.Fatalf("cloud fallback requests: %+v", cloud.reqs)
	}
}
