package agent

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func textMessage(role llm.Role, text string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

func TestEstimateRequestBudget_MessagesOnly(t *testing.T) {
	budget := EstimateRequestBudget(RequestBudgetInput{
		System:        "system",
		Messages:      []llm.Message{textMessage(llm.RoleUser, strings.Repeat("a", 400))},
		ContextWindow: 1000,
	})
	if !budget.Fits {
		t.Fatal("small message-only request should fit")
	}
	if budget.SystemTokens == 0 || budget.MessageTokens == 0 {
		t.Fatalf("expected system and message costs, got %+v", budget)
	}
	if budget.ToolTokens != 0 {
		t.Fatalf("unexpected tool cost: %+v", budget)
	}
}

func TestEstimateRequestBudget_ToolsOnly(t *testing.T) {
	tools := []llm.Tool{{
		Name:        "big_tool",
		Description: strings.Repeat("tool description ", 100),
		Schema:      []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}}
	budget := EstimateRequestBudget(RequestBudgetInput{Tools: tools, ContextWindow: 1000})
	if budget.ToolTokens == 0 {
		t.Fatalf("expected tools to be counted, got %+v", budget)
	}
	if budget.MessageTokens != 0 {
		t.Fatalf("unexpected message cost: %+v", budget)
	}
}

func TestEstimateRequestBudget_OutputReserveCanOverflow(t *testing.T) {
	budget := EstimateRequestBudget(RequestBudgetInput{
		Messages:      []llm.Message{textMessage(llm.RoleUser, strings.Repeat("a", 2000))}, // ~500 tokens
		MaxTokens:     500,
		ContextWindow: 1000,
	})
	if budget.Fits {
		t.Fatalf("output reserve should push request over 900-token budget: %+v", budget)
	}
	if budget.OutputReserve != 500 {
		t.Fatalf("reserve not recorded: %+v", budget)
	}
}

func TestTrimMessagesToBudget_RepairsToolPairingAfterTrimming(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "old-use", ToolName: "Read", ToolInput: []byte(`{"path":"old"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "old-use", Content: strings.Repeat("old result", 1000)}}},
		textMessage(llm.RoleUser, "current"),
	}
	trimmed, budget := TrimMessagesToBudget(RequestBudgetInput{Messages: messages, ContextWindow: 1000}, 1)
	if !budget.Fits {
		t.Fatalf("expected request to fit after trimming, got %+v", budget)
	}
	if !llm.IsValidPairing(trimmed) {
		t.Fatalf("trimmed history must have valid tool pairing: %+v", trimmed)
	}
	if len(trimmed) != 1 || trimmed[0].Blocks[0].Text != "current" {
		t.Fatalf("expected orphaned tool pair to be dropped and current preserved, got %+v", trimmed)
	}
}

func TestTrimMessagesToBudget_CountsToolsAndKeepsNewest(t *testing.T) {
	messages := []llm.Message{
		textMessage(llm.RoleUser, strings.Repeat("old", 1000)),
		textMessage(llm.RoleAssistant, strings.Repeat("middle", 1000)),
		textMessage(llm.RoleUser, "current"),
	}
	tools := []llm.Tool{{Name: "Read", Description: strings.Repeat("read files ", 300), Schema: []byte(`{"type":"object"}`)}}
	trimmed, budget := TrimMessagesToBudget(RequestBudgetInput{
		Messages:      messages,
		Tools:         tools,
		ContextWindow: 2500,
	}, 1)
	if !budget.Fits {
		t.Fatalf("expected trimming to fit, got %+v", budget)
	}
	if len(trimmed) >= len(messages) {
		t.Fatalf("expected messages to be trimmed; len=%d", len(trimmed))
	}
	last := trimmed[len(trimmed)-1]
	if last.Blocks[0].Text != "current" {
		t.Fatalf("must preserve newest/current message, got %+v", last)
	}
	if budget.ToolTokens == 0 {
		t.Fatalf("tools should be counted: %+v", budget)
	}
}
