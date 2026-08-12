package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

// sse writes SSE-formatted lines to w (each line gets "data: <line>\n\n").
func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, l := range lines {
		fmt.Fprintf(w, "data: %s\n\n", l)
	}
}

// TestStreamChat_ToolDeltaEvents verifies the streamReader emits the correct
// START→DELTA→STOP event sequence mirroring the Anthropic contract:
//
//   - EventMessageStart at stream open
//   - EventTextDelta for content fragments
//   - EventToolUseStart when a tool-call index is first seen
//   - EventToolUseInputDelta (TextDelta = JSON fragment) for each argument chunk
//   - EventToolUseStop when the tool index closes
//   - EventMessageStop with InputTokens + OutputTokens from the usage chunk
//
// The httptest server splits tool-call arguments across two chunks to exercise
// the fragment-streaming path.
func TestStreamChat_ToolDeltaEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: text content
			`{"choices":[{"delta":{"content":"hi "}}]}`,
			// chunk 2: first tool-call fragment (index 0, id, name, partial args)
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"p\":"}}]}}]}`,
			// chunk 3: second tool-call fragment (same index, rest of args)
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
			// chunk 4: finish reason + usage (stream_options include_usage)
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`,
			// SSE terminator
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	var (
		text        string
		toolName    string
		toolID      string
		toolArgs    string // reassembled from EventToolUseInputDelta fragments
		sawToolStop bool
		sawMsgStart bool
		inputToks   int
		outputToks  int
	)

	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventMessageStart:
			sawMsgStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventToolUseStart:
			toolName = ev.ToolName
			toolID = ev.ToolUseID
		case llm.EventToolUseInputDelta:
			toolArgs += ev.TextDelta
		case llm.EventToolUseStop:
			sawToolStop = true
		case llm.EventMessageStop:
			inputToks = ev.InputTokens
			outputToks = ev.OutputTokens
		}
	}

	if !sawMsgStart {
		t.Fatal("expected EventMessageStart")
	}
	if text != "hi " {
		t.Fatalf("text: got %q, want %q", text, "hi ")
	}
	if toolName != "read" {
		t.Fatalf("toolName: got %q, want %q", toolName, "read")
	}
	if toolID != "c1" {
		t.Fatalf("toolID: got %q, want %q", toolID, "c1")
	}
	if toolArgs != `{"p":"x"}` {
		t.Fatalf("toolArgs (reassembled): got %q, want %q", toolArgs, `{"p":"x"}`)
	}
	if !sawToolStop {
		t.Fatal("expected EventToolUseStop")
	}
	if inputToks != 4 || outputToks != 6 {
		t.Fatalf("tokens on EventMessageStop: got input=%d output=%d, want 4/6", inputToks, outputToks)
	}
}

// collectToolTurn drains a stream reader and returns the reassembled tool-use
// turn (name, id, args) plus whether Start/Stop framing was seen. Shared by the
// deferred-name regression tests.
func collectToolTurn(t *testing.T, rd llm.StreamReader) (name, id, args string, sawStart, sawStop bool) {
	t.Helper()
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventToolUseStart:
			sawStart = true
			name = ev.ToolName
			id = ev.ToolUseID
		case llm.EventToolUseInputDelta:
			args += ev.TextDelta
		case llm.EventToolUseStop:
			sawStop = true
		}
	}
	return
}

// TestStreamChat_ToolNameArrivesLate reproduces the GLM-4.5-Air (via
// llama-server) streaming shape that made every delegated sub-agent run report
// called=[]: the tool-call index opens with an EMPTY name (and partial args),
// and the function name arrives in a LATER fragment for the same index. The old
// reader captured ToolName only on first sight of the index, so the name was
// permanently lost (empty ToolName → invisible to calledToolNames). The reader
// must now defer EventToolUseStart until the name is known and still reassemble
// the full argument JSON in order.
func TestStreamChat_ToolNameArrivesLate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: index opens with id + FIRST arg fragment, NO name yet.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c9","type":"function","function":{"arguments":"{\"p\":"}}]}}]}`,
			// chunk 2: the name arrives now, on a later fragment for the same index.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"read"}}]}}]}`,
			// chunk 3: rest of the arguments.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "glm"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	name, id, args, sawStart, sawStop := collectToolTurn(t, rd)
	if !sawStart {
		t.Fatal("expected EventToolUseStart even though the name arrived late")
	}
	if name != "read" {
		t.Fatalf("toolName: got %q, want %q (late-arriving name was dropped)", name, "read")
	}
	if id != "c9" {
		t.Fatalf("toolID: got %q, want %q", id, "c9")
	}
	if args != `{"p":"x"}` {
		t.Fatalf("toolArgs (reassembled across the pre-name and post-name fragments): got %q, want %q", args, `{"p":"x"}`)
	}
	if !sawStop {
		t.Fatal("expected EventToolUseStop")
	}
}

