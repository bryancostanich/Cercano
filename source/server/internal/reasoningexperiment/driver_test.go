package reasoningexperiment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/pkg/config"
)

func fixtureSpec() Spec {
	zero := 0.0
	return Spec{Profile: "fixture", Model: "diagnostic", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "private audit instruction"}}}}, Tools: []llm.Tool{{Name: "Read", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}, Results: []RecordedResult{{Name: "Read", Arguments: json.RawMessage(`{"path":"fixture.txt"}`), Content: "private recorded result"}}, MaxRequests: 4, MaxTokens: 128, TimeoutSeconds: 5, Temperature: &zero}
}
func fixtureConfig(url string) config.Config {
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "fixture", Flavor: "chat_completions", BaseURL: url}}
	return c
}
func send(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		fmt.Fprintf(w, "data: %s\n\n", e)
	}
}
func callSSE(w http.ResponseWriter, id, name, args string) {
	event := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "private reasoning", "tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}}
	b, _ := json.Marshal(event)
	send(w, string(b), "[DONE]")
}
func TestPairRealTransport(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Error("auth missing")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["max_tokens"] != float64(128) || body["temperature"] != float64(0) || body["model"] != "diagnostic" {
					t.Error("settings changed")
				}
				preserved := false
				for _, m := range body["messages"].([]any) {
					if m.(map[string]any)["reasoning_content"] == "private reasoning" {
						preserved = true
					}
				}
				if preserved {
					send(w, `{"choices":[{"index":0,"delta":{"content":"private answer"},"finish_reason":"stop"}]}`, "[DONE]")
				} else {
					// Same initial call IDs give identical paired inputs; repeated actions use a new ID.
					id := "initial"
					if len(body["messages"].([]any)) > 1 {
						id = fmt.Sprint("repeat", n)
					}
					callSSE(w, id, "Read", `{"path":"fixture.txt"}`)
				}
			}))
			defer srv.Close()
			s := fixtureSpec()
			s.PreserveFirst = reverse
			builds := 0
			report, err := Run(context.Background(), fixtureConfig(srv.URL+"/v1"), s, func(p config.CloudProfile) (inference.Provider, error) {
				builds++
				return openai.NewClient(openai.Config{BaseURL: p.BaseURL, Model: p.Model, APIKey: "fixture-secret"}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 4 || builds != 1 || report.MaxRequestedOutputTokens != 1024 {
				t.Fatalf("bounds/builds: %d %d %+v", requests.Load(), builds, report)
			}
			for _, a := range report.Arms {
				want := "repeated_action_result_batch"
				if a.Mode == openai.ReasoningPreserve {
					want = "terminal_stop"
				}
				if a.Status != want || len(a.Steps) != 2 {
					t.Fatalf("arm: %+v", a)
				}
			}
			if len(report.ComparableInputs) != 2 || !report.ComparableInputs[0] || !report.ComparableInputs[1] {
				t.Fatalf("inputs differ: %+v", report)
			}
			b, _ := json.Marshal(report)
			for _, secret := range []string{"private", "fixture-secret"} {
				if strings.Contains(string(b), secret) {
					t.Fatalf("raw material leaked: %s", b)
				}
			}
		})
	}
}
func TestRejectBeforeBuild(t *testing.T) {
	for _, name := range []string{"unbounded", "total", "history", "duplicate", "local", "profile", "flavor", "endpoint", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			s := fixtureSpec()
			c := fixtureConfig("http://localhost/v1")
			ctx := context.Background()
			switch name {
			case "unbounded":
				s.MaxTokens = 0
			case "total":
				s.MaxRequests = 12
				s.MaxTokens = 32768
			case "history":
				s.Messages[0].Blocks[0].Type = llm.BlockToolUse
			case "duplicate":
				s.Results = append(s.Results, s.Results[0])
			case "local":
				c.LocusMode = "open_only"
			case "profile":
				s.Profile = "missing"
			case "flavor":
				c.CloudProfiles[0].Flavor = "responses"
			case "endpoint":
				c.CloudProfiles[0].BaseURL = "https://user:secret@example.com/v1"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := Run(ctx, c, s, func(config.CloudProfile) (inference.Provider, error) {
				t.Fatal("credentials/build reached")
				return nil, nil
			})
			if err == nil {
				t.Fatal("accepted unsafe input")
			}
		})
	}
}
func TestFailClosedOutcomes(t *testing.T) {
	for _, tc := range []struct{ name, want string }{{"unknown", "unrecorded_tool_call"}, {"budget", "request_budget"}, {"length", "nonterminal_finish"}, {"incomplete", "provider_or_capture_error"}, {"http", "provider_or_capture_error"}, {"missing-reasoning", "provider_or_capture_error"}} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch tc.name {
				case "unknown":
					callSSE(w, "call", "Write", `{"path":"fixture.txt"}`)
				case "budget":
					callSSE(w, "call", "Read", `{"path":"fixture.txt"}`)
				case "length":
					send(w, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, "[DONE]")
				case "incomplete":
					send(w, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
				case "http":
					w.WriteHeader(401)
					fmt.Fprint(w, "private credential failure")
				case "missing-reasoning":
					send(w, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"Read","arguments":"{\"path\":\"fixture.txt\"}"}}]},"finish_reason":"tool_calls"}]}`, "[DONE]")
				}
			}))
			defer srv.Close()
			s := fixtureSpec()
			if tc.name == "budget" {
				s.MaxRequests = 1
			}
			report, err := Run(context.Background(), fixtureConfig(srv.URL+"/v1"), s, func(p config.CloudProfile) (inference.Provider, error) {
				return openai.NewClient(openai.Config{BaseURL: p.BaseURL, Model: p.Model}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			a := report.Arms[1]
			if a.Status != tc.want {
				t.Fatalf("got %+v want %s", a, tc.want)
			}
			if tc.name == "http" && requests.Load() != 2 {
				t.Fatal("retried failed request")
			}
		})
	}
}
func TestDecodeStrict(t *testing.T) {
	for _, input := range []string{`{"unknown":true}`, `{} {}`, strings.Repeat(" ", MaxInputBytes+1), `{`} {
		if _, e := Decode(strings.NewReader(input)); e == nil {
			t.Fatal("accepted invalid input")
		}
	}
	b, _ := json.Marshal(fixtureSpec())
	if _, e := Decode(strings.NewReader(string(b))); e != nil {
		t.Fatal(e)
	}
}

func TestRepeatedMultiStepCycleAndReplayMemory(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprint(memory), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []any `json:"messages"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				step := (len(body.Messages) - 1) / 2
				args := `{"path":"fixture.txt"}`
				if step%2 == 1 {
					args = `{"path":"second.txt"}`
				}
				callSSE(w, fmt.Sprint("call", step), "Read", args)
			}))
			defer srv.Close()
			s := fixtureSpec()
			s.Results = append(s.Results, RecordedResult{Name: "Read", Arguments: json.RawMessage(`{"path":"second.txt"}`), Content: "second result"})
			if memory {
				s.Results[0].Content = strings.Repeat("x", 2<<20)
			}
			report, err := Run(t.Context(), fixtureConfig(srv.URL+"/v1"), s, func(p config.CloudProfile) (inference.Provider, error) {
				return openai.NewClient(openai.Config{BaseURL: p.BaseURL, Model: p.Model}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range report.Arms {
				want := "repeated_action_result_batch"
				steps := 3
				if memory {
					want = "replay_memory_budget"
					steps = 1
				}
				if a.Status != want || len(a.Steps) != steps {
					t.Fatalf("%+v", a)
				}
			}
		})
	}
}
func TestPairDeadline(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	s := fixtureSpec()
	s.TimeoutSeconds = 1
	report, err := Run(t.Context(), fixtureConfig(srv.URL+"/v1"), s, func(p config.CloudProfile) (inference.Provider, error) {
		return openai.NewClient(openai.Config{BaseURL: p.BaseURL, Model: p.Model}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("second arm ignored shared deadline")
	}
	for _, a := range report.Arms {
		if a.Status != "cancelled_or_timeout" {
			t.Fatalf("%+v", a)
		}
	}
}
