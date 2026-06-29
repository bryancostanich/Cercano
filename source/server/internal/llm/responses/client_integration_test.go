package responses

// Live integration tests against the real OpenAI Responses API.
// Skipped unless INTEGRATION_TEST=1 and OPENAI_API_KEY are set. Model defaults to
// gpt-4o-mini; override with OPENAI_RESPONSES_MODEL (use an o-series model to
// exercise reasoning). Optional OPENAI_BASE_URL points at a compatible endpoint.
//
//   INTEGRATION_TEST=1 OPENAI_API_KEY=sk-... \
//     go test ./internal/llm/responses/ -run Integration -v

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run live Responses tests")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model := os.Getenv("OPENAI_RESPONSES_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return NewClient(Config{BaseURL: os.Getenv("OPENAI_BASE_URL"), APIKey: key, Model: model})
}

func TestIntegration_Chat(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.Chat(ctx, llm.ChatRequest{
		System:   "You are terse. Reply with exactly one word.",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("reply=%q in=%d out=%d", text, resp.InputTokens, resp.OutputTokens)
	if strings.TrimSpace(text) == "" {
		t.Error("empty reply")
	}
}

func TestIntegration_Stream(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rd, err := c.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Count 1 to 5, space separated."}}}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	defer rd.Close()
	var text string
	var sawStop bool
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta {
			text += ev.TextDelta
		}
		if ev.Type == llm.EventMessageStop {
			sawStop = true
		}
	}
	t.Logf("streamed=%q", text)
	if strings.TrimSpace(text) == "" || !sawStop {
		t.Errorf("text=%q sawStop=%v", text, sawStop)
	}
}

func TestIntegration_ToolCall(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.", Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	resp, err := c.Chat(ctx, llm.ChatRequest{
		Tools:    []llm.Tool{weather},
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Call the tool."}}}},
	})
	if err != nil {
		t.Fatalf("Chat(tools): %v", err)
	}
	var called bool
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockToolUse && b.ToolName == "get_weather" {
			called = true
		}
	}
	if !called {
		t.Error("model did not emit a get_weather tool call")
	}
}

func TestIntegration_ReasoningRoundTrip(t *testing.T) {
	model := os.Getenv("OPENAI_RESPONSES_MODEL")
	if model == "" || !strings.HasPrefix(model, "o") {
		t.Skip("set OPENAI_RESPONSES_MODEL to an o-series reasoning model to run this")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.", Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	first, err := c.Chat(ctx, llm.ChatRequest{
		Tools:    []llm.Tool{weather},
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Use the tool."}}}},
	})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	// Confirm a reasoning block came back to carry forward.
	var reasoning, toolUse *llm.Block
	for i := range first.Blocks {
		switch first.Blocks[i].Type {
		case llm.BlockReasoning:
			reasoning = &first.Blocks[i]
		case llm.BlockToolUse:
			toolUse = &first.Blocks[i]
		}
	}
	if reasoning == nil || reasoning.ReasoningData == "" {
		t.Fatal("expected an encrypted reasoning block on turn 1")
	}
	if toolUse == nil {
		t.Skip("model did not call the tool; cannot exercise the round-trip")
	}
	// Second turn: replay assistant blocks (reasoning + tool_use) + a tool result.
	second, err := c.Chat(ctx, llm.ChatRequest{
		Tools: []llm.Tool{weather},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Use the tool."}}},
			{Role: llm.RoleAssistant, Blocks: first.Blocks},
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: toolUse.ToolUseID, Content: "18C and sunny"}}},
		},
	})
	if err != nil {
		t.Fatalf("second turn (reasoning carried): %v", err)
	}
	var text string
	for _, b := range second.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("final=%q", text)
	if strings.TrimSpace(text) == "" {
		t.Error("expected a final answer after carrying reasoning")
	}
}