// TestStreamChat_ToolNameOnFinalFragment covers the variant where the name only
// appears on the very last tool fragment before finish — the reader must flush
// the deferred Start at EOF rather than drop the call.
func TestStreamChat_ToolNameOnFinalFragment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"arguments":"{}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"list_dir"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":2}}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "glm"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	name, _, args, sawStart, sawStop := collectToolTurn(t, rd)
	if !sawStart || name != "list_dir" {
		t.Fatalf("expected Start with name list_dir, got start=%v name=%q", sawStart, name)
	}
	if args != `{}` {
		t.Fatalf("args: got %q, want %q", args, `{}`)
	}
	if !sawStop {
		t.Fatal("expected EventToolUseStop")
	}
}

// TestStreamChat_ToolArgumentsArriveBeforeNameAndID covers a compatibility
// server shape where argument fragments begin before the function name/id show
// up. The reader must buffer those early argument bytes, emit Start only once it
// knows the tool name, and replay the buffered args in order.
func TestStreamChat_ToolArgumentsArriveBeforeNameAndID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: arguments arrive first, with neither id nor name yet.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
			// chunk 2: id + name finally arrive.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c-early-args","type":"function","function":{"name":"read"}}]}}]}`,
			// chunk 3: argument tail.
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"file.txt\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "compat"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	name, id, args, sawStart, sawStop := collectToolTurn(t, rd)
	if !sawStart || !sawStop {
		t.Fatalf("expected complete tool framing, sawStart=%v sawStop=%v", sawStart, sawStop)
	}
	if name != "read" || id != "c-early-args" {
		t.Fatalf("tool start = name=%q id=%q, want read/c-early-args", name, id)
	}
	if args != `{"path":"file.txt"}` {
		t.Fatalf("args = %q, want %q", args, `{"path":"file.txt"}`)
	}
}

// TestStreamChat_SequentialToolCallIndexes documents the supported multiple-
// tool shape for this reader: fragments for each tool index must be contiguous.
// The streamReader intentionally tracks one open tool at a time; fully
// interleaved fragments for index 0/1/0 are not claimed as supported by this
// test. Contiguous multi-index streams still assemble into separate tool turns.
func TestStreamChat_SequentialToolCallIndexes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c1","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"x\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "compat"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	type turn struct{ name, id, args string }
	var turns []turn
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventToolUseStart:
			turns = append(turns, turn{name: ev.ToolName, id: ev.ToolUseID})
		case llm.EventToolUseInputDelta:
			if len(turns) == 0 {
				t.Fatalf("input delta before tool start: %q", ev.TextDelta)
			}
			turns[len(turns)-1].args += ev.TextDelta
		}
	}

	if len(turns) != 2 {
		t.Fatalf("turns = %#v, want 2", turns)
	}
	if turns[0] != (turn{name: "read", id: "c0", args: `{}`}) {
		t.Fatalf("turn0 = %#v", turns[0])
	}
	if turns[1] != (turn{name: "grep", id: "c1", args: `{"pattern":"x"}`}) {
		t.Fatalf("turn1 = %#v", turns[1])
	}
}

