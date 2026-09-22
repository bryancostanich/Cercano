package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

type pinnedPromptProvider struct {
	budgetProbeProvider
	requests []llm.ChatRequest
}

func (p *pinnedPromptProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	// Snapshot before later compaction can mutate the working-history slices.
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var saved llm.ChatRequest
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	p.requests = append(p.requests, saved)
	if len(req.Tools) == 0 {
		return &scriptedStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: "finished"}, {Type: llm.EventMessageStop, StopReason: "end_turn"}}}, nil
	}
	return p.budgetProbeProvider.StreamChat(ctx, req)
}

func assertPinnedTask(t *testing.T, messages []llm.Message, task string) {
	t.Helper()
	if len(messages) == 0 || messages[0].Role != llm.RoleUser || len(messages[0].Blocks) != 1 || messages[0].Blocks[0].Type != llm.BlockText || messages[0].Blocks[0].Text != task {
		t.Fatal("original task is not the exact leading user message")
	}
	count := 0
	for _, m := range messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && b.Text == task {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("task appears %d times, want exactly once", count)
	}
	if !llm.IsValidPairing(messages) {
		t.Fatal("tool pairing corrupted")
	}
}

func TestPinnedPromptCompactionOutcomesAndFinalPass(t *testing.T) {
	const task = "  Implement ONLY phase one.\nNo global downloads; preserve calibration — μ.\n"
	for _, mode := range []string{"rewrite", "in-place", "failure", "empty", "no-op"} {
		t.Run(mode, func(t *testing.T) {
			p := &pinnedPromptProvider{}
			executed, passes := 0, 0
			c := LoopCompactorFunc(func(_ context.Context, h []llm.Message) ([]llm.Message, int, error) {
				passes++
				for _, m := range h {
					for _, b := range m.Blocks {
						if b.Text == task {
							t.Fatal("compactor received task")
						}
					}
				}
				switch mode {
				case "rewrite":
					out := []llm.Message{textMessage(llm.RoleUser, "Summary omits every constraint.")}
					if len(h) >= 2 {
						out = append(out, h[len(h)-2:]...)
					}
					return out, 3, nil
				case "in-place":
					h[0].Blocks[0].Text = "mutated execution history"
					return h, 3, nil
				case "failure":
					return nil, 3, errors.New("test summarizer failure")
				case "empty":
					return nil, 3, nil
				default:
					return h, 0, nil
				}
			})
			result, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Model: "probe", UserInput: task, PinUserInput: true, Registry: probeRegistry(t, &executed), Permissions: NewStaticPermissionStore(ModeBypass), MaxIterations: 3, MaxTokensPerTurn: 64, LoopCompactor: c})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 4 || passes != 2 || executed != 3 {
				t.Fatalf("requests=%d passes=%d executed=%d", len(p.requests), passes, executed)
			}
			for _, r := range p.requests {
				assertPinnedTask(t, r.Messages, task)
			}
			if len(p.requests[3].Tools) != 0 {
				t.Fatal("missing no-tools final pass")
			}
			assertPinnedTask(t, result.History, task)
		})
	}
}

func TestPinnedPromptTrimsExecutionHistory(t *testing.T) {
	const task = "Keep this entire task unchanged.\nNo global downloads.\n"
	for _, withSummary := range []bool{false, true} {
		t.Run(fmt.Sprintf("summary=%t", withSummary), func(t *testing.T) {
			p := &pinnedPromptProvider{}
			executed := 0
			old := textMessage(llm.RoleUser, strings.Repeat("old execution evidence ", 3000))
			var c LoopCompactor
			if withSummary {
				c = LoopCompactorFunc(func(_ context.Context, h []llm.Message) ([]llm.Message, int, error) {
					return []llm.Message{textMessage(llm.RoleUser, strings.Repeat("oversized generated summary ", 3000)), h[len(h)-1]}, 0, nil
				})
			}
			_, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Model: "probe", UserInput: task, PinUserInput: true, ConvHistory: []llm.Message{old, textMessage(llm.RoleUser, "recent evidence")}, Registry: probeRegistry(t, &executed), Permissions: NewStaticPermissionStore(ModeBypass), MaxIterations: 1, MaxTokensPerTurn: 64, ContextWindow: 1024, LoopCompactor: c})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 2 {
				t.Fatalf("requests=%d", len(p.requests))
			}
			for _, r := range p.requests {
				assertPinnedTask(t, r.Messages, task)
				for _, m := range r.Messages {
					for _, b := range m.Blocks {
						if b.Text == old.Blocks[0].Text || strings.Contains(b.Text, "oversized generated summary") {
							t.Fatal("old history/summary was not trimmed")
						}
					}
				}
				if !EstimateRequestBudget(RequestBudgetInput{System: r.System, Messages: r.Messages, Tools: r.Tools, MaxTokens: r.MaxTokens, ContextWindow: 1024}).Fits {
					t.Fatal("oversize request sent")
				}
			}
		})
	}
}

