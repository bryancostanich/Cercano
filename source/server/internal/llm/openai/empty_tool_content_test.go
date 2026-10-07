package openai

// Regression tests for outgoing wire serialization of OpenAI-compatible
// tool-result messages with genuinely empty content. go-openai v1.41.2 models
// ChatCompletionMessage.Content as `json:"content,omitempty"`, so an empty tool
// result is serialized with NO "content" field at all. Strict OpenAI-compatible
// endpoints (DeepInfra-hosted GLM, verified 2026-09) reject that with
// HTTP 422 "Field required". These tests pin the wire contract end-to-end
// through the same normalizingDoer transport the production clients use, for
// both non-streaming and streaming request paths, and the reasoning-diagnostic
// actual request path.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// emptyToolContentReq is the exact wire request shape messagesToOpenAI builds
// for a tool loop turn whose Read returned an empty file: an assistant tool
// call followed by two tool results, one genuinely empty and one nonempty.
func emptyToolContentReq() goopenai.ChatCompletionRequest {
	return goopenai.ChatCompletionRequest{
		Model: "m",
		Messages: []goopenai.ChatCompletionMessage{
			{Role: "user", Content: "hi"},
			{
				Role: "assistant",
				ToolCalls: []goopenai.ToolCall{{
					ID:       "call-1",
					Type:     "function",
					Function: goopenai.FunctionCall{Name: "read_file", Arguments: "{}"},
				}, {ID: "call-2", Type: "function", Function: goopenai.FunctionCall{Name: "read_file", Arguments: "{}"}}},
			},
			{Role: "tool", ToolCallID: "call-1", Content: ""},
			{Role: "tool", ToolCallID: "call-2", Content: "FILE"},
		},
	}
}

// toolMessages decodes the captured request body and returns its messages.
func toolMessages(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("captured body is not JSON: %v\n%s", err, body)
	}
	return wire.Messages
}

// requireToolContent asserts every role:"tool" message carries a content field
// (present on the wire, even when the value is the empty string) and returns
// the decoded (toolCallID, content) pairs.
func requireToolContent(t *testing.T, msgs []map[string]json.RawMessage) map[string]string {
	t.Helper()
	pairs := map[string]string{}
	for i, m := range msgs {
		var role string
		if err := json.Unmarshal(m["role"], &role); err != nil {
			t.Fatalf("message %d: bad role: %v", i, err)
		}
		if role != "tool" {
			continue
		}
		raw, ok := m["content"]
		if !ok {
			id, _ := stringField(t, m["tool_call_id"])
			t.Fatalf("tool message %d (tool_call_id=%q) missing required content field on the wire; body would be rejected by strict OpenAI-compatible endpoints", i, id)
		}
		var content string
		if err := json.Unmarshal(raw, &content); err != nil {
			t.Fatalf("tool message %d: content is not a string: %v", i, err)
		}
		id, _ := stringField(t, m["tool_call_id"])
		pairs[id] = content
	}
	return pairs
}

