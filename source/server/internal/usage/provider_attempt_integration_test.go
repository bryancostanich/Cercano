package usage_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/llm/responses"
	"cercano/source/server/internal/usage"
)

func TestPhysicalAdapterAttempts(t *testing.T) {
	for _, tc := range []struct {
		name, success, failure string
		retry                  bool
		client                 func(string) inference.Provider
	}{
		{"anthropic", `{"id":"m","type":"message","model":"actual","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`, `{"type":"error","error":{"type":"invalid_request_error","message":"temperature is deprecated for this model"}}`, true, func(url string) inference.Provider {
			return anthropic.NewClient(anthropic.Config{BaseURL: url, APIKey: "fake", Model: "fake"})
		}},
		{"openai", `{"model":"actual","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`, "", false, func(url string) inference.Provider {
			return openai.NewClient(openai.Config{BaseURL: url, APIKey: "fake", Model: "fake"})
		}},
		{"responses", `{"model":"actual","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":7}}`, `{"error":{"message":"Unsupported parameter: temperature","type":"invalid_request_error"}}`, true, func(url string) inference.Provider {
			return responses.NewClient(responses.Config{BaseURL: url, APIKey: "fake", Model: "fake"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if tc.retry && n == 1 {
					w.WriteHeader(400)
					w.Write([]byte(tc.failure))
					return
				}
				w.Write([]byte(tc.success))
			}))
			defer srv.Close()
			var got []usage.AttemptObservation
			ctx := usage.WithAttempts(t.Context(), func(o usage.AttemptObservation) bool { got = append(got, o); return true }, usage.Attribution{Source: "compaction", OperationID: "op"})
			zero := 0.0
			_, err := tc.client(srv.URL).Chat(ctx, llm.ChatRequest{Model: "fake", MaxTokens: 10, Temperature: &zero})
			if err != nil {
				t.Fatal(err)
			}
			starts, ends := 0, 0
			ids := map[string]bool{}
			for _, o := range got {
				if o.Revision == 1 {
					starts++
					ids[o.ID] = true
				}
				if o.Outcome != usage.Started {
					ends++
					if o.Outcome == usage.Completed && (o.Tokens.Input != llm.ReportedTokens(11) || o.Tokens.Output != llm.ReportedTokens(7) || o.Model != "actual") {
						t.Fatalf("completed=%+v", o)
					}
				}
			}
			want := 1
			if tc.retry {
				want = 2
			}
			if int(calls.Load()) != want || starts != want || ends != want || len(ids) != want {
				t.Fatalf("physical calls=%d starts=%d ends=%d ids=%d want=%d", calls.Load(), starts, ends, len(ids), want)
			}
		})
	}
}
