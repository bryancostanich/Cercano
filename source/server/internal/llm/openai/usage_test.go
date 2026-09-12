package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

func TestNormalizedSDKUsage(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      llm.TokenUsage
	}{
		{"missing", `{}`, llm.TokenUsage{}},
		{"ambiguous zero", `{"prompt_tokens":0,"completion_tokens":0}`, llm.TokenUsage{}},
		{"positive", `{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}`, llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(7), CacheRead: llm.ReportedTokens(3), Reasoning: llm.ReportedTokens(2)}},
		{"negative", `{"prompt_tokens":-1,"completion_tokens":-1}`, llm.TokenUsage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw goopenai.Usage
			if err := json.Unmarshal([]byte(tc.raw), &raw); err != nil {
				t.Fatal(err)
			}
			if got := normalizedUsage(raw); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestNormalizedSDKChatAndStream(t *testing.T) {
	raw := `{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}`
	for _, stream := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stream {
				sse(w, `{"choices":[],"usage":`+raw+`}`, `{"choices":[],"usage":`+raw+`}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"model":"fake","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + raw + `}`))
		}))
		c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "fake", Model: "fake"})
		var out llm.ChatResponse
		var err error
		if stream {
			var rd llm.StreamReader
			rd, err = c.StreamChat(t.Context(), llm.ChatRequest{})
			if err == nil {
				out, err = llm.CollectStream(t.Context(), rd, nil, nil)
			}
		} else {
			out, err = c.Chat(t.Context(), llm.ChatRequest{})
		}
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if out.Usage.Input != llm.ReportedTokens(11) || out.Usage.Output != llm.ReportedTokens(7) || out.Usage.CacheRead != llm.ReportedTokens(3) || out.InputTokens != 11 {
			t.Fatalf("stream=%v: %+v", stream, out)
		}
	}
}
