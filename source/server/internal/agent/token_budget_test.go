package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

func TestTokenBudgetAccumulation(t *testing.T) {
	b := TokenBudget{Limit: 100}
	if b.Exhausted() {
		t.Fatal("fresh budget exhausted")
	}
	b.Add(40, 20)
	if b.Exhausted() || b.Spent != 60 {
		t.Fatalf("spent=%d", b.Spent)
	}
	// Unreported/negative usage must add nothing: enforcement is based only on
	// provider-billed counts, never estimates.
	b.Add(0, 0)
	b.Add(-5, -5)
	if b.Spent != 60 {
		t.Fatalf("phantom accumulation: %d", b.Spent)
	}
	b.Add(40, 0)
	if !b.Exhausted() {
		t.Fatal("crossed budget not exhausted")
	}
	var le *llm.Error
	if err := b.Err(); !errors.As(err, &le) || le.Class != llm.ErrTokenBudgetExhausted || le.Used != 100 || le.Limit != 100 {
		t.Fatalf("wrong classified error: %v", b.Err())
	}
	unlimited := TokenBudget{}
	unlimited.Add(1<<30, 1<<30)
	if unlimited.Exhausted() {
		t.Fatal("disabled budget enforced")
	}
}

// countingProbeTool records executions so tests can prove the crossing
// response's tool calls never ran.
type countingProbeTool struct{ executed *int }

func (c countingProbeTool) Name() string                      { return "probe" }
func (c countingProbeTool) Description() string               { return "test probe" }
func (c countingProbeTool) Permission() agenttools.Permission { return agenttools.PermR }
func (c countingProbeTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (c countingProbeTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	*c.executed++
	return &agenttools.Result{Type: agenttools.ResultText, Text: "ok"}, nil
}

func probeRegistry(t *testing.T, executed *int) *agenttools.Registry {
	t.Helper()
	reg := agenttools.NewRegistry()
	reg.MustRegister(countingProbeTool{executed: executed})
	return reg
}

// budgetProbeProvider bills a fixed token count per call and always asks for
// another tool call, simulating a grinding dispatch that would never stop.
type budgetProbeProvider struct {
	calls int
	bill  int
}

func (p *budgetProbeProvider) Name() string { return "budget-probe" }

func (p *budgetProbeProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}

func (p *budgetProbeProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("streaming only")
}

func (p *budgetProbeProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	blocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("call-%d", p.calls),
		ToolName: "probe", ToolInput: json.RawMessage(`{}`)}}
	events := blocksToEvents(blocks)
	// The terminal event carries this provider's billed usage.
	for i := range events {
		if events[i].Type == llm.EventMessageStop {
			events[i].StopReason = "tool_use"
			events[i].InputTokens = p.bill
			events[i].OutputTokens = 64
			events[i].Usage = llm.TokenUsage{Input: llm.ReportedTokens(int64(p.bill)), Output: llm.ReportedTokens(64)}
		}
	}
	return &scriptedStream{events: events}, nil
}

// The loop must stop with the classified error after the response that crosses
// the budget, without executing that response's tool calls, and the partial
// history must survive for the caller.
func TestToolLoopStopsAtTokenBudget(t *testing.T) {
	executed := 0
	provider := &budgetProbeProvider{bill: 4000}
	res, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider:    provider,
		Model:       "m",
		System:      "probe forever",
		UserInput:   "go",
		Registry:    probeRegistry(t, &executed),
		Permissions: NewStaticPermissionStore(ModeBypass),
		TokenBudget: 10000,
	})
	var le *llm.Error
	if !errors.As(err, &le) || le.Class != llm.ErrTokenBudgetExhausted {
		t.Fatalf("expected token budget error, got %v", err)
	}
	// bill+64 per call: call1=4064, call2=8128, call3=12192 → stop after 3rd.
	if provider.calls != 3 {
		t.Fatalf("calls=%d want 3", provider.calls)
	}
	// The crossing response's tool call must NOT run: 2 executions only.
	if executed != 2 {
		t.Fatalf("executed=%d want 2", executed)
	}
	if le.Used != 12192 || le.Limit != 10000 {
		t.Fatalf("counts: %+v", le)
	}
	if len(res.History) == 0 || res.Iterations != 3 {
		t.Fatalf("partial result lost: iters=%d history=%d", res.Iterations, len(res.History))
	}
	if !strings.Contains(err.Error(), "narrow the task") {
		t.Fatalf("unactionable message: %v", err)
	}
}

// Budget zero disables enforcement (main turns); the iteration cap remains the
// backstop and must terminate the same grinding provider.
func TestToolLoopUnbudgetedFallsBackToIterationCap(t *testing.T) {
	executed := 0
	provider := &budgetProbeProvider{bill: 4000}
	_, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider:      provider,
		Model:         "m",
		System:        "probe forever",
		UserInput:     "go",
		Registry:      probeRegistry(t, &executed),
		Permissions:   NewStaticPermissionStore(ModeBypass),
		MaxIterations: 4,
	})
	if err != nil {
		var le *llm.Error
		if errors.As(err, &le) && le.Class == llm.ErrTokenBudgetExhausted {
			t.Fatalf("disabled budget enforced: %v", err)
		}
	}
	if executed != 4 {
		t.Fatalf("iteration cap regressed: calls=%d executed=%d", provider.calls, executed)
	}
}

// finalAnswerAfterBudgetProvider answers terminally on its first call while
// billing past the budget in that same response.
type finalAnswerAfterBudgetProvider struct{}

func (p *finalAnswerAfterBudgetProvider) Name() string { return "final-answer" }

func (p *finalAnswerAfterBudgetProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}

func (p *finalAnswerAfterBudgetProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("streaming only")
}

func (p *finalAnswerAfterBudgetProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	events := blocksToEvents([]llm.Block{{Type: llm.BlockText, Text: "the findings"}})
	for i := range events {
		if events[i].Type == llm.EventMessageStop {
			events[i].InputTokens = 5000
			events[i].OutputTokens = 100
			events[i].Usage = llm.TokenUsage{Input: llm.ReportedTokens(5000), Output: llm.ReportedTokens(100)}
		}
	}
	return &scriptedStream{events: events}, nil
}

// A terminal answer (no tool calls) on the crossing response completes
// normally: the money is spent either way, and the result is useful.
func TestToolLoopBudgetAllowsFinalAnswer(t *testing.T) {
	res, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider:    &finalAnswerAfterBudgetProvider{},
		Model:       "m",
		System:      "answer",
		UserInput:   "go",
		Registry:    agenttools.NewRegistry(),
		Permissions: NewStaticPermissionStore(ModeBypass),
		TokenBudget: 100,
	})
	if err != nil {
		t.Fatalf("finished dispatch failed: %v", err)
	}
	if res.FinalText != "the findings" {
		t.Fatalf("answer lost: %q", res.FinalText)
	}
}
