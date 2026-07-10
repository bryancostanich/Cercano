package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

// TestStreamChat_CollectYieldsContent guards the message-framing contract that
// #7 violated: the Ollama adapter must open its stream with EventMessageStart,
// or collect.go's stream-guard drops every delta and CollectStream returns an
// empty message — the silent "empty output, exit 0" bug.
//
// Unlike TestStreamChat_EmitsTextAndToolUse, which reads events straight off
// the StreamReader, this drives the stream through llm.CollectStream, where the
// framing guard actually lives. Without message_start the collected text is
// empty and this fails.
func TestStreamChat_CollectYieldsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]any{
			"model":   "qwen3-coder",
			"message": map[string]any{"role": "assistant", "content": "Hi"},
			"done":    false,
		})
		_ = enc.Encode(map[string]any{
			"model":             "qwen3-coder",
			"message":           map[string]any{"role": "assistant", "content": " there"},
			"done":              true,
			"prompt_eval_count": 11,
			"eval_count":        7,
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "qwen3-coder"})
	rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "qwen3-coder", MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()

	resp, err := llm.CollectStream(t.Context(), rdr, nil)
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	if text == "" {
		t.Fatal("collected stream yielded empty text — Ollama adapter never opened the message frame (message_start missing); output silently dropped (regression of #7)")
	}
	if text != "Hi there" {
		t.Errorf("collected text = %q, want %q", text, "Hi there")
	}
	// Ollama reports usage only on the final (Done) chunk; the adapter must
	// forward it on EventMessageStop or every local turn is accounted at zero (#17).
	if resp.InputTokens != 11 || resp.OutputTokens != 7 {
		t.Errorf("token counts = in:%d out:%d, want in:11 out:7 (regression of #17 — Ollama usage dropped)", resp.InputTokens, resp.OutputTokens)
	}
}
