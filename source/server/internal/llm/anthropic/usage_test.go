package anthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
	sdk "github.com/anthropics/anthropic-sdk-go"
)

func TestNormalizedUsagePresence(t *testing.T) {
	for _, tc := range []struct {
		name, raw     string
		known         bool
		input, output int64
	}{
		{"missing", `{}`, false, 0, 0},
		{"zero", `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, true, 0, 0},
		{"cache inclusive", `{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":5,"output_tokens_details":{"thinking_tokens":2}}`, true, 19, 7},
		{"cache absent", `{"input_tokens":11,"output_tokens":7}`, false, 0, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw sdk.Usage
			if err := json.Unmarshal([]byte(tc.raw), &raw); err != nil {
				t.Fatal(err)
			}
			var counts usageCounts
			counts.message(raw)
			got := counts.snapshot()
			if got.TotalsKnown() != tc.known || got.Input.Value != tc.input || got.Output.Value != tc.output {
				t.Fatalf("normalized=%+v", got)
			}
			if tc.name == "cache inclusive" && got.Reasoning != llm.ReportedTokens(2) {
				t.Fatal("reasoning missing")
			}
		})
	}
}

func TestNormalizedChatAndStreamUsage(t *testing.T) {
	raw := `{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":5}`
	t.Run("chat", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":"end_turn","usage":` + raw + `}`))
		}))
		defer srv.Close()
		c := NewClient(Config{BaseURL: srv.URL, APIKey: "fake", Model: "claude"})
		out, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10})
		if err != nil {
			t.Fatal(err)
		}
		if out.Usage.Input != llm.ReportedTokens(19) || out.Usage.Output != llm.ReportedTokens(7) || out.InputTokens != 11 {
			t.Fatalf("chat usage=%+v", out)
		}
	})
	t.Run("stream cumulative", func(t *testing.T) {
		fixture := strings.Replace(sseFixture, `{"input_tokens":1,"output_tokens":0}`, raw, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(fixture))
		}))
		defer srv.Close()
		c := NewClient(Config{BaseURL: srv.URL, APIKey: "fake", Model: "claude"})
		rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10})
		if err != nil {
			t.Fatal(err)
		}
		out, err := llm.CollectStream(t.Context(), rdr, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out.Usage.Input != llm.ReportedTokens(19) || out.Usage.Output != llm.ReportedTokens(12) || out.Usage.CacheRead != llm.ReportedTokens(3) {
			t.Fatalf("stream usage=%+v", out.Usage)
		}
	})
}
