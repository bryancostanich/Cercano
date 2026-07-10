package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Chat_BasicResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      "qwen3-coder",
			"created_at": "2026-01-01T00:00:00Z",
			"message": map[string]any{
				"role":    "assistant",
				"content": "hello",
			},
			"done":              true,
			"prompt_eval_count": 9,
			"eval_count":        4,
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "qwen3-coder"})
	if !c.Capabilities().SupportsTools {
		t.Errorf("expected tool support")
	}
	resp, err := c.Chat(t.Context(), ChatRequest{Model: "qwen3-coder", MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) == 0 || resp.Blocks[0].Text != "hello" {
		t.Errorf("response blocks: %+v", resp.Blocks)
	}
	// The non-streaming path must forward Ollama's usage counts, else local
	// turns are accounted at zero (#17).
	if resp.InputTokens != 9 || resp.OutputTokens != 4 {
		t.Errorf("token counts = in:%d out:%d, want in:9 out:4 (regression of #17)", resp.InputTokens, resp.OutputTokens)
	}
}