func stringField(t *testing.T, raw json.RawMessage) (string, error) {
	t.Helper()
	var s string
	if len(raw) == 0 {
		return "", fmt.Errorf("field absent")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// TestEmptyToolResultContentSent_NonStreaming pins the nonstreaming wire shape.
func TestEmptyToolResultContentSent_NonStreaming(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		gotBody = b
		io.WriteString(w, `{"id":"1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{}).CreateChatCompletion(context.Background(), emptyToolContentReq())
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	pairs := requireToolContent(t, toolMessages(t, gotBody))
	if got := pairs["call-1"]; got != "" {
		t.Fatalf("genuine empty tool result mutated on the wire: %q", got)
	}
	if got := pairs["call-2"]; got != "FILE" {
		t.Fatalf("nonempty tool result changed: %q", got)
	}
}

// TestEmptyToolResultContentSent_Streaming pins the same wire shape on the
// streaming request path (CreateChatCompletionStream shares normalizingDoer).
func TestEmptyToolResultContentSent_Streaming(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		gotBody = b
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	stream, err := clientTo(srv, Quirks{}).CreateChatCompletionStream(context.Background(), emptyToolContentReq())
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer stream.Close()
	pairs := requireToolContent(t, toolMessages(t, gotBody))
	if got := pairs["call-1"]; got != "" {
		t.Fatalf("genuine empty tool result mutated on the wire: %q", got)
	}
	if got := pairs["call-2"]; got != "FILE" {
		t.Fatalf("nonempty tool result changed: %q", got)
	}
}

// TestNonToolMessagesUnpatched guards the blast radius: absent non-tool fields
// (e.g. an assistant tool-call turn's omitted content) must stay absent, and
// the user message must keep its text.
func TestNonToolMessagesUnpatched(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		io.WriteString(w, `{"id":"1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	if _, err := clientTo(srv, Quirks{}).CreateChatCompletion(context.Background(), emptyToolContentReq()); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	msgs := toolMessages(t, gotBody)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages on the wire, got %d", len(msgs))
	}
	var user string
	if err := json.Unmarshal(msgs[0]["content"], &user); err != nil || user != "hi" {
		t.Fatalf("user content changed: raw=%s err=%v", msgs[0]["content"], err)
	}
	if _, ok := msgs[1]["content"]; ok {
		t.Fatalf("assistant tool-call turn content invented on the wire")
	}
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal(msgs[1]["tool_calls"], &calls); err != nil || len(calls) != 2 {
		t.Fatalf("assistant tool_calls lost: %v %s", err, msgs[1]["tool_calls"])
	}
	if id, _ := stringField(t, calls[0]["id"]); id != "call-1" {
		t.Fatalf("tool call id changed: %v", calls[0]["id"])
	}
}

func TestPatchToolContentPreservesPayloadAndReplay(t *testing.T) {
	const input = `{"messages":[{"role":"tool","tool_call_id":"empty"},{"role":"tool","tool_call_id":"null","content":null},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","tool_calls":[{"id":"empty","function":{"arguments":"{\"n\":9007199254740993}"}}]}],"seed":9007199254740993}`
	req, err := http.NewRequest(http.MethodPost, "http://localhost/chat/completions", bytes.NewBufferString(input))
	if err != nil {
		t.Fatal(err)
	}
	patched, err := patchToolResultContent(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(patched.Body)
	if err != nil {
		t.Fatal(err)
	}
	if patched.ContentLength != int64(len(body)) {
		t.Fatal("stale content length")
	}
	replay, err := patched.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	replayBody, err := io.ReadAll(replay)
	if err != nil || !bytes.Equal(body, replayBody) {
		t.Fatal("retry body differs")
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before["seed"], after["seed"]) {
		t.Fatal("large integer changed")
	}
	msgs := toolMessages(t, body)
	if string(msgs[0]["content"]) != `""` || string(msgs[1]["content"]) != "null" {
		t.Fatal("tool content changed")
	}
	original := toolMessages(t, []byte(input))
	for _, i := range []int{2, 3} {
		for key, value := range original[i] {
			if !bytes.Equal(value, msgs[i][key]) {
				t.Fatalf("message %d %s changed", i, key)
			}
		}
	}
	// Already-normalized requests are byte-stable on repeat/retry.
	second, err := http.NewRequest(http.MethodPost, "http://localhost/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	second, err = patchToolResultContent(second)
	if err != nil {
		t.Fatal(err)
	}
	again, err := io.ReadAll(second.Body)
	if err != nil || !bytes.Equal(body, again) {
		t.Fatal("patch not idempotent")
	}
}

// TestEmptyToolResultContentSent_ReasoningDiagnostic pins the same wire
// contract through the reasoning-diagnostic actual request path. The session's
// captured Request bytes are the exact payload sent over the wire (captured
// after the temperature and tool-content patches), so they double as payload
// evidence that an empty tool result is normalized to content:"" while the
// assistant tool-call turn keeps its absent content untouched.
func TestEmptyToolResultContentSent_ReasoningDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	session, err := NewReasoningDiagnostic(ReasoningDiagnosticConfig{
		Mode:           ReasoningDrop,
		BaseURL:        srv.URL,
		Model:          "m",
		MaxRequests:    1,
		MaxBodyBytes:   1 << 20,
		MaxMemoryBytes: 8 << 20,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	client := clientTo(srv, Quirks{})
	ctx := WithReasoningDiagnostic(context.Background(), session)
	stream, err := client.CreateChatCompletionStream(ctx, emptyToolContentReq())
	if err != nil {
		t.Fatalf("diagnostic-path stream request failed: %v", err)
	}
	defer stream.Close()

	captures := session.Captures()
	if len(captures) != 1 {
		t.Fatalf("expected 1 capture, got %d", len(captures))
	}
	pairs := requireToolContent(t, toolMessages(t, captures[0].Request))
	if got := pairs["call-1"]; got != "" {
		t.Fatalf("genuine empty tool result mutated on the diagnostic wire: %q", got)
	}
	if got := pairs["call-2"]; got != "FILE" {
		t.Fatalf("nonempty tool result changed: %q", got)
	}
	msgs := toolMessages(t, captures[0].Request)
	if _, ok := msgs[1]["content"]; ok {
		t.Fatalf("assistant tool-call turn content invented on the diagnostic wire")
	}
	if !bytes.Contains(captures[0].Request, []byte(`"content":""`)) {
		t.Fatalf("captured diagnostic request lacks content:\"\" wire evidence: %s", captures[0].Request)
	}
}
