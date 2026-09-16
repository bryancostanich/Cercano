package openai

// This is an offline experiment harness, NOT a production reasoning fix.
// Synthetic endpoint behavior tests the intervention, not model causality.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

const diagnosticReasoning = "synthetic private continuation"

type continuationWire struct {
	Messages []map[string]json.RawMessage `json:"messages"`
}

// Memory only: never log bodies, headers, credentials, or opaque reasoning.
// One instance per arm. No shared global HTTP transport or state.
type continuationCapture struct {
	next      http.RoundTripper
	preserve  bool
	reasoning map[string]string
	requests  [][]byte
	supplied  int
}

func diagnosticBoundedRead(r io.Reader) ([]byte, error) {
	const limit = 2 << 20
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && len(b) > limit {
		return nil, fmt.Errorf("diagnostic body limit exceeded")
	}
	return b, err
}

func (c *continuationCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	b, err := diagnosticBoundedRead(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	var wire continuationWire
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, err
	}
	if c.preserve {
		for _, m := range wire.Messages {
			var calls []struct {
				ID string `json:"id"`
			}
			if raw := m["tool_calls"]; raw != nil {
				if err := json.Unmarshal(raw, &calls); err != nil {
					return nil, err
				}
				if len(calls) == 0 {
					continue
				}
				reason, ok := c.reasoning[calls[0].ID]
				if !ok {
					return nil, fmt.Errorf("missing captured continuation")
				}
				for _, call := range calls {
					if r, ok := c.reasoning[call.ID]; !ok || r != reason {
						return nil, fmt.Errorf("inconsistent captured continuation")
					}
				}
				m["reasoning_content"], _ = json.Marshal(reason)
			}
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			return nil, err
		}
		obj["messages"], err = json.Marshal(wire.Messages)
		if err != nil {
			return nil, err
		}
		b, err = json.Marshal(obj)
		if err != nil {
			return nil, err
		}
	}
	c.requests = append(c.requests, append([]byte(nil), b...))
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(b))
	cloned.ContentLength = int64(len(b))
	cloned.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	resp, err := c.next.RoundTrip(cloned)
	if err != nil {
		return nil, err
	}
	body, err := diagnosticBoundedRead(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("diagnostic endpoint status %d", resp.StatusCode)
	}
	var reason strings.Builder
	var ids []string
	done := false
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			continue
		}
		var event struct {
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Reasoning string `json:"reasoning_content"`
					Calls     []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("invalid diagnostic SSE JSON")
		}
		for _, choice := range event.Choices {
			if choice.Index != 0 {
				return nil, fmt.Errorf("multiple choices unsupported")
			}
			reason.WriteString(choice.Delta.Reasoning)
			for _, call := range choice.Delta.Calls {
				if call.ID != "" {
					ids = append(ids, call.ID)
				}
			}
		}
	}
	if !done {
		return nil, fmt.Errorf("incomplete diagnostic stream")
	}
	if reason.Len() > 0 {
		c.supplied++
		for _, id := range ids {
			c.reasoning[id] = reason.String()
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

type continuationOutcome struct {
	stop    string
	calls   int
	capture *continuationCapture
}

// Recorded fixture lookup only. Never executes a capability or reads a file.
// Bound: at most maxCalls requests * 128 requested output tokens, ten seconds,
// and 2 MiB per request/response. Budget exhaustion is NOT success.
func runContinuationDiagnostic(ctx context.Context, endpoint string, preserve bool, maxCalls int) (continuationOutcome, error) {
	out := continuationOutcome{capture: &continuationCapture{next: http.DefaultTransport, preserve: preserve, reasoning: map[string]string{}}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sdk := goopenai.DefaultConfig("synthetic-not-a-credential")
	sdk.BaseURL = endpoint
	sdk.HTTPClient = &normalizingDoer{next: &http.Client{Transport: out.capture}, quirks: quirksFor("")}
	client := NewClient(Config{Model: "diagnostic"})
	client.api = goopenai.NewClientWithConfig(sdk)
	history := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Read fixture.txt, then report its status."}}}}
	tool := llm.Tool{Name: "Read", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}
	fixtures := map[string]string{`Read:{"path":"fixture.txt"}`: "status=ready"}
	seen := map[[32]byte]bool{}
	temp := 0.0
	for out.calls < maxCalls {
		out.calls++
		rd, err := client.StreamChat(ctx, llm.ChatRequest{Messages: history, Tools: []llm.Tool{tool}, Temperature: &temp, MaxTokens: 128})
		if err != nil {
			return out, err
		}
		response, err := llm.CollectStream(ctx, rd, nil, nil)
		rd.Close()
		if err != nil {
			return out, err
		}
		results := []llm.Block{}
		var cycle strings.Builder
		for _, b := range response.Blocks {
			if b.Type != llm.BlockToolUse {
				continue
			}
			var args any
			if err := json.Unmarshal(b.ToolInput, &args); err != nil {
				return out, err
			}
			canonical, err := json.Marshal(args)
			if err != nil {
				return out, err
			}
			key := b.ToolName + ":" + string(canonical)
			result, ok := fixtures[key]
			if !ok {
				return out, fmt.Errorf("unrecorded tool call rejected")
			}
			// Ignore newly generated call IDs when comparing unchanged actions/results.
			part, _ := json.Marshal([]string{key, result})
			cycle.Write(part)
			results = append(results, llm.Block{Type: llm.BlockToolResult, ToolUseRef: b.ToolUseID, Content: result})
		}
		if len(results) == 0 {
			if response.StopReason != "stop" {
				out.stop = "nonterminal"
				return out, nil
			}
			out.stop = "completed"
			return out, nil
		}
		hash := sha256.Sum256([]byte(cycle.String()))
		if seen[hash] {
			out.stop = "repeated-unchanged-call-batch"
			return out, nil
		}
		seen[hash] = true
		history = append(history, llm.Message{Role: llm.RoleAssistant, Blocks: response.Blocks}, llm.Message{Role: llm.RoleUser, Blocks: results})
	}
	out.stop = "request-budget"
	return out, nil
}

func TestReasoningContinuationDiagnosticPair(t *testing.T) {
	// Deliberately scripted: proves the intervention reaches the wire, NOT that
	// a real GLM endpoint will behave this way.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire continuationWire
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture", 400)
			return
		}
		preserved := false
		for _, m := range wire.Messages {
			var reason string
			json.Unmarshal(m["reasoning_content"], &reason)
			preserved = preserved || reason == diagnosticReasoning
		}
		if preserved {
			sse(w, `{"choices":[{"delta":{"content":"ready"},"finish_reason":"stop"}]}`, "[DONE]")
			return
		}
		sse(w, `{"choices":[{"delta":{"reasoning_content":"synthetic private "}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"continuation"}}]}`,
			fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call%d","type":"function","function":{"name":"Read","arguments":"{\"path\":\"fixture.txt\"}"}}]}}]}`, len(wire.Messages)),
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, "[DONE]")
	}))
	defer srv.Close()
	drop, err := runContinuationDiagnostic(context.Background(), srv.URL+"/v1", false, 4)
	if err != nil {
		t.Fatal(err)
	}
	keep, err := runContinuationDiagnostic(context.Background(), srv.URL+"/v1", true, 4)
	if err != nil {
		t.Fatal(err)
	}
	if drop.stop != "repeated-unchanged-call-batch" || keep.stop != "completed" || drop.calls != 2 || keep.calls != 2 {
		t.Fatalf("unexpected outcomes: drop=%s/%d keep=%s/%d", drop.stop, drop.calls, keep.stop, keep.calls)
	}
	if drop.capture.supplied != 2 || keep.capture.supplied != 1 {
		t.Fatal("reasoning arrival not captured")
	}
	for i := 0; i < 2; i++ {
		var a, b map[string]any
		json.Unmarshal(drop.capture.requests[i], &a)
		json.Unmarshal(keep.capture.requests[i], &b)
		additions := 0
		for _, m := range b["messages"].([]any) {
			msg := m.(map[string]any)
			if reason, ok := msg["reasoning_content"]; ok {
				if reason != diagnosticReasoning {
					t.Fatal("opaque reasoning changed")
				}
				delete(msg, "reasoning_content")
				additions++
			}
		}
		if additions != i {
			t.Fatal("intervention applied to wrong message")
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatal("paired requests differ beyond reasoning state")
		}
	}
	var second continuationWire
	json.Unmarshal(drop.capture.requests[1], &second)
	if len(second.Messages) != 3 || string(second.Messages[2]["tool_call_id"]) != `"call1"` || string(second.Messages[2]["content"]) != `"status=ready"` {
		t.Fatal("tool result chain changed")
	}
	for _, m := range second.Messages {
		if _, ok := m["reasoning_content"]; ok {
			t.Fatal("baseline adapter changed: reasoning now preserved")
		}
	}
	budget, err := runContinuationDiagnostic(context.Background(), srv.URL+"/v1", false, 1)
	if err != nil || budget.stop != "request-budget" || budget.calls != 1 {
		t.Fatal("request bound failed")
	}
}

func TestReasoningContinuationDiagnosticFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, nameOnWire, reasoning, ending string
		preserve                            bool
		want                                string
	}{
		{"unknown-tool", "Write", "", "[DONE]", false, "unrecorded tool call"},
		{"missing-reasoning", "Read", "", "[DONE]", true, "missing captured continuation"},
		{"incomplete-stream", "Read", "", "", false, "incomplete diagnostic stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sse(w, fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","type":"function","function":{"name":%q,"arguments":"{\"path\":\"fixture.txt\"}"}}]}}]}`, tc.nameOnWire), `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
				if tc.ending != "" {
					sse(w, tc.ending)
				}
			}))
			defer srv.Close()
			_, err := runContinuationDiagnostic(context.Background(), srv.URL+"/v1", tc.preserve, 2)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
		})
	}
	_, err := diagnosticBoundedRead(strings.NewReader(strings.Repeat("x", (2<<20)+1)))
	if err == nil {
		t.Fatal("oversized capture accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runContinuationDiagnostic(ctx, "http://127.0.0.1:1/v1", false, 1)
	if err == nil {
		t.Fatal("cancelled experiment accepted")
	}
}