// TestStreamChat_SeparateUsageChunk verifies that the real OpenAI include_usage
// shape — where finish_reason and usage arrive in SEPARATE chunks — still
// produces the correct InputTokens/OutputTokens on EventMessageStop.
//
// Wire shape:
//  1. text delta chunk
//  2. chunk with choices[0].finish_reason="stop" and empty delta  (no usage yet)
//  3. chunk with choices=[] and usage object                       (len==0 path)
//  4. [DONE]
//
// The reader must skip the empty-choices chunk (continue) but still capture the
// usage before emitting EventMessageStop at EOF.
func TestStreamChat_SeparateUsageChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			// chunk 1: text content
			`{"choices":[{"delta":{"content":"hello"}}]}`,
			// chunk 2: finish_reason only — no usage yet
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			// chunk 3: usage only — choices array is empty (the len==0 continue path)
			`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
			// SSE terminator
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	var (
		text        string
		sawMsgStart bool
		stopReason  string
		inputToks   int
		outputToks  int
	)

	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventMessageStart:
			sawMsgStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventMessageStop:
			stopReason = ev.StopReason
			inputToks = ev.InputTokens
			outputToks = ev.OutputTokens
		}
	}

	if !sawMsgStart {
		t.Fatal("expected EventMessageStart")
	}
	if text != "hello" {
		t.Fatalf("text: got %q, want %q", text, "hello")
	}
	if stopReason != "stop" {
		t.Fatalf("stopReason: got %q, want %q", stopReason, "stop")
	}
	if inputToks != 7 || outputToks != 3 {
		t.Fatalf("tokens on EventMessageStop: got input=%d output=%d, want 7/3", inputToks, outputToks)
	}
}

// collectText drives a reader to EOF and returns the concatenated text deltas
// plus whether EventMessageStart was seen.
func collectText(t *testing.T, rd llm.StreamReader) string {
	t.Helper()
	var text string
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta {
			text += ev.TextDelta
		}
	}
	return text
}

func streamReaderFor(t *testing.T, lines ...string) (llm.StreamReader, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, lines...)
	}))
	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		},
	})
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return rd, func() { rd.Close(); srv.Close() }
}

// TestStreamChat_RecoversReasoningWhenNoContent covers GLM-4.5-Air streaming:
// reasoning_content deltas carry the plaintext answer while content stays empty.
// The reader must flush the buffered reasoning as a single visible text delta.
func TestStreamChat_RecoversReasoningWhenNoContent(t *testing.T) {
	rd, cleanup := streamReaderFor(t,
		`{"choices":[{"delta":{"reasoning_content":"The answer "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"is 42."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		`[DONE]`,
	)
	defer cleanup()
	if got := collectText(t, rd); got != "The answer is 42." {
		t.Fatalf("recovered text: got %q, want %q", got, "The answer is 42.")
	}
}

// TestStreamChat_PrefersContentOverReasoning: when normal content streams, the
// buffered reasoning must be discarded (no duplicate/appended text).
func TestStreamChat_PrefersContentOverReasoning(t *testing.T) {
	rd, cleanup := streamReaderFor(t,
		`{"choices":[{"delta":{"reasoning_content":"thinking..."}}]}`,
		`{"choices":[{"delta":{"content":"Real answer."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	defer cleanup()
	if got := collectText(t, rd); got != "Real answer." {
		t.Fatalf("text: got %q, want %q", got, "Real answer.")
	}
}

// TestStreamChat_InterleavedContentAndReasoning: reasoning arriving both before
// and after content must not leak into the visible text.
func TestStreamChat_InterleavedContentAndReasoning(t *testing.T) {
	rd, cleanup := streamReaderFor(t,
		`{"choices":[{"delta":{"reasoning_content":"pre "}}]}`,
		`{"choices":[{"delta":{"content":"hello "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"mid "}}]}`,
		`{"choices":[{"delta":{"content":"world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	defer cleanup()
	if got := collectText(t, rd); got != "hello world" {
		t.Fatalf("text: got %q, want %q", got, "hello world")
	}
}

// TestStreamChat_ToolCallOnlyIgnoresReasoning: a tool-call-only turn with
// reasoning must not synthesize a visible text delta (the tool call is the
// action; a real tool_use turn's reasoning is just thinking).
func TestStreamChat_ToolCallOnlyIgnoresReasoning(t *testing.T) {
	rd, cleanup := streamReaderFor(t,
		`{"choices":[{"delta":{"reasoning_content":"I should search."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	defer cleanup()
	if got := collectText(t, rd); got != "" {
		t.Fatalf("expected no visible text, got %q", got)
	}
}