func TestPinnedPromptTooLargeFailsBeforeProvider(t *testing.T) {
	p := &pinnedPromptProvider{}
	executed := 0
	task := strings.Repeat("original task must never be removed ", 3000)
	result, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Model: "probe", UserInput: task, PinUserInput: true, Registry: probeRegistry(t, &executed), Permissions: NewStaticPermissionStore(ModeBypass), MaxIterations: 2, MaxTokensPerTurn: 64, ContextWindow: 1024})
	var le *llm.Error
	if !errors.As(err, &le) || le.Class != llm.ErrContextOverflow {
		t.Fatalf("want context overflow, got %v", err)
	}
	if len(p.requests) != 0 || executed != 0 {
		t.Fatal("oversize protected task reached provider or tools")
	}
	assertPinnedTask(t, result.History, task)
	if result.LastRequestBudget.MessageTokens < estimateTokens(task) {
		t.Fatal("pinned task excluded from accounting")
	}
}

func TestTrimMessagesProtectsTaskAndRecentParallelExchange(t *testing.T) {
	task := "Original task\nNo global downloads.\n"
	messages := []llm.Message{
		textMessage(llm.RoleUser, task),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "old", ToolName: "Read"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "old", Content: strings.Repeat("old evidence ", 3000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "a", ToolName: "Read"}, {Type: llm.BlockToolUse, ToolUseID: "b", ToolName: "Read"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "a", Content: "recent a"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "b", Content: "recent b"}}},
	}
	tail := preservedLoopTail(messages, 0, 1)
	if tail != 3 {
		t.Fatalf("parallel exchange tail=%d", tail)
	}
	out, budget := TrimMessagesToBudget(RequestBudgetInput{Messages: messages, ProtectedPrefix: 1, ContextWindow: 1024}, tail)
	if !budget.Fits || budget.TrimmedMessages != 2 || len(out) != 4 {
		t.Fatalf("budget=%+v messages=%d", budget, len(out))
	}
	assertPinnedTask(t, out, task)
	if out[1].Blocks[0].ToolUseID != "a" || out[3].Blocks[0].ToolUseRef != "b" {
		t.Fatal("latest exchange removed")
	}
	// When the protected task itself exceeds the window, never make it fit by
	// deleting that task, even if all execution history is removable.
	messages[0] = textMessage(llm.RoleUser, strings.Repeat(task, 1000))
	out, budget = TrimMessagesToBudget(RequestBudgetInput{Messages: messages, ProtectedPrefix: 1, ContextWindow: 128}, 0)
	if budget.Fits {
		t.Fatal("protected oversized task silently discarded")
	}
	assertPinnedTask(t, out, messages[0].Blocks[0].Text)
}

func TestPinnedPromptFinalPassCannotDropTaskToFit(t *testing.T) {
	const task = "Complete only the original task. No global downloads."
	p := &pinnedPromptProvider{}
	executed := 0
	registry := probeRegistry(t, &executed)
	tools := buildCompactToolCatalog(registry, Profile{}, false, map[string]bool{}, false)
	// Choose the smallest window that fits the initial request. The final
	// instruction adds more tokens than removing this one small tool schema
	// saves, so the no-tools pass must fail rather than drop the task.
	initial := RequestBudgetInput{Messages: []llm.Message{textMessage(llm.RoleUser, task)}, Tools: tools, MaxTokens: 64}
	used := EstimateRequestBudget(initial).EstimatedUsed
	window := int(math.Ceil(float64(used) / preflightSafetyFraction))
	finalNotice := fmt.Sprintf("You've reached the %d-step tool limit for this turn. Stop calling tools and give your best answer now using what you've gathered.", 1)
	smallestFinal := RequestBudgetInput{Messages: []llm.Message{textMessage(llm.RoleUser, task), textMessage(llm.RoleUser, finalNotice)}, MaxTokens: 64, ContextWindow: window}
	if EstimateRequestBudget(smallestFinal).Fits {
		t.Fatal("fixture must overflow even after all working history is dropped")
	}
	result, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Model: "probe", UserInput: task, PinUserInput: true, Registry: registry, Permissions: NewStaticPermissionStore(ModeBypass), MaxIterations: 1, MaxTokensPerTurn: 64, ContextWindow: window})
	var le *llm.Error
	if !errors.As(err, &le) || le.Class != llm.ErrContextOverflow {
		t.Fatalf("want final-pass context overflow, got %v", err)
	}
	if len(p.requests) != 1 || executed != 1 {
		t.Fatalf("requests=%d executed=%d", len(p.requests), executed)
	}
	assertPinnedTask(t, result.History, task)
}
