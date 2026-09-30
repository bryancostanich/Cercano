package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
)

func hookSession(t *testing.T, base string, mode ReasoningDiagnosticMode) *ReasoningDiagnostic {
	t.Helper()
	s, err := NewReasoningDiagnostic(ReasoningDiagnosticConfig{Mode: mode, BaseURL: base, Model: "diagnostic", MaxRequests: 4, MaxBodyBytes: 1 << 20, MaxMemoryBytes: 8 << 20, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func hookInitial() []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "inspect the recorded fixture"}}}}
}
func hookCall(t *testing.T, ctx context.Context, client *Client, history []llm.Message) *llm.ChatResponse {
	t.Helper()
	events, err := client.StreamChat(ctx, llm.ChatRequest{Messages: history, MaxTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	r, err := llm.CollectStream(ctx, events, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &r
}
func hookFollowup(h []llm.Message, r *llm.ChatResponse) []llm.Message {
	return append(h, llm.Message{Role: llm.RoleAssistant, Blocks: r.Blocks}, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "call1", Content: "status=ready"}}})
}
func hookToolSSE(w http.ResponseWriter) {
	sse(w, `{"choices":[{"index":0,"delta":{"reasoning_content":"synthetic private "}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"continuation","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"fixture.txt\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, "[DONE]")
}

func TestReasoningDiagnosticRealClientPair(t *testing.T) {
	var authenticated atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-fixture-key" {
			t.Error("authentication lost")
		}
		authenticated.Add(1)
		var wire continuationWire
		if json.NewDecoder(r.Body).Decode(&wire) != nil {
			t.Error("invalid request")
			return
		}
		if len(wire.Messages) == 1 {
			hookToolSSE(w)
			return
		}
		sse(w, `{"choices":[{"index":0,"delta":{"content":"ready"},"finish_reason":"stop"}]}`, "[DONE]")
	}))
	defer srv.Close()
	client := NewClient(Config{BaseURL: srv.URL + "/v1", Model: "diagnostic", APIKey: "secret-fixture-key"})
	var pairs [][]ReasoningDiagnosticCapture
	for _, mode := range []ReasoningDiagnosticMode{ReasoningDrop, ReasoningPreserve} {
		session := hookSession(t, srv.URL+"/v1", mode)
		ctx := WithReasoningDiagnostic(context.Background(), session)
		history := hookInitial()
		first := hookCall(t, ctx, client, history)
		hookCall(t, ctx, client, hookFollowup(history, first))
		captures := session.Captures()
		if len(captures) != 2 || !captures[0].ReasoningArrived || captures[1].ReasoningReplayed != (mode == ReasoningPreserve) {
			t.Fatal("incorrect capture metadata")
		}
		for _, c := range captures {
			if bytes.Contains(c.Request, []byte("secret-fixture-key")) || bytes.Contains(c.Response, []byte("secret-fixture-key")) {
				t.Fatal("credentials captured")
			}
		}
		pairs = append(pairs, captures)
		captures[0].Request[0] = '!'
		if session.Captures()[0].Request[0] == '!' {
			t.Fatal("capture aliases session")
		}
		captures[0].Request[0] = '{'
	}
	if authenticated.Load() != 4 {
		t.Fatal("missing authenticated requests")
	}
	for i := 0; i < 2; i++ {
		var a, b map[string]any
		json.Unmarshal(pairs[0][i].Request, &a)
		json.Unmarshal(pairs[1][i].Request, &b)
		additions := 0
		for _, m := range b["messages"].([]any) {
			msg := m.(map[string]any)
			if reason, ok := msg["reasoning_content"]; ok {
				if reason != diagnosticReasoning {
					t.Fatal("reasoning changed")
				}
				delete(msg, "reasoning_content")
				additions++
			}
		}
		if additions != i || !reflect.DeepEqual(a, b) {
			t.Fatal("intervention changed more than reasoning")
		}
	}
	// No opt-in means normal streaming: the baseline cannot see prior sessions.
	first := hookCall(t, context.Background(), client, hookInitial())
	isolated := hookSession(t, srv.URL+"/v1", ReasoningPreserve)
	_, err := client.StreamChat(WithReasoningDiagnostic(context.Background(), isolated), llm.ChatRequest{Messages: hookFollowup(hookInitial(), first), MaxTokens: 128})
	if err == nil {
		t.Fatal("session reused another session's reasoning")
	}
}

func hookWireRequest(t *testing.T, s *ReasoningDiagnostic, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(WithReasoningDiagnostic(context.Background(), s), http.MethodPost, s.endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return (&normalizingDoer{next: &http.Client{}}).Do(req)
}

const hookFirstWire = `{"model":"diagnostic","stream":true,"messages":[{"role":"user","content":"fixture"}]}`

func TestReasoningDiagnosticBoundsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, wire, response string
		status               int
		maxBody, maxMemory   int64
		want                 string
	}{
		{name: "nonstream", wire: `{"model":"diagnostic","stream":false}`, want: "nonstream"},
		{name: "model", wire: `{"model":"other","stream":true}`, want: "model changed"},
		{name: "choices", wire: `{"model":"diagnostic","stream":true,"n":2}`, want: "multiple choices"},
		{name: "oversized-request", maxBody: 10, want: "body limit"},
		{name: "oversized-response", maxBody: 1024, response: strings.Repeat("x", 1025), want: "body limit"},
		{name: "memory", maxBody: 1024, maxMemory: 1024, response: strings.Repeat("x", 1000), want: "body limit"},
		{name: "incomplete", response: `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n", want: "incomplete"},
		{name: "http-error", status: 500, response: "private error body", want: "HTTP status 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				io.WriteString(w, tc.response)
			}))
			defer srv.Close()
			s := hookSession(t, srv.URL+"/v1", ReasoningPreserve)
			if tc.maxBody > 0 {
				s.cfg.MaxBodyBytes = tc.maxBody
			}
			if tc.maxMemory > 0 {
				s.cfg.MaxMemoryBytes = tc.maxMemory
			}
			wire := tc.wire
			if wire == "" {
				wire = hookFirstWire
			}
			_, err := hookWireRequest(t, s, wire)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private error body") {
				t.Fatalf("expected safe %s error, got %v", tc.want, err)
			}
			if len(s.Captures()) != 0 {
				t.Fatal("failed request captured")
			}
			if _, err = hookWireRequest(t, s, hookFirstWire); err == nil {
				t.Fatal("failed session resumed")
			}
		})
	}
}

