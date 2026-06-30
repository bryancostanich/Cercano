package bedrock

// Live integration tests against real Amazon Bedrock. Skipped unless
// INTEGRATION_TEST=1, AWS credentials are resolvable, and BEDROCK_REGION is set.
// Model defaults to anthropic.claude-3-5-sonnet-20240620-v1:0; override with
// BEDROCK_MODEL.
//
//   INTEGRATION_TEST=1 BEDROCK_REGION=us-east-1 \
//     go test ./internal/llm/bedrock/ -run Integration -v

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
		t.Skip("set INTEGRATION_TEST=1 to run live Bedrock tests")
	}
	region := os.Getenv("BEDROCK_REGION")
	if region == "" {
		t.Skip("BEDROCK_REGION not set")
	}
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		model = "anthropic.claude-3-5-sonnet-20240620-v1:0"
	}
	c, err := NewClient(Config{Region: region, Model: model})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestIntegration_Chat(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.Chat(ctx, llm.ChatRequest{
		System:    "You are terse. Reply with exactly one word.",
		MaxTokens: 64,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
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
		MaxTokens: 64,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Count 1 to 5, space separated."}}}},
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
		MaxTokens: 256,
		Tools:     []llm.Tool{weather},
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Call the tool."}}}},
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
