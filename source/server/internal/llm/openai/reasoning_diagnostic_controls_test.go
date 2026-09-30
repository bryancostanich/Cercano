package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReasoningDiagnosticConstructor(t *testing.T) {
	valid := ReasoningDiagnosticConfig{Mode: ReasoningDrop, BaseURL: "https://fixture.invalid/v1", Model: "fixture", MaxRequests: 2, MaxBodyBytes: 1024, MaxMemoryBytes: 4096, Timeout: time.Second}
	for _, mutate := range []func(*ReasoningDiagnosticConfig){
		func(c *ReasoningDiagnosticConfig) { c.Mode = "" }, func(c *ReasoningDiagnosticConfig) { c.BaseURL = "https://user:secret@fixture.invalid/v1" },
		func(c *ReasoningDiagnosticConfig) { c.BaseURL += "?key=secret" }, func(c *ReasoningDiagnosticConfig) { c.Model = "" },
		func(c *ReasoningDiagnosticConfig) { c.MaxRequests = 0 }, func(c *ReasoningDiagnosticConfig) { c.MaxBodyBytes = 0 },
		func(c *ReasoningDiagnosticConfig) { c.MaxMemoryBytes = 1 }, func(c *ReasoningDiagnosticConfig) { c.Timeout = 0 },
		func(c *ReasoningDiagnosticConfig) { c.Timeout = 2 * time.Hour },
	} {
		cfg := valid
		mutate(&cfg)
		if _, err := NewReasoningDiagnostic(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestReasoningDiagnosticTemperatureAndNoOptIn(t *testing.T) {
	var requests []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var obj map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
			t.Error(err)
		}
		requests = append(requests, obj)
		sse(w, `{"choices":[{"delta":{"content":"ready"},"finish_reason":"stop"}]}`, "[DONE]")
	}))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningDrop)
	body := strings.TrimSuffix(hookFirstWire, "}") + `,"temperature":-999999}`
	resp, err := hookWireRequest(t, s, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodPost, s.endpoint, strings.NewReader(body))
	resp, err = (&normalizingDoer{next: &http.Client{}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	for _, r := range requests {
		if string(r["temperature"]) != "0" {
			t.Fatal("zero temperature patch lost")
		}
	}
	var captured map[string]json.RawMessage
	json.Unmarshal(s.Captures()[0].Request, &captured)
	if string(captured["temperature"]) != "0" || len(s.Captures()) != 1 {
		t.Fatal("capture differs from wire or leaked outside context")
	}
}

func TestReasoningDiagnosticMissingReasoning(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"Read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "[DONE]")
	}))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningPreserve)
	resp, err := hookWireRequest(t, s, hookFirstWire)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, err = hookWireRequest(t, s, `{"model":"diagnostic","stream":true,"messages":[{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"Read","arguments":"{}"}}]}]}`)
	if err == nil || hits.Load() != 1 {
		t.Fatal("missing reasoning accepted")
	}
}

func TestReasoningDiagnosticDeadlineDuringRead(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	s := hookSession(t, srv.URL+"/v1", ReasoningDrop)
	ctx, cancel := context.WithCancel(WithReasoningDiagnostic(context.Background(), s))
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, strings.NewReader(hookFirstWire))
	done := make(chan error, 1)
	go func() { _, err := (&normalizingDoer{next: &http.Client{}}).Do(req); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled read accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("read ignored cancellation")
	}
	if len(s.Captures()) != 0 {
		t.Fatal("cancelled body retained")
	}
}
