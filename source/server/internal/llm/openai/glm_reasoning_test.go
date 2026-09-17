package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestGLMReasoningModelFamily(t *testing.T) {
	for model, want := range map[string]bool{
		"zai-org/GLM-5.3":         true,
		"zai-org/GLM-5.3-Flash":   true,
		"glm-4.5-air":             true,
		"THUDM/glm-4-9b-chat":     true,
		"zai-org/other":           false,
		"gpt-5.5":                 false,
		"deepseek-ai/DeepSeek-V4": false,
		"":                        false,
	} {
		if got := glmReasoningModel(model); got != want {
			t.Errorf("glmReasoningModel(%q)=%v want %v", model, got, want)
		}
	}
}

// The effort pin and reasoning round-trip apply to GLM cloud calls only:
// non-GLM cloud models and the local llama-server backend stay byte-identical.
func TestGLMEffortPinnedAndReasoningRoundTrip(t *testing.T) {
	type wire struct {
		ReasoningEffort string           `json:"reasoning_effort"`
		Messages        []map[string]any `json:"messages"`
	}
	for _, tc := range []struct {
		name, backend, model string
		disableThinking      bool
		wantEffort           string
		wantRoundTrip        bool
	}{
		{"glm-cloud", "", "zai-org/GLM-5.3", false, glmReasoningEffort, true},
		{"glm-cloud-flash", "", "zai-org/GLM-5.3-Flash", false, glmReasoningEffort, true},
		{"glm-disable-thinking-wins", "", "zai-org/GLM-5.3", true, "", false},
		{"non-glm-cloud-untouched", "", "gpt-5.5", false, "", false},
		{"glm-local-untouched", "llama_server", "glm-4.5-air", false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got wire
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewDecoder(r.Body).Decode(&got)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"private thinking\",\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: tc.model, APIKey: "k", Backend: tc.backend})
			req := llm.ChatRequest{Model: tc.model, DisableThinking: tc.disableThinking,
				Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}}}}
			stream, err := c.StreamChat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := llm.CollectStream(context.Background(), stream, nil, nil)
			stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got.ReasoningEffort != tc.wantEffort {
				t.Fatalf("reasoning_effort=%q want %q", got.ReasoningEffort, tc.wantEffort)
			}
			var reasoning *llm.Block
			for i, b := range resp.Blocks {
				if b.Type == llm.BlockReasoning {
					reasoning = &resp.Blocks[i]
				}
			}
			if tc.wantRoundTrip {
				if reasoning == nil || reasoning.ReasoningData != "private thinking" || reasoning.ReasoningID != "" {
					t.Fatalf("round-trip block missing/wrong: %+v", resp.Blocks)
				}
				// The captured turn must send reasoning_content back on the
				// continuation, on the assistant tool-call message only.
				cont := append([]llm.Message{}, req.Messages...)
				cont = append(cont,
					llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks},
					llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "call-1", Content: "result"}}})
				stream2, err := c.StreamChat(context.Background(), llm.ChatRequest{Model: tc.model, Messages: cont})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = llm.CollectStream(context.Background(), stream2, nil, nil); err != nil {
					t.Fatal(err)
				}
				stream2.Close()
				replayed := 0
				for _, m := range got.Messages {
					rc, _ := m["reasoning_content"].(string)
					if rc == "" {
						continue
					}
					replayed++
					if m["role"] != "assistant" || rc != "private thinking" {
						t.Fatalf("bad replay message: %+v", m)
					}
					if _, hasCalls := m["tool_calls"]; !hasCalls {
						t.Fatalf("reasoning replayed off the tool-call turn: %+v", m)
					}
				}
				if replayed != 1 {
					t.Fatalf("reasoning_content appeared %d times, want 1", replayed)
				}
			} else {
				if reasoning != nil {
					t.Fatalf("unexpected round-trip block: %+v", reasoning)
				}
				for _, m := range got.Messages {
					if rc, _ := m["reasoning_content"].(string); rc != "" {
						t.Fatalf("reasoning leaked to wire for %s", tc.name)
					}
				}
			}
			// Presence accounting must agree regardless of gating.
			if !resp.Usage.ReasoningChunks.Known || resp.Usage.ReasoningChunks.Value != 1 {
				t.Fatalf("presence evidence lost: %+v", resp.Usage)
			}
			_ = calls
		})
	}
}

// A GLM answer with no tool calls keeps the existing promote-to-text recovery:
// reasoning must not become a round-trip block on terminal turns.
func TestGLMTerminalAnswerStaysPromoted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"the answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: "zai-org/GLM-5.3", APIKey: "k"})
	stream, err := c.StreamChat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "q"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	resp, err := llm.CollectStream(context.Background(), stream, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != llm.BlockText || resp.Blocks[0].Text != "the answer" {
		t.Fatalf("promotion regressed: %+v", resp.Blocks)
	}
}

// Non-streaming Chat must mirror the streaming capture and replay gates.
func TestGLMNonStreamingReasoningCapture(t *testing.T) {
	for _, model := range []string{"zai-org/GLM-5.3", "gpt-5.5"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0,
				"message": map[string]any{"role": "assistant", "reasoning_content": "rt",
					"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "Read", "arguments": "{}"}}}},
				"finish_reason": "tool_calls"}}})
		}))
		c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: model, APIKey: "k"})
		resp, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "q"}}}}})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		hasReasoning := false
		for _, b := range resp.Blocks {
			if b.Type == llm.BlockReasoning {
				hasReasoning = true
			}
		}
		if want := glmReasoningModel(model); hasReasoning != want {
			t.Fatalf("%s: round-trip block=%v want %v (%+v)", model, hasReasoning, want, resp.Blocks)
		}
	}
}
