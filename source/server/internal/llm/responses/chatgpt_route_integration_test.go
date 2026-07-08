package responses

// Live diagnostics for the ChatGPT subscription route (codex backend).
// Skipped unless CHATGPT_INTEGRATION=1. Uses the "chatgpt" profile's token
// from the OS keychain (the same one `sign in with ChatGPT` stores), so
// running this may raise a keychain authorization prompt.
//
//   CHATGPT_INTEGRATION=1 go test ./internal/llm/responses/ -run ChatGPTRoute -v
//
// Unlike the normal client path, these tests call do() directly and print the
// RAW response body on non-2xx, because errorFromBody drops bodies that don't
// carry an {"error":{"message":...}} envelope — and diagnosing backend 400s
// needs the body verbatim.

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

const chatgptTestModel = "gpt-5.5"

func chatgptLiveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("CHATGPT_INTEGRATION") != "1" {
		t.Skip("set CHATGPT_INTEGRATION=1 to run live codex-backend tests")
	}
	store, err := secrets.OpenKeychain()
	if err != nil {
		t.Skipf("keychain unavailable: %v", err)
	}
	src := chatgptauth.NewSource(store, "chatgpt", chatgptauth.Flow{})
	return NewClient(Config{Model: chatgptTestModel, Route: RouteChatGPT, TokenSource: src})
}

// sendRaw posts one request and returns status + raw body (drained fully so
// streams don't leak). Bodies are capped at 4 KiB — codex error bodies are small.
func sendRaw(t *testing.T, c *Client, req request) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.do(ctx, req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(body)
}

func userText(text string) inputItem {
	return inputItem{Type: "message", Role: "user", Content: []contentPart{{Type: "input_text", Text: text}}}
}

// TestChatGPTRoute_Stages walks from the bare request shape up to the shape a
// resumed cross-provider conversation produces, reporting the first stage the
// backend rejects and the verbatim error body.
func TestChatGPTRoute_Stages(t *testing.T) {
	c := chatgptLiveClient(t)

	weatherTool := tool{Type: "function", Name: "get_weather", Description: "Get the current weather for a city.",
		Parameters: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}

	stages := []struct {
		name string
		req  request
	}{
		{"bare", request{
			Model: chatgptTestModel, Store: false, Stream: true,
			Include: []string{"reasoning.encrypted_content"},
			Input:   []inputItem{userText("Reply with the single word: pong")},
		}},
		{"system", request{
			Model: chatgptTestModel, Store: false, Stream: true,
			Include:      []string{"reasoning.encrypted_content"},
			Instructions: "You are a terse coding assistant.",
			Input:        []inputItem{userText("Reply with the single word: pong")},
		}},
		{"tools", request{
			Model: chatgptTestModel, Store: false, Stream: true,
			Include: []string{"reasoning.encrypted_content"},
			Tools:   []tool{weatherTool},
			Input:   []inputItem{userText("Reply with the single word: pong")},
		}},
		// MaxTokens set on the ChatRequest: buildRequest must NOT forward
		// max_output_tokens on this route (the backend rejects it with
		// "Unsupported parameter").
		{"max-tokens", c.buildRequest(llm.ChatRequest{
			MaxTokens: 4096,
			Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
		}, true)},
		// A resumed conversation whose earlier turns ran on Anthropic: plain
		// assistant text (must replay as output_text — the backend rejects
		// input_text on assistant messages) plus a tool_use/tool_result pair
		// whose call_id is in Anthropic's toolu_ format, not codex's fc_/call_.
		{"anthropic-history", c.buildRequest(llm.ChatRequest{
			Tools: []llm.Tool{{Name: "get_weather", Description: "Get the current weather for a city.",
				Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}},
			Messages: []llm.Message{
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Use the tool."}}},
				{Role: llm.RoleAssistant, Blocks: []llm.Block{
					{Type: llm.BlockText, Text: "Checking the weather now."},
					{Type: llm.BlockToolUse, ToolUseID: "toolu_01XYZabcdef", ToolName: "get_weather", ToolInput: []byte(`{"city":"Paris"}`)},
				}},
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "toolu_01XYZabcdef", Content: "18C and sunny"}}},
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "still running?"}}},
			},
		}, true)},
	}

	for _, st := range stages {
		st := st
		t.Run(st.name, func(t *testing.T) {
			status, body := sendRaw(t, c, st.req)
			if status < 200 || status >= 300 {
				t.Errorf("stage %q REJECTED: status=%d body=%s", st.name, status, body)
				return
			}
			t.Logf("stage %q accepted (status=%d)", st.name, status)
		})
	}
}

// TestChatGPTRoute_ReasoningRoundTrip verifies that reasoning items captured
// from a live turn replay cleanly (the backend requires a summary field on
// replayed reasoning items — "Missing required parameter: 'input[N].summary'").
func TestChatGPTRoute_ReasoningRoundTrip(t *testing.T) {
	c := chatgptLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.",
		Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	// Phrased to provoke a reasoning item before the tool call so the replay
	// leg genuinely exercises reasoning-item round-tripping.
	ask := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText,
		Text: "I'm flying from the city that hosted the 1900 World's Fair to the one with the Brandenburg Gate. Work out which city I depart from, then use the tool to get its weather."}}}

	first, err := c.Chat(ctx, llm.ChatRequest{Tools: []llm.Tool{weather}, Messages: []llm.Message{ask}})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	var toolUse *llm.Block
	var sawReasoning bool
	for i := range first.Blocks {
		switch first.Blocks[i].Type {
		case llm.BlockReasoning:
			sawReasoning = true
		case llm.BlockToolUse:
			toolUse = &first.Blocks[i]
		}
	}
	if !sawReasoning {
		t.Log("no reasoning block on turn 1 — replay still exercises the item shape if present in Blocks")
	}
	if toolUse == nil {
		t.Skip("model did not call the tool; cannot exercise the round-trip")
	}
	second, err := c.Chat(ctx, llm.ChatRequest{
		Tools: []llm.Tool{weather},
		Messages: []llm.Message{
			ask,
			{Role: llm.RoleAssistant, Blocks: first.Blocks},
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: toolUse.ToolUseID, Content: "18C and sunny"}}},
		},
	})
	if err != nil {
		t.Fatalf("second turn (history replayed): %v", err)
	}
	var text string
	for _, b := range second.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("round-trip final=%q (reasoning on turn1=%v)", text, sawReasoning)
}

// TestChatGPTRoute_BuildRequestReplica sends exactly what buildRequest produces
// for a tool-loop-shaped ChatRequest, end to end through StreamChat.
func TestChatGPTRoute_BuildRequestReplica(t *testing.T) {
	c := chatgptLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.",
		Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	rd, err := c.StreamChat(ctx, llm.ChatRequest{
		System:    "You are a terse coding assistant.",
		Tools:     []llm.Tool{weather},
		MaxTokens: 4096,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
	})
	if err != nil {
		t.Fatalf("StreamChat replica: %v", err)
	}
	defer rd.Close()
	var text string
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
	}
	t.Logf("replica streamed=%q", text)
}
