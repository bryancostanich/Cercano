package responses

// Offline tests for the ChatGPT codex-backend quirks: parameters and content
// shapes that api.openai.com tolerates but chatgpt.com/backend-api/codex
// rejects with 400. Verified live via chatgpt_route_integration_test.go.

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBuildRequest_ChatGPTRouteOmitsMaxOutputTokens(t *testing.T) {
	req := llm.ChatRequest{
		MaxTokens: 4096,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	}
	chatgpt := NewClient(Config{Model: "gpt-5.5", Route: RouteChatGPT, TokenSource: staticTokens{}})
	if got := chatgpt.buildRequest(req, true).MaxOutputTokens; got != 0 {
		t.Errorf("chatgpt route forwarded max_output_tokens=%d; backend rejects the parameter", got)
	}
	apiKey := NewClient(Config{Model: "gpt-5.5", APIKey: "sk-test"})
	if got := apiKey.buildRequest(req, true).MaxOutputTokens; got != 4096 {
		t.Errorf("api-key route MaxOutputTokens = %d, want 4096", got)
	}
}

func TestMessagesToInput_AssistantTextIsOutputText(t *testing.T) {
	items := messagesToInput([]llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "question"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "answer"}}},
	})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if got := items[0].Content[0].Type; got != "input_text" {
		t.Errorf("user text type = %q, want input_text", got)
	}
	if got := items[1].Content[0].Type; got != "output_text" {
		t.Errorf("assistant text type = %q, want output_text", got)
	}
}

func TestMessagesToInput_ReasoningCarriesEmptySummary(t *testing.T) {
	items := messagesToInput([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningID: "rs_123", ReasoningData: "enc"},
			{Type: llm.BlockText, Text: "answer"},
		}},
	})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Type != "reasoning" {
		t.Fatalf("items[0].Type = %q, want reasoning", items[0].Type)
	}
	// The API requires summary on replayed reasoning items; absent =>
	// "Missing required parameter: 'input[N].summary'".
	if got := string(items[0].Summary); got != "[]" {
		t.Errorf("reasoning Summary = %q, want []", got)
	}
	// Non-reasoning items must not grow a summary field.
	if items[1].Summary != nil {
		t.Errorf("message item Summary = %q, want absent", items[1].Summary)
	}
}

func TestErrorFromBody_Shapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai-envelope", `{"error":{"message":"boom","type":"invalid_request_error"}}`, "responses: boom"},
		{"codex-detail", `{"detail":"Unsupported parameter: max_output_tokens"}`, "responses: Unsupported parameter: max_output_tokens"},
		{"opaque", `<html>gateway</html>`, "responses: status 400: <html>gateway</html>"},
		{"empty", ``, "responses: status 400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner, _, _ := errorFromBody(400, []byte(tc.body))
			if got := inner.Error(); got != tc.want {
				t.Errorf("errorFromBody = %q, want %q", got, tc.want)
			}
		})
	}
}

// staticTokens satisfies TokenSource for offline construction of a
// chatgpt-route client.
type staticTokens struct{}

func (staticTokens) Token(context.Context) (string, string, error) { return "tok", "acct", nil }

// TestStreamErrorMessage_Shapes pins error extraction from every error-frame
// shape the Responses SSE stream produces. The generic fallback must carry
// the raw frame — "responses stream error" alone is undiagnosable.
func TestStreamErrorMessage_Shapes(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"response.failed nested", `{"type":"response.failed","response":{"error":{"message":"rate limit exceeded"}}}`,
			"rate limit exceeded"},
		{"top-level error event", `{"type":"error","code":"server_error","message":"The server had an error"}`,
			"server_error: The server had an error"},
		{"top-level error envelope", `{"type":"error","error":{"message":"boom"}}`,
			"boom"},
		{"opaque frame", `{"type":"error","unexpected":"shape"}`,
			`responses stream error: {"type":"error","unexpected":"shape"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var env streamEnvelope
			if err := json.Unmarshal([]byte(tc.frame), &env); err != nil {
				t.Fatalf("frame does not parse: %v", err)
			}
			if got := streamErrorMessage(env, tc.frame); got != tc.want {
				t.Errorf("streamErrorMessage = %q, want %q", got, tc.want)
			}
		})
	}
}
