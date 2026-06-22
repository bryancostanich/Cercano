package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm/anthropic"
)

func TestIntegration_FullTurnWithToolCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			// First call: model says "I'll list the dir" and emits a tool_use.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "m_1", "type": "message", "role": "assistant",
				"model": "claude",
				"content": []map[string]any{
					{"type": "text", "text": "Listing."},
					{"type": "tool_use", "id": "u1", "name": "list_dir",
						"input": map[string]any{"path": "."}},
				},
				"stop_reason": "tool_use",
				"usage":       map[string]int{"input_tokens": 10, "output_tokens": 5},
			})
		} else {
			// Second call: model summarizes and stops.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "m_2", "type": "message", "role": "assistant",
				"model": "claude",
				"content": []map[string]any{
					{"type": "text", "text": "Done."},
				},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 20, "output_tokens": 3},
			})
		}
	}))
	defer srv.Close()

	prov := anthropic.NewClient(anthropic.Config{
		BaseURL: srv.URL, APIKey: "dummy", Model: "claude",
	})
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Model: "claude", UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 round-trips, got %d", calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}
