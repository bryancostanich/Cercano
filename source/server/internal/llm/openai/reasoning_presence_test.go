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

// Presence evidence must be adapter-measured on every normal call: nonempty
// reasoning counts chunks/bytes, and confirmed absence is known zero.
func TestStreamUsageCarriesReasoningPresence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		deltas        []string
		chunks, bytes int64
	}{
		{"absent", []string{`{"content":"visible"}`}, 0, 0},
		{"empty-and-null-are-absent", []string{`{"reasoning_content":null}`, `{"reasoning_content":""}`, `{"content":"visible"}`}, 0, 0},
		{"multibyte-chunks", []string{`{"reasoning_content":"aβ"}`, `{"content":"visible"}`, `{"reasoning_content":"c"}`}, 2, 4},
		{"promoted-to-text-still-counted", []string{`{"reasoning_content":"answer"}`}, 1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, d := range tc.deltas {
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", d)
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: "m", APIKey: "k"})
			stream, err := c.StreamChat(context.Background(), llm.ChatRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var last llm.TokenUsage
			for {
				ev, ok, err := stream.Next()
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				if ev.Type == llm.EventMessageStop {
					last = ev.Usage
				}
			}
			want := llm.TokenUsage{ReasoningChunks: llm.ReportedTokens(tc.chunks), ReasoningBytes: llm.ReportedTokens(tc.bytes)}
			if last.ReasoningChunks != want.ReasoningChunks || last.ReasoningBytes != want.ReasoningBytes {
				t.Fatalf("got chunks=%+v bytes=%+v", last.ReasoningChunks, last.ReasoningBytes)
			}
			if !last.Input.Known || last.Input.Value != 7 {
				t.Fatalf("regressed usage: %+v", last)
			}
		})
	}
}

func TestChatResponseCarriesReasoningPresence(t *testing.T) {
	for _, reasoning := range []string{"", "thinking β"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			msg := map[string]any{"role": "assistant", "content": "visible"}
			if reasoning != "" {
				msg["reasoning_content"] = reasoning
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2}})
		}))
		c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: "m", APIKey: "k"})
		resp, err := c.Chat(context.Background(), llm.ChatRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		wantChunks, wantBytes := int64(0), int64(0)
		if reasoning != "" {
			wantChunks, wantBytes = 1, int64(len(reasoning))
		}
		if resp.Usage.ReasoningChunks != llm.ReportedTokens(wantChunks) || resp.Usage.ReasoningBytes != llm.ReportedTokens(wantBytes) {
			t.Fatalf("reasoning %q: got %+v", reasoning, resp.Usage)
		}
	}
}
