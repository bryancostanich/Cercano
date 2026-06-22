package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestStreamChat_EmitsTextAndToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]any{
			"model":   "qwen3-coder",
			"message": map[string]any{"role": "assistant", "content": "Hi"},
			"done":    false,
		})
		_ = enc.Encode(map[string]any{
			"model": "qwen3-coder",
			"message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []map[string]any{{
					"function": map[string]any{"name": "read_file", "arguments": map[string]any{"path": "x"}},
				}},
			},
			"done": true,
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "qwen3-coder"})
	rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "qwen3-coder", MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()

	gotText := false
	gotTool := false
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta && ev.TextDelta == "Hi" {
			gotText = true
		}
		if ev.Type == llm.EventToolUseStart && ev.ToolName == "read_file" {
			gotTool = true
		}
	}
	if !gotText || !gotTool {
		t.Errorf("missing events: text=%v tool=%v", gotText, gotTool)
	}
}