func TestReasoningDiagnosticControls(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sse(w, `{"choices":[{"index":0,"delta":{"content":"ready"},"finish_reason":"stop"}]}`, "[DONE]")
	}))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningDrop)
	s.cfg.MaxRequests = 1
	resp, err := hookWireRequest(t, s, hookFirstWire)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err = hookWireRequest(t, s, hookFirstWire); err == nil || hits.Load() != 1 {
		t.Fatal("request budget ignored")
	}
	s.Close()
	if len(s.Captures()) != 0 {
		t.Fatal("close retained captures")
	}
	for _, kind := range []string{"cancel", "deadline", "endpoint", "closed"} {
		t.Run(kind, func(t *testing.T) {
			s := hookSession(t, srv.URL+"/v1", ReasoningDrop)
			ctx := WithReasoningDiagnostic(context.Background(), s)
			endpoint := s.endpoint
			switch kind {
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				s.deadline = time.Now().Add(-time.Second)
			case "endpoint":
				endpoint += "/other"
			case "closed":
				s.Close()
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(hookFirstWire))
			if _, err := (&normalizingDoer{next: &http.Client{}}).Do(req); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if hits.Load() != 1 {
		t.Fatal("rejected request reached server")
	}
}

func TestReasoningDiagnosticRejectsChangedToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hookToolSSE(w) }))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningPreserve)
	resp, err := hookWireRequest(t, s, hookFirstWire)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, err = hookWireRequest(t, s, `{"model":"diagnostic","stream":true,"messages":[{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"Write","arguments":"{}"}}]}]}`)
	if err == nil || !strings.Contains(err.Error(), "inconsistent continuation") {
		t.Fatalf("changed call did not fail before inference: %v", err)
	}
}

func TestReasoningDiagnosticSSEValidation(t *testing.T) {
	valid := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"x","tool_calls":[{"index":0,"id":"ca","function":{"name":"Read","arguments":"{}"}}]}}]}` + "\n\n" + `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"ll"}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	reason, ids, _, err := parseDiagnosticSSE([]byte(valid))
	if err != nil || reason != "x" || len(ids) != 1 || ids[0].ID != "call" {
		t.Fatal("fragmented IDs failed")
	}
	for _, body := range []string{strings.Replace(valid, `"index":0`, `"index":1`, 1), strings.ReplaceAll(valid, "[DONE]", ""), valid + "data: {}\n", `data: {"choices":[{},{}]}` + "\n\ndata: [DONE]\n", strings.Replace(valid, `"index":0,"id":"ca","function":{"name":"Read","arguments":"{}"}`, `"id":"ca"`, 1)} {
		if _, _, _, err := parseDiagnosticSSE([]byte(body)); err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
}

func TestReasoningDiagnosticConcurrentAndRedirect(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		sse(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]")
	}))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningDrop)
	done := make(chan error, 1)
	go func() {
		resp, err := hookWireRequest(t, s, hookFirstWire)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	<-started
	if _, err := hookWireRequest(t, s, hookFirstWire); err == nil {
		t.Error("overlapping request accepted")
	}
	s.Close()
	close(release)
	if err := <-done; err == nil {
		t.Error("closed in-flight request captured")
	}
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	s = hookSession(t, redirect.URL+"/v1", ReasoningDrop)
	if _, err := hookWireRequest(t, s, hookFirstWire); err == nil || targetHits.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
