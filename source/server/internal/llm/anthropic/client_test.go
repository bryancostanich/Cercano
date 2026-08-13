package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClient_NewClient_SetsBaseURLAndUA(t *testing.T) {
	var seenURL string
	var seenUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.Path
		seenUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:   srv.URL,
		APIKey:    "dummy",
		Model:     "claude-opus-4-7",
		UserAgent: "claude-cli/test",
	})
	caps := c.Capabilities()
	if !caps.SupportsTools || !caps.SupportsParallelTools {
		t.Errorf("expected tool support, got %+v", caps)
	}
	_, err := c.Chat(t.Context(), simpleReq())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seenURL, "/v1/messages") {
		t.Errorf("expected /v1/messages, got %q", seenURL)
	}
	if seenUA != "claude-cli/test" {
		t.Errorf("expected custom UA, got %q", seenUA)
	}
}

func simpleReq() ChatRequest {
	return ChatRequest{Model: "claude-opus-4-7", MaxTokens: 10}
}

func TestClient_Chat_SendsMessagesAndTools(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"got it"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	resp, err := c.Chat(t.Context(), ChatRequest{
		Model:     "claude",
		MaxTokens: 100,
		System:    "you are concise",
		Messages: []llm.Message{{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
		}},
		Tools: []llm.Tool{{
			Name:        "ping",
			Description: "ping",
			Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := string(seenBody)
	if !strings.Contains(body, `"system"`) || !strings.Contains(body, "you are concise") {
		t.Errorf("system not sent: %s", body)
	}
	if !strings.Contains(body, `"tools"`) || !strings.Contains(body, "ping") {
		t.Errorf("tools not sent: %s", body)
	}
	if !strings.Contains(body, `"hi"`) {
		t.Errorf("user message not sent: %s", body)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != llm.BlockText || resp.Blocks[0].Text != "got it" {
		t.Errorf("blocks not converted: %+v", resp.Blocks)
	}
}

func TestClient_BuildParams_FloorsUnsetMaxTokens(t *testing.T) {
	// max_tokens:0 on the wire is not an error at api.anthropic.com — the
	// completion just comes back with zero output tokens. A caller that
	// forgets MaxTokens must get a usable budget, not silent empty text.
	c := &Client{}
	params := c.buildParams(ChatRequest{
		Model: "claude-opus-5-0",
		Messages: []llm.Message{{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
		}},
	})
	if params.MaxTokens <= 0 {
		t.Fatalf("MaxTokens = %d, want a positive default", params.MaxTokens)
	}
	// An explicit budget must pass through untouched.
	params = c.buildParams(simpleReq())
	if params.MaxTokens != 10 {
		t.Fatalf("explicit MaxTokens = %d, want 10", params.MaxTokens)
	}
}

func TestClient_Chat_RetriesWithoutDeprecatedTemperature(t *testing.T) {
	// Newer Anthropic models reject the temperature parameter outright
	// ("`temperature` is deprecated for this model"). Greedy decoding is
	// unattainable there — the adapter must retry once without temperature
	// instead of failing the call, and remember the model so subsequent
	// calls skip the doomed attempt.
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(b), `"temperature"`) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"` + "`temperature`" + ` is deprecated for this model."}}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	zero := 0.0
	req := ChatRequest{
		Model: "claude-opus-5-0", MaxTokens: 10, Temperature: &zero,
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	}

	resp, err := c.Chat(t.Context(), req)
	if err != nil {
		t.Fatalf("expected retry without temperature to succeed, got: %v", err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "ok" {
		t.Fatalf("unexpected response: %+v", resp.Blocks)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests (rejected + retry), got %d", len(bodies))
	}
	if strings.Contains(bodies[1], `"temperature"`) {
		t.Fatalf("retry must omit temperature: %s", bodies[1])
	}

	// Second call to the same model: the adapter remembers and goes straight
	// to the no-temperature request.
	bodies = nil
	if _, err := c.Chat(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || strings.Contains(bodies[0], `"temperature"`) {
		t.Fatalf("expected a single temperature-free request, got %d: %v", len(bodies), bodies)
	}

	// A genuinely different 400 must NOT be retried or masked.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be positive"}}`))
	}))
	defer srv2.Close()
	c2 := NewClient(Config{BaseURL: srv2.URL, APIKey: "dummy", Model: "claude"})
	if _, err := c2.Chat(t.Context(), req); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("unrelated 400 must surface unchanged, got: %v", err)
	}
}

func TestClient_BuildParams_TemperatureZeroReachesWire(t *testing.T) {
	// Temperature is a pointer: nil = provider default (omit from the wire),
	// &0 = greedy decoding, which MUST be sent — the compaction summarizer
	// depends on it (default-temp summaries are a format coin flip).
	c := &Client{}
	msg := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}

	zero := 0.0
	params := c.buildParams(ChatRequest{Model: "m", MaxTokens: 10, Messages: msg, Temperature: &zero})
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temperature":0`) {
		t.Fatalf("greedy temperature not on the wire: %s", body)
	}

	params = c.buildParams(ChatRequest{Model: "m", MaxTokens: 10, Messages: msg})
	body, err = json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"temperature"`) {
		t.Fatalf("nil temperature must be omitted from the wire: %s", body)
	}
}
