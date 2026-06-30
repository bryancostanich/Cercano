# OpenAI Responses Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a hand-rolled `llm.Provider` speaking the OpenAI Responses API (`POST /v1/responses`) — stateless, `store=false`, with parity (text/tools/vision/streaming) plus reasoning persistence carried in our own history.

**Architecture:** A new `internal/llm/responses/` package mirrors `internal/llm/openai/` (client + adapter + stream + wire types) behind the existing `llm.Provider` seam. Reasoning round-trips via a new `BlockReasoning` block and a new `EventReasoning` stream event. Transient retry is extracted into a neutral `internal/llm/httpx` package that both providers depend on.

**Tech Stack:** Go (module `cercano/source/server`); standard library only for the Responses client (`net/http`, `encoding/json`, `bufio`); no new third-party dependency.

Design spec: [cloud-responses.md](./cloud-responses.md).

## Global Constraints

- **No new third-party dependency.** The Responses client is hand-rolled with the standard library. `go-openai` is NOT used by the `responses` package.
- **Stateless, `store=false`.** Every request sets `"store": false` and `"include": ["reasoning.encrypted_content"]`. Never use `previous_response_id`.
- **Provider name:** `Name()` returns `"openai-responses"`.
- **Capabilities:** `SupportsTools:true, SupportsParallelTools:true, SupportsVision:true, SupportsCaching:false`.
- **Reasoning is opaque.** We store/forward `encrypted_content` verbatim in `BlockReasoning.ReasoningData`; never parse it.
- **OpenAI-native:** no `backend`/quirks layer for this flavor. `base_url` empty → `https://api.openai.com/v1`; endpoint path `/responses`.
- **Retry shared, not duplicated:** retry lives in `internal/llm/httpx`; `openai`'s `normalizingDoer` composes it; the `responses` client uses it directly. Retry policy: `MaxAttempts:3, BaseDelay:500ms, OnStatus:[429,500,502,503]`.
- **Streaming order:** the stream reader emits events so `collectStream` assembles blocks in output order (reasoning before tool_use), matching the non-streaming adapter.
- **Build/test from `source/server/`.** Build: `go build ./...`. Test a package: `go test ./internal/llm/responses/ -count=1`.
- **Out of scope (do NOT build):** `previous_response_id`/server state; hosted tools (web/file search, code interpreter); compat fanout; Bedrock; human-readable reasoning summaries.

---

## File Structure

**New:**
- `internal/llm/httpx/retry.go` + `retry_test.go` — neutral retry transport.
- `internal/llm/responses/wire.go` — Responses request/response/stream JSON structs.
- `internal/llm/responses/adapter.go` + `adapter_test.go` — `llm` ↔ Responses translation.
- `internal/llm/responses/client.go` + `client_test.go` — Config, NewClient, Chat, StreamChat, Name, Capabilities.
- `internal/llm/responses/stream.go` + `stream_test.go` — SSE → `llm.StreamEvent`.
- `internal/llm/responses/client_integration_test.go` — gated live tests.

**Modified:**
- `internal/llm/messages.go` — add `BlockReasoning` + reasoning fields on `Block`.
- `internal/llm/stream.go` — add `EventReasoning` + reasoning fields on `StreamEvent`.
- `internal/llm/openai/quirks.go` — `Retry` field becomes `httpx.RetryPolicy`.
- `internal/llm/openai/transport.go` — `normalizingDoer` keeps only error-normalization; retry moves to httpx.
- `internal/llm/openai/transport_test.go` — keep error-norm tests; retry tests move to httpx.
- `internal/llm/openai/client.go` — `NewClient` composes `httpx.RetryTransport`.
- `internal/agent/toolloop.go` — `collectStream` handles `EventReasoning`.
- `internal/cloudfactory/factory.go` — fill the `responses` flavor case.
- `docs/agent/cloud-responses.md` — flip status to Implemented (final task).

---

### Task 1: Extract retry into `internal/llm/httpx`; refactor openai to compose it

A pure refactor: move the retry loop out of `openai`'s `normalizingDoer` into a neutral `httpx.RetryTransport`, leaving `normalizingDoer` responsible only for array-error normalization. No behavior change. The existing error-norm tests stay in `openai`; the retry tests move to `httpx`.

**Files:**
- Create: `source/server/internal/llm/httpx/retry.go`, `source/server/internal/llm/httpx/retry_test.go`
- Modify: `source/server/internal/llm/openai/quirks.go`, `transport.go`, `client.go`, `transport_test.go`

**Interfaces:**
- Produces: `httpx.Doer` interface (`Do(*http.Request) (*http.Response, error)`); `httpx.RetryPolicy{MaxAttempts int; BaseDelay time.Duration; OnStatus []int}`; `httpx.RetryTransport{Next Doer; Policy RetryPolicy}` with a `Do` method.
- Changes: `openai.Quirks.Retry` becomes type `httpx.RetryPolicy`; `openai`'s `normalizingDoer.Do` no longer retries.

- [ ] **Step 1: Write the httpx retry tests**

Create `source/server/internal/llm/httpx/retry_test.go`:

```go
package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func postReq(t *testing.T, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRetryThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := tr.Do(postReq(t, srv.URL, `{"x":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 || atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("status=%d hits=%d", resp.StatusCode, hits)
	}
}

func TestRetryExhausted(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := tr.Do(postReq(t, srv.URL, `{"x":1}`))
	if err != nil {
		t.Fatalf("expected the 503 response, not an error: %v", err)
	}
	if resp.StatusCode != 503 || atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("status=%d hits=%d", resp.StatusCode, hits)
	}
}

func TestRetryBodyResent(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := tr.Do(postReq(t, srv.URL, `{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("body not resent identically: %#v", bodies)
	}
}

func TestRetryContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, OnStatus: []int{503}}}

	done := make(chan struct{})
	go func() {
		req := postReq(t, srv.URL, `{"x":1}`).WithContext(ctx)
		tr.Do(req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("context cancel did not abort backoff")
	}
}

func TestNoRetryOnSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	tr := &RetryTransport{Next: &http.Client{}, Policy: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := tr.Do(postReq(t, srv.URL, `{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 hit, got %d", hits)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/httpx/ -count=1`
Expected: FAIL — build error, `undefined: RetryTransport`.

- [ ] **Step 3: Implement httpx**

Create `source/server/internal/llm/httpx/retry.go`:

```go
// Package httpx holds small, provider-neutral HTTP helpers shared by the cloud
// LLM clients.
package httpx

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Doer is the minimal HTTP "do" interface, satisfied by *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// RetryPolicy controls transient-failure retries.
type RetryPolicy struct {
	MaxAttempts int           // total attempts incl. the first; <2 disables retry
	BaseDelay   time.Duration // first backoff; doubles each subsequent attempt
	OnStatus    []int         // HTTP statuses that trigger a retry
}

// RetryTransport wraps a Doer, retrying transient HTTP statuses with exponential
// backoff and replaying the request body each attempt. Transport-level errors
// (context, DNS, connection reset) are NOT retried — only the configured
// statuses. The retried response body is drained and closed before the next try.
type RetryTransport struct {
	Next   Doer
	Policy RetryPolicy
}

func (t *RetryTransport) Do(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = b
		}
		resp, err := t.Next.Do(req)
		if t.shouldRetry(resp, err, attempt) {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if !sleepBackoff(req.Context(), t.Policy.BaseDelay, attempt) {
				return nil, req.Context().Err()
			}
			continue
		}
		return resp, err
	}
}

func (t *RetryTransport) shouldRetry(resp *http.Response, err error, attempt int) bool {
	if attempt >= t.Policy.MaxAttempts || err != nil || resp == nil {
		return false
	}
	for _, s := range t.Policy.OnStatus {
		if resp.StatusCode == s {
			return true
		}
	}
	return false
}

// sleepBackoff waits BaseDelay*2^(attempt-1), aborting on ctx cancellation.
// Returns false if the context ended during the wait.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) bool {
	tm := time.NewTimer(base << (attempt - 1))
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}
```

- [ ] **Step 4: Run httpx tests to verify pass**

Run: `cd source/server && go test ./internal/llm/httpx/ -count=1`
Expected: PASS (5 tests).

- [ ] **Step 5: Refactor openai to compose httpx**

In `source/server/internal/llm/openai/quirks.go`, replace the local `RetryPolicy` type and `defaultRetry`/`Quirks` so `Retry` is an `httpx.RetryPolicy`. Add the import and delete the local `RetryPolicy` struct:

```go
package openai

import (
	"time"

	"cercano/source/server/internal/llm/httpx"
)

// Quirks captures a backend's known deviations from OpenAI Chat Completions.
type Quirks struct {
	ImagesAsBase64  bool
	NormalizeErrors bool
	Retry           httpx.RetryPolicy
}

func defaultRetry() httpx.RetryPolicy {
	return httpx.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		OnStatus:    []int{429, 500, 502, 503},
	}
}
```

Keep `quirksFor` exactly as-is (it already returns `Quirks` with `Retry: defaultRetry()`). Delete the old `type RetryPolicy struct { ... }` block from this file (it now lives in httpx).

In `source/server/internal/llm/openai/transport.go`, strip retry out of `normalizingDoer` — it keeps only error normalization. Replace the file's `normalizingDoer`, `Do`, `shouldRetry`, and `sleepBackoff` with just:

```go
package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"
)

// normalizingDoer wraps an HTTPDoer to rewrite array-shaped error bodies into the
// object shape go-openai's ErrorResponse expects, before go-openai parses them.
// Retry is handled by the httpx.RetryTransport this wraps (see client.go). 2xx
// (streaming) responses pass through untouched — their bodies are never buffered.
type normalizingDoer struct {
	next   goopenai.HTTPDoer
	quirks Quirks
}

func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, err
	}
	return d.normalize(resp), nil
}
```

Keep the existing `normalize` and `arrayErrorToObject` functions in this file unchanged. (Delete the now-removed `shouldRetry` and `sleepBackoff`; they live in httpx.)

In `source/server/internal/llm/openai/client.go`, update `NewClient` to compose the retry transport. Replace its body so `HTTPClient` is the retry transport wrapped by the normalizer, and add the httpx import:

```go
import (
	"context"
	"encoding/base64"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/httpx"
)
```

```go
func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	q := quirksFor(cfg.Backend)
	retry := &httpx.RetryTransport{Next: &http.Client{}, Policy: q.Retry}
	c.HTTPClient = &normalizingDoer{next: retry, quirks: q}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model, quirks: q}
}
```

- [ ] **Step 6: Move retry tests out of openai's transport_test.go**

In `source/server/internal/llm/openai/transport_test.go`, DELETE the four retry tests (`TestRetryThenSuccess`, `TestRetryExhausted`, `TestRetryBodyResent`, `TestRetryContextCancel`) — they now live in `httpx`. KEEP `TestNormalizeArrayError` and `TestObjectErrorUnchanged` and the `clientTo`/`chatReq` helpers. The `clientTo` helper still constructs `&normalizingDoer{next: &http.Client{}, quirks: q}` directly (error-norm needs no retry), which remains valid. Remove the now-unused `sync/atomic` and `time` imports if the remaining two tests don't use them (they don't).

- [ ] **Step 7: Run the affected packages**

Run: `cd source/server && go build ./... && go test ./internal/llm/httpx/ ./internal/llm/openai/ -count=1`
Expected: PASS — httpx (5 tests), openai (all remaining tests incl. the two error-norm tests, quirks, client, adapter, stream).

- [ ] **Step 8: Commit**

```bash
git add source/server/internal/llm/httpx/ source/server/internal/llm/openai/quirks.go source/server/internal/llm/openai/transport.go source/server/internal/llm/openai/client.go source/server/internal/llm/openai/transport_test.go
git commit -m "refactor(llm): extract retry into shared httpx; openai composes it"
```

---

### Task 2: Add `BlockReasoning` and `EventReasoning` to the llm core

Two tiny vocabulary additions to the shared `llm` package so reasoning round-trips and rides the stream.

**Files:**
- Modify: `source/server/internal/llm/messages.go` (add block type + fields)
- Modify: `source/server/internal/llm/stream.go` (add event type + fields)
- Test: `source/server/internal/llm/messages_test.go` (create or append)

**Interfaces:**
- Produces: `llm.BlockReasoning BlockType = "reasoning"`; `Block.ReasoningID string`, `Block.ReasoningData string`. `llm.EventReasoning StreamEventType = "reasoning"`; `StreamEvent.ReasoningID string`, `StreamEvent.ReasoningData string`.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/messages_test.go` (or append if it exists):

```go
package llm

import (
	"encoding/json"
	"testing"
)

func TestBlockReasoningRoundTrip(t *testing.T) {
	b := Block{Type: BlockReasoning, ReasoningID: "rs_1", ReasoningData: "ENCRYPTED"}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var got Block
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != BlockReasoning || got.ReasoningID != "rs_1" || got.ReasoningData != "ENCRYPTED" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/ -run TestBlockReasoningRoundTrip -v -count=1`
Expected: FAIL — `undefined: BlockReasoning` / `unknown field ReasoningID`.

- [ ] **Step 3: Implement the additions**

In `source/server/internal/llm/messages.go`, add the const and fields:

```go
const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockImage      BlockType = "image"
	BlockReasoning  BlockType = "reasoning"
)
```

And in the `Block` struct, after the image fields, add:

```go
	// reasoning: opaque encrypted reasoning carried across turns (Responses API).
	// We never read ReasoningData — it is stored and sent back verbatim.
	ReasoningID   string `json:"reasoning_id,omitempty"`
	ReasoningData string `json:"reasoning_data,omitempty"`
```

In `source/server/internal/llm/stream.go`, add the event const:

```go
const (
	EventMessageStart      StreamEventType = "message_start"
	EventTextDelta         StreamEventType = "text_delta"
	EventToolUseStart      StreamEventType = "tool_use_start"
	EventToolUseInputDelta StreamEventType = "tool_use_input_delta"
	EventToolUseStop       StreamEventType = "tool_use_stop"
	EventReasoning         StreamEventType = "reasoning"
	EventMessageStop       StreamEventType = "message_stop"
	EventError             StreamEventType = "error"
)
```

And in the `StreamEvent` struct, after `ToolInputRaw`, add:

```go
	// Set on EventReasoning: the opaque encrypted reasoning item to carry forward.
	ReasoningID   string
	ReasoningData string
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/ -count=1`
Expected: PASS.

- [ ] **Step 5: Verify other adapters drop reasoning blocks harmlessly**

Run: `cd source/server && grep -nA2 "case llm.Block" internal/llm/anthropic/adapter.go internal/llm/openai/adapter.go | grep -i default || echo "no default-case error in either adapter switch"`
Expected: prints "no default-case error…" — confirming a `BlockReasoning` routed to anthropic/openai is silently skipped (their block switches have no `default` that errors). If a `default` that errors exists, add a no-op `case llm.BlockReasoning:` to that switch and note it in the commit.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/messages.go source/server/internal/llm/stream.go source/server/internal/llm/messages_test.go
git commit -m "feat(llm): add BlockReasoning + EventReasoning for Responses reasoning carry"
```

---

### Task 3: Responses wire types + adapter translation

Hand-rolled request/response JSON structs and the pure translation between `llm` types and the Responses wire shape (both directions).

**Files:**
- Create: `source/server/internal/llm/responses/wire.go`
- Create: `source/server/internal/llm/responses/adapter.go`
- Test: `source/server/internal/llm/responses/adapter_test.go`

**Interfaces:**
- Consumes: `llm.Message`, `llm.Block` (incl. `BlockReasoning`), `llm.Tool{Name,Description,Schema}`.
- Produces: `request`/`inputItem`/`contentPart`/`tool`/`response`/`outputItem`/`outputContent`/`usage`/`apiError` structs; `messagesToInput([]llm.Message) []inputItem`; `toolsToResponses([]llm.Tool) []tool`; `blocksFromOutput([]outputItem) []llm.Block`.

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/llm/responses/adapter_test.go`:

```go
package responses

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestMessagesToInput_TextImageToolReasoning(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "hi"},
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAAA"},
		}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningID: "rs_1", ReasoningData: "ENC"},
			{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "get_weather", ToolInput: json.RawMessage(`{"city":"Paris"}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "call_1", Content: "sunny"},
		}},
	}
	items := messagesToInput(msgs)

	// Expect, in order: message(user text+image), reasoning, function_call, function_call_output.
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d: %+v", len(items), items)
	}
	if items[0].Type != "message" || items[0].Role != "user" || len(items[0].Content) != 2 {
		t.Fatalf("item0 = %+v", items[0])
	}
	if items[0].Content[0].Type != "input_text" || items[0].Content[1].Type != "input_image" {
		t.Fatalf("content parts = %+v", items[0].Content)
	}
	if items[0].Content[1].ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("image url = %q", items[0].Content[1].ImageURL)
	}
	if items[1].Type != "reasoning" || items[1].ID != "rs_1" || items[1].EncryptedContent != "ENC" {
		t.Fatalf("item1 = %+v", items[1])
	}
	if items[2].Type != "function_call" || items[2].CallID != "call_1" || items[2].Name != "get_weather" || items[2].Arguments != `{"city":"Paris"}` {
		t.Fatalf("item2 = %+v", items[2])
	}
	if items[3].Type != "function_call_output" || items[3].CallID != "call_1" || items[3].Output != "sunny" {
		t.Fatalf("item3 = %+v", items[3])
	}
}

func TestMessagesToInput_ImageURLPassthrough(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, ImageURL: "https://x/y.png"},
	}}}
	items := messagesToInput(msgs)
	if items[0].Content[0].ImageURL != "https://x/y.png" {
		t.Fatalf("url = %q", items[0].Content[0].ImageURL)
	}
}

func TestToolsToResponses(t *testing.T) {
	tools := []llm.Tool{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}}
	got := toolsToResponses(tools)
	if len(got) != 1 || got[0].Type != "function" || got[0].Name != "get_weather" || string(got[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", got)
	}
	if toolsToResponses(nil) != nil {
		t.Fatal("nil tools should map to nil")
	}
}

func TestBlocksFromOutput(t *testing.T) {
	out := []outputItem{
		{Type: "reasoning", ID: "rs_1", EncryptedContent: "ENC"},
		{Type: "message", Role: "assistant", Content: []outputContent{{Type: "output_text", Text: "hello"}}},
		{Type: "function_call", CallID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`},
	}
	blocks := blocksFromOutput(out)
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != llm.BlockReasoning || blocks[0].ReasoningID != "rs_1" || blocks[0].ReasoningData != "ENC" {
		t.Fatalf("block0 = %+v", blocks[0])
	}
	if blocks[1].Type != llm.BlockText || blocks[1].Text != "hello" {
		t.Fatalf("block1 = %+v", blocks[1])
	}
	if blocks[2].Type != llm.BlockToolUse || blocks[2].ToolUseID != "call_1" || blocks[2].ToolName != "get_weather" || string(blocks[2].ToolInput) != `{"city":"Paris"}` {
		t.Fatalf("block2 = %+v", blocks[2])
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/responses/ -count=1`
Expected: FAIL — build error, `undefined: messagesToInput` etc.

- [ ] **Step 3: Implement wire types**

Create `source/server/internal/llm/responses/wire.go`:

```go
package responses

import "encoding/json"

// ---- request ----

type request struct {
	Model           string      `json:"model"`
	Instructions    string      `json:"instructions,omitempty"`
	Input           []inputItem `json:"input"`
	Tools           []tool      `json:"tools,omitempty"`
	MaxOutputTokens int         `json:"max_output_tokens,omitempty"`
	Temperature     *float64    `json:"temperature,omitempty"`
	Store           bool        `json:"store"`
	Include         []string    `json:"include,omitempty"`
	Stream          bool        `json:"stream,omitempty"`
}

type inputItem struct {
	Type string `json:"type"` // message | function_call | function_call_output | reasoning

	// message
	Role    string        `json:"role,omitempty"`
	Content []contentPart `json:"content,omitempty"`

	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output
	Output string `json:"output,omitempty"`

	// reasoning
	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"` // input_text | input_image
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type tool struct {
	Type        string          `json:"type"` // function
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ---- response (non-stream) ----

type response struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Output []outputItem `json:"output"`
	Usage  *usage       `json:"usage,omitempty"`
	Error  *apiError    `json:"error,omitempty"`
}

type outputItem struct {
	Type    string          `json:"type"` // message | function_call | reasoning
	Role    string          `json:"role,omitempty"`
	Content []outputContent `json:"content,omitempty"`

	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

type outputContent struct {
	Type string `json:"type"` // output_text
	Text string `json:"text,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}
```

Create `source/server/internal/llm/responses/adapter.go`:

```go
package responses

import (
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
)

// messagesToInput maps llm messages to Responses input items, preserving order.
// Text and image blocks accumulate into a single "message" item per message;
// tool calls, tool results, and reasoning become their own items (flushing any
// pending message first so order is preserved).
func messagesToInput(msgs []llm.Message) []inputItem {
	var items []inputItem
	for _, m := range msgs {
		role := roleString(m.Role)
		var parts []contentPart
		flush := func() {
			if len(parts) > 0 {
				items = append(items, inputItem{Type: "message", Role: role, Content: parts})
				parts = nil
			}
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				parts = append(parts, contentPart{Type: "input_text", Text: b.Text})
			case llm.BlockImage:
				url := b.ImageURL
				if url == "" {
					url = fmt.Sprintf("data:%s;base64,%s", b.MediaType, b.ImageData)
				}
				parts = append(parts, contentPart{Type: "input_image", ImageURL: url})
			case llm.BlockToolUse:
				flush()
				items = append(items, inputItem{Type: "function_call", CallID: b.ToolUseID, Name: b.ToolName, Arguments: string(b.ToolInput)})
			case llm.BlockToolResult:
				flush()
				items = append(items, inputItem{Type: "function_call_output", CallID: b.ToolUseRef, Output: b.Content})
			case llm.BlockReasoning:
				flush()
				items = append(items, inputItem{Type: "reasoning", ID: b.ReasoningID, EncryptedContent: b.ReasoningData})
			}
		}
		flush()
	}
	return items
}

func roleString(r llm.Role) string {
	if r == llm.RoleAssistant {
		return "assistant"
	}
	return "user"
}

func toolsToResponses(tools []llm.Tool) []tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, tool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	return out
}

// blocksFromOutput maps Responses output items to llm blocks, preserving order.
func blocksFromOutput(out []outputItem) []llm.Block {
	var blocks []llm.Block
	for _, it := range out {
		switch it.Type {
		case "reasoning":
			blocks = append(blocks, llm.Block{Type: llm.BlockReasoning, ReasoningID: it.ID, ReasoningData: it.EncryptedContent})
		case "message":
			for _, c := range it.Content {
				if c.Type == "output_text" && c.Text != "" {
					blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: c.Text})
				}
			}
		case "function_call":
			args := it.Arguments
			if args == "" {
				args = "{}"
			}
			blocks = append(blocks, llm.Block{Type: llm.BlockToolUse, ToolUseID: it.CallID, ToolName: it.Name, ToolInput: json.RawMessage(args)})
		}
	}
	return blocks
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/responses/ -count=1`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/responses/wire.go source/server/internal/llm/responses/adapter.go source/server/internal/llm/responses/adapter_test.go
git commit -m "feat(responses): wire types + llm<->Responses adapter"
```

---

### Task 4: Responses client — Config, NewClient, Chat (non-stream), Name, Capabilities

The non-streaming client: build the request, POST `/responses`, decode, map output → blocks. Uses the httpx retry transport.

**Files:**
- Create: `source/server/internal/llm/responses/client.go`
- Test: `source/server/internal/llm/responses/client_test.go`

**Interfaces:**
- Consumes: `httpx.RetryTransport`/`httpx.RetryPolicy` (Task 1); `messagesToInput`, `toolsToResponses`, `blocksFromOutput`, wire `request`/`response` (Task 3); `llm.ChatRequest`/`llm.ChatResponse`/`llm.Capabilities`.
- Produces: `responses.Config{BaseURL,APIKey,Model}`; `responses.NewClient(Config) *Client`; `(*Client).Name()`, `.Capabilities()`, `.Chat(ctx, llm.ChatRequest) (llm.ChatResponse, error)`, `.StreamChat(...)` (StreamChat added in Task 5 — for now Task 4 defines `Chat` and a placeholder `StreamChat` returning an error so the file compiles, then Task 5 implements it).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/responses/client_test.go`:

```go
package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClientChat(t *testing.T) {
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("path = %q, want .../responses", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_1","status":"completed","output":[
			{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}
		],"usage":{"input_tokens":7,"output_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "gpt-5"})
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// request shape: store=false, include set, instructions carried.
	if gotBody.Store != false || len(gotBody.Include) != 1 || gotBody.Include[0] != "reasoning.encrypted_content" {
		t.Errorf("request store/include wrong: %+v", gotBody)
	}
	if gotBody.Instructions != "sys" || gotBody.Model != "gpt-5" {
		t.Errorf("request instructions/model wrong: %+v", gotBody)
	}
	// response mapping: reasoning + text blocks, usage.
	if len(resp.Blocks) != 2 || resp.Blocks[0].Type != llm.BlockReasoning || resp.Blocks[1].Text != "hello" {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 3 {
		t.Errorf("usage = %d/%d", resp.InputTokens, resp.OutputTokens)
	}
	if c.Name() != "openai-responses" || !c.Capabilities().SupportsVision || !c.Capabilities().SupportsTools {
		t.Errorf("name/caps wrong")
	}
}

func TestClientChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad model","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "x"})
	_, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("expected a readable API error, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/responses/ -run TestClientChat -count=1`
Expected: FAIL — `undefined: NewClient`.

- [ ] **Step 3: Implement the client**

Create `source/server/internal/llm/responses/client.go`:

```go
package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/httpx"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Config holds the Responses client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Client implements llm.Provider using the OpenAI Responses API.
type Client struct {
	http    httpx.Doer
	baseURL string
	apiKey  string
	model   string
}

// NewClient constructs a Client. The HTTP transport retries transient statuses
// (429/5xx) with backoff, shared with the chat client via internal/llm/httpx.
func NewClient(cfg Config) *Client {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	retry := &httpx.RetryTransport{
		Next:   &http.Client{},
		Policy: httpx.RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * 1e6, OnStatus: []int{429, 500, 502, 503}},
	}
	return &Client{http: retry, baseURL: base, apiKey: cfg.APIKey, model: cfg.Model}
}

func (c *Client) Name() string { return "openai-responses" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       false,
		SupportsVision:        true,
	}
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

func (c *Client) buildRequest(req llm.ChatRequest, stream bool) request {
	r := request{
		Model:        modelOr(c.model, req.Model),
		Instructions: req.System,
		Input:        messagesToInput(req.Messages),
		Tools:        toolsToResponses(req.Tools),
		Store:        false,
		Include:      []string{"reasoning.encrypted_content"},
		Stream:       stream,
	}
	if req.MaxTokens > 0 {
		r.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		tmp := req.Temperature
		r.Temperature = &tmp
	}
	return r
}

// do POSTs the request body to /responses and returns the raw http response.
func (c *Client) do(ctx context.Context, body request) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	return c.http.Do(httpReq)
}

// errorFromBody turns a non-2xx response into a readable error.
func errorFromBody(status int, body []byte) error {
	var env struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error != nil && env.Error.Message != "" {
		return fmt.Errorf("responses: %s", env.Error.Message)
	}
	return fmt.Errorf("responses: status %d", status)
}

// Chat sends a non-streaming Responses request and maps output to blocks.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	httpResp, err := c.do(ctx, c.buildRequest(req, false))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return llm.ChatResponse{}, errorFromBody(httpResp.StatusCode, body)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return llm.ChatResponse{}, fmt.Errorf("responses: decode: %w", err)
	}
	out := llm.ChatResponse{Blocks: blocksFromOutput(r.Output), StopReason: r.Status}
	if r.Usage != nil {
		out.InputTokens = r.Usage.InputTokens
		out.OutputTokens = r.Usage.OutputTokens
	}
	return out, nil
}

// StreamChat is implemented in stream.go (Task 5). Placeholder kept minimal so the
// package compiles; replaced in the next task.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, fmt.Errorf("responses: streaming not yet implemented")
}
```

Note: `500 * 1e6` is 500ms in nanoseconds without importing `time` here; if you prefer, import `time` and write `500 * time.Millisecond`. Either compiles — pick the `time` form for readability and add the import.

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/responses/ -count=1`
Expected: PASS (adapter tests + the two client tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/responses/client.go source/server/internal/llm/responses/client_test.go
git commit -m "feat(responses): non-streaming Chat client over /responses"
```

---

### Task 5: Responses streaming — SSE reader → `llm.StreamEvent`

Parse the Responses SSE event stream into our `StreamEvent` vocabulary, including `EventReasoning`. Replace the placeholder `StreamChat`.

**Files:**
- Create: `source/server/internal/llm/responses/stream.go`
- Modify: `source/server/internal/llm/responses/client.go` (replace placeholder `StreamChat`)
- Test: `source/server/internal/llm/responses/stream_test.go`

**Interfaces:**
- Consumes: `llm.StreamReader`, `llm.StreamEvent`, the event consts incl. `EventReasoning` (Task 2); wire `response`/`usage`.
- Produces: `newStreamReader(io.ReadCloser) *streamReader` implementing `llm.StreamReader`; `(*Client).StreamChat` returning it.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/responses/stream_test.go`:

```go
package responses

import (
	"io"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// sseFixture is a recorded Responses SSE stream: a text delta, a function call
// (added → args delta → done), a reasoning item done, then completed w/ usage.
const sseFixture = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Hel"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"lo"}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"get_weather"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"{\"city\":"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"\"Paris\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"}}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":9,"output_tokens":4}}}

`

func collect(t *testing.T, body string) []llm.StreamEvent {
	t.Helper()
	rd := newStreamReader(io.NopCloser(strings.NewReader(body)))
	defer rd.Close()
	var evs []llm.StreamEvent
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		evs = append(evs, ev)
	}
	return evs
}

func TestStreamReader(t *testing.T) {
	evs := collect(t, sseFixture)

	var text string
	var toolArgs string
	var sawStart, sawToolStart, sawToolStop, sawReasoning, sawStop bool
	var in, out int
	var reasoningID, reasoningData string
	for _, ev := range evs {
		switch ev.Type {
		case llm.EventMessageStart:
			sawStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventToolUseStart:
			sawToolStart = true
			if ev.ToolUseID != "call_1" || ev.ToolName != "get_weather" {
				t.Errorf("tool start = %+v", ev)
			}
		case llm.EventToolUseInputDelta:
			toolArgs += ev.TextDelta
		case llm.EventToolUseStop:
			sawToolStop = true
		case llm.EventReasoning:
			sawReasoning = true
			reasoningID, reasoningData = ev.ReasoningID, ev.ReasoningData
		case llm.EventMessageStop:
			sawStop = true
			in, out = ev.InputTokens, ev.OutputTokens
		}
	}
	if !sawStart || !sawToolStart || !sawToolStop || !sawReasoning || !sawStop {
		t.Fatalf("missing events: start=%v toolStart=%v toolStop=%v reasoning=%v stop=%v", sawStart, sawToolStart, sawToolStop, sawReasoning, sawStop)
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if toolArgs != `{"city":"Paris"}` {
		t.Errorf("toolArgs = %q", toolArgs)
	}
	if reasoningID != "rs_1" || reasoningData != "ENC" {
		t.Errorf("reasoning = %q/%q", reasoningID, reasoningData)
	}
	if in != 9 || out != 4 {
		t.Errorf("usage = %d/%d", in, out)
	}
}

func TestStreamReaderError(t *testing.T) {
	body := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n"
	evs := collect(t, body)
	if len(evs) != 1 || evs[0].Type != llm.EventError || !strings.Contains(evs[0].ErrText, "boom") {
		t.Fatalf("events = %+v", evs)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/responses/ -run TestStreamReader -count=1`
Expected: FAIL — `undefined: newStreamReader`.

- [ ] **Step 3: Implement the stream reader**

Create `source/server/internal/llm/responses/stream.go`:

```go
package responses

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"cercano/source/server/internal/llm"
)

// streamReader parses the Responses SSE event stream into llm.StreamEvents. SSE
// frames are separated by a blank line; we read each "data:" payload, JSON-decode
// it, and dispatch on its "type" field. Reasoning encrypted_content arrives on the
// reasoning item's output_item.done and is surfaced as EventReasoning in stream
// order, so collectStream assembles a BlockReasoning before the function_call.
type streamReader struct {
	rc      io.ReadCloser
	br      *bufio.Reader
	pending []llm.StreamEvent
	done    bool
}

func newStreamReader(rc io.ReadCloser) *streamReader {
	return &streamReader{rc: rc, br: bufio.NewReader(rc)}
}

type streamEnvelope struct {
	Type     string      `json:"type"`
	Delta    string      `json:"delta"`
	Item     *streamItem `json:"item"`
	Response *response   `json:"response"`
}

type streamItem struct {
	Type             string `json:"type"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content"`
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	for len(s.pending) == 0 {
		if s.done {
			return llm.StreamEvent{}, false, nil
		}
		data, err := s.readFrame()
		if err == io.EOF {
			s.done = true
			if data == "" {
				return llm.StreamEvent{}, false, nil
			}
		} else if err != nil {
			return llm.StreamEvent{}, false, err
		}
		if data == "" {
			continue
		}
		s.dispatch(data)
	}
	ev := s.pending[0]
	s.pending = s.pending[1:]
	return ev, true, nil
}

// readFrame reads lines until a blank line, returning the concatenated "data:"
// payload for one SSE event. Returns io.EOF when the stream ends.
func (s *streamReader) readFrame() (string, error) {
	var data strings.Builder
	for {
		line, err := s.br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" { // end of frame (or leading blank)
			if err != nil {
				return data.String(), err
			}
			if data.Len() > 0 {
				return data.String(), nil
			}
			continue // skip leading blank lines between frames
		}
		if strings.HasPrefix(trimmed, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		}
		// "event:" and other field lines are ignored; type lives in the JSON.
		if err != nil {
			return data.String(), err
		}
	}
}

func (s *streamReader) dispatch(data string) {
	if data == "[DONE]" {
		s.done = true
		return
	}
	var env streamEnvelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return // ignore unparseable frames
	}
	switch env.Type {
	case "response.created":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventMessageStart})
	case "response.output_text.delta":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: env.Delta})
	case "response.output_item.added":
		if env.Item != nil && env.Item.Type == "function_call" {
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseStart, ToolUseID: env.Item.CallID, ToolName: env.Item.Name})
		}
	case "response.function_call_arguments.delta":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: env.Delta})
	case "response.output_item.done":
		if env.Item == nil {
			return
		}
		switch env.Item.Type {
		case "function_call":
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseStop})
		case "reasoning":
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventReasoning, ReasoningID: env.Item.ID, ReasoningData: env.Item.EncryptedContent})
		}
	case "response.completed":
		ev := llm.StreamEvent{Type: llm.EventMessageStop}
		if env.Response != nil {
			ev.StopReason = env.Response.Status
			if env.Response.Usage != nil {
				ev.InputTokens = env.Response.Usage.InputTokens
				ev.OutputTokens = env.Response.Usage.OutputTokens
			}
		}
		s.pending = append(s.pending, ev)
	case "response.failed", "response.error", "error":
		msg := "responses stream error"
		if env.Response != nil && env.Response.Error != nil && env.Response.Error.Message != "" {
			msg = env.Response.Error.Message
		}
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventError, ErrText: msg})
	}
}

func (s *streamReader) Close() error { return s.rc.Close() }
```

In `source/server/internal/llm/responses/client.go`, replace the placeholder `StreamChat` with the real one:

```go
// StreamChat opens a streaming Responses request and returns a StreamReader that
// emits llm.StreamEvents.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	httpResp, err := c.do(ctx, c.buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, errorFromBody(httpResp.StatusCode, body)
	}
	return newStreamReader(httpResp.Body), nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/responses/ -count=1`
Expected: PASS (adapter + client + the two stream tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/responses/stream.go source/server/internal/llm/responses/client.go source/server/internal/llm/responses/stream_test.go
git commit -m "feat(responses): SSE stream reader + StreamChat (incl. reasoning events)"
```

---

### Task 6: Tool-loop handles `EventReasoning`

`collectStream` appends a `BlockReasoning` (in stream order) when a reasoning event arrives, so streamed turns carry reasoning back exactly like non-streaming.

**Files:**
- Modify: `source/server/internal/agent/toolloop.go` (the `collectStream` switch, ~line 378-415)
- Test: `source/server/internal/agent/toolloop_test.go` (append) — or create a focused test file

**Interfaces:**
- Consumes: `llm.EventReasoning`, `llm.BlockReasoning`, `StreamEvent.ReasoningID/ReasoningData` (Task 2).

- [ ] **Step 1: Write the failing test**

Append to `source/server/internal/agent/toolloop_test.go` (create it if absent with `package agent` + imports). The test drives `collectStream` through a tiny fake `llm.StreamReader`:

```go
func TestCollectStreamCarriesReasoning(t *testing.T) {
	events := []llm.StreamEvent{
		{Type: llm.EventReasoning, ReasoningID: "rs_1", ReasoningData: "ENC"},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, InputTokens: 1, OutputTokens: 1},
	}
	rd := &sliceReader{events: events}
	resp, err := collectStream(context.Background(), rd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d: %+v", len(resp.Blocks), resp.Blocks)
	}
	if resp.Blocks[0].Type != llm.BlockReasoning || resp.Blocks[0].ReasoningID != "rs_1" || resp.Blocks[0].ReasoningData != "ENC" {
		t.Fatalf("block0 = %+v", resp.Blocks[0])
	}
	if resp.Blocks[1].Type != llm.BlockText || resp.Blocks[1].Text != "hi" {
		t.Fatalf("block1 = %+v", resp.Blocks[1])
	}
}

// sliceReader is a fake llm.StreamReader over a fixed event slice.
type sliceReader struct {
	events []llm.StreamEvent
	i      int
}

func (s *sliceReader) Next() (llm.StreamEvent, bool, error) {
	if s.i >= len(s.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := s.events[s.i]
	s.i++
	return ev, true, nil
}
func (s *sliceReader) Close() error { return nil }
```

Ensure the test file imports `context` and `cercano/source/server/internal/llm`. If `toolloop_test.go` already defines a stream-reader fake or imports these, reuse them and drop the duplicates.

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/agent/ -run TestCollectStreamCarriesReasoning -count=1`
Expected: FAIL — `resp.Blocks` has 1 entry (reasoning dropped), assertion fails.

- [ ] **Step 3: Implement the case**

In `source/server/internal/agent/toolloop.go`, inside `collectStream`'s event `switch` (after the `EventToolUseStop` case, before `EventMessageStart`), add:

```go
		case llm.EventReasoning:
			flushText()
			flushTool()
			out.Blocks = append(out.Blocks, llm.Block{
				Type:          llm.BlockReasoning,
				ReasoningID:   ev.ReasoningID,
				ReasoningData: ev.ReasoningData,
			})
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/agent/ -count=1`
Expected: PASS (the new test + existing agent tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go
git commit -m "feat(agent): collectStream carries reasoning blocks from the stream"
```

---

### Task 7: Factory wiring

Fill the reserved `responses` flavor case.

**Files:**
- Modify: `source/server/internal/cloudfactory/factory.go` (the `switch`, ~line 25-36; imports)
- Test: `source/server/internal/cloudfactory/factory_test.go` (append)

**Interfaces:**
- Consumes: `responses.NewClient`/`responses.Config` (Task 4); `FlavorResponses` (already defined, `factory.go:18`).

- [ ] **Step 1: Write the failing test**

Append to `source/server/internal/cloudfactory/factory_test.go`:

```go
func TestBuildResponsesProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "r", Flavor: "responses", Model: "gpt-5"}, "sk")
	if err != nil || p == nil || p.Name() != "openai-responses" {
		t.Fatalf("responses → %v, %v", p, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/cloudfactory/ -run TestBuildResponsesProvider -count=1`
Expected: FAIL — `BuildCloudProvider` returns an error ("flavor \"responses\" not yet supported").

- [ ] **Step 3: Implement the case**

In `source/server/internal/cloudfactory/factory.go`, add the import:

```go
	"cercano/source/server/internal/llm/responses"
```

And add the case to the `switch p.Flavor` (after the `FlavorChatCompletions` case):

```go
	case FlavorResponses:
		return responses.NewClient(responses.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/cloudfactory/ -count=1`
Expected: PASS (new test + existing factory tests, including `TestBuildUnsupportedFlavor` which uses flavor `""`).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cloudfactory/factory.go source/server/internal/cloudfactory/factory_test.go
git commit -m "feat(cloudfactory): wire the responses flavor to the Responses provider"
```

---

### Task 8: Gated integration tests + flip spec status

Live tests behind `INTEGRATION_TEST=1` proving real `Chat`, streaming, a tool call, and a reasoning round-trip; mark the design implemented.

**Files:**
- Create: `source/server/internal/llm/responses/client_integration_test.go`
- Modify: `docs/agent/cloud-responses.md` (status line)

**Interfaces:**
- Consumes: `NewClient`, `Chat`, `StreamChat`.

- [ ] **Step 1: Write the gated tests**

Create `source/server/internal/llm/responses/client_integration_test.go`:

```go
package responses

// Live integration tests against the real OpenAI Responses API.
// Skipped unless INTEGRATION_TEST=1 and OPENAI_API_KEY are set. Model defaults to
// gpt-4o-mini; override with OPENAI_RESPONSES_MODEL (use an o-series model to
// exercise reasoning). Optional OPENAI_BASE_URL points at a compatible endpoint.
//
//   INTEGRATION_TEST=1 OPENAI_API_KEY=sk-... \
//     go test ./internal/llm/responses/ -run Integration -v

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run live Responses tests")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model := os.Getenv("OPENAI_RESPONSES_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return NewClient(Config{BaseURL: os.Getenv("OPENAI_BASE_URL"), APIKey: key, Model: model})
}

func TestIntegration_Chat(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.Chat(ctx, llm.ChatRequest{
		System:   "You are terse. Reply with exactly one word.",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("reply=%q in=%d out=%d", text, resp.InputTokens, resp.OutputTokens)
	if strings.TrimSpace(text) == "" {
		t.Error("empty reply")
	}
}

func TestIntegration_Stream(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rd, err := c.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Count 1 to 5, space separated."}}}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	defer rd.Close()
	var text string
	var sawStop bool
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta {
			text += ev.TextDelta
		}
		if ev.Type == llm.EventMessageStop {
			sawStop = true
		}
	}
	t.Logf("streamed=%q", text)
	if strings.TrimSpace(text) == "" || !sawStop {
		t.Errorf("text=%q sawStop=%v", text, sawStop)
	}
}

func TestIntegration_ToolCall(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.", Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	resp, err := c.Chat(ctx, llm.ChatRequest{
		Tools:    []llm.Tool{weather},
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Call the tool."}}}},
	})
	if err != nil {
		t.Fatalf("Chat(tools): %v", err)
	}
	var called bool
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockToolUse && b.ToolName == "get_weather" {
			called = true
		}
	}
	if !called {
		t.Error("model did not emit a get_weather tool call")
	}
}

func TestIntegration_ReasoningRoundTrip(t *testing.T) {
	model := os.Getenv("OPENAI_RESPONSES_MODEL")
	if model == "" || !strings.HasPrefix(model, "o") {
		t.Skip("set OPENAI_RESPONSES_MODEL to an o-series reasoning model to run this")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	weather := llm.Tool{Name: "get_weather", Description: "Get the current weather for a city.", Schema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
	first, err := c.Chat(ctx, llm.ChatRequest{
		Tools:    []llm.Tool{weather},
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Use the tool."}}}},
	})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	// Confirm a reasoning block came back to carry forward.
	var reasoning, toolUse *llm.Block
	for i := range first.Blocks {
		switch first.Blocks[i].Type {
		case llm.BlockReasoning:
			reasoning = &first.Blocks[i]
		case llm.BlockToolUse:
			toolUse = &first.Blocks[i]
		}
	}
	if reasoning == nil || reasoning.ReasoningData == "" {
		t.Fatal("expected an encrypted reasoning block on turn 1")
	}
	if toolUse == nil {
		t.Skip("model did not call the tool; cannot exercise the round-trip")
	}
	// Second turn: replay assistant blocks (reasoning + tool_use) + a tool result.
	second, err := c.Chat(ctx, llm.ChatRequest{
		Tools: []llm.Tool{weather},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Use the tool."}}},
			{Role: llm.RoleAssistant, Blocks: first.Blocks},
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: toolUse.ToolUseID, Content: "18C and sunny"}}},
		},
	})
	if err != nil {
		t.Fatalf("second turn (reasoning carried): %v", err)
	}
	var text string
	for _, b := range second.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("final=%q", text)
	if strings.TrimSpace(text) == "" {
		t.Error("expected a final answer after carrying reasoning")
	}
}
```

- [ ] **Step 2: Verify gated tests compile + skip without the gate**

Run: `cd source/server && go test ./internal/llm/responses/ -run Integration -v -count=1`
Expected: PASS via SKIP for each ("set INTEGRATION_TEST=1 …" / the reasoning one skips on model prefix). Confirms compilation and gating; a live run is optional manual verification.

- [ ] **Step 3: Flip the spec status**

In `docs/agent/cloud-responses.md`, change:

```markdown
**Status:** Implemented 2026-06-29. Sub-project 3 of the multi-cloud effort
```

- [ ] **Step 4: Run the full server suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS. (Pre-existing flaky `TestPendingCarriesPersist` excepted — if it alone fails, confirm it's unrelated before proceeding.)

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/responses/client_integration_test.go docs/agent/cloud-responses.md
git commit -m "test(responses): gated live tests (chat/stream/tools/reasoning); mark SP3 implemented"
```

---

## Self-Review

**1. Spec coverage** (against `cloud-responses.md`):
- Core decisions: hand-rolled (T3-5), stateless+store=false+include (T4 buildRequest), reasoning unaffected/opaque carry (T2 block, T3 adapter, T6 tool-loop), OpenAI-native no quirks (T4/T7). ✅
- §1 package structure (client/adapter/stream/wire) → T3-5. ✅
- §2 message-model reasoning block → T2. ✅
- §3 translation both directions incl. flat function tools, instructions, store/include → T3, T4. ✅
- §4 streaming SSE→StreamEvent incl. EventReasoning + ordering → T2 (event), T5 (reader), T6 (tool-loop). ✅
- §5 capabilities + factory → T4 (caps), T7 (factory). ✅
- §6 transport: shared httpx retry + object-shaped errors, no array-norm → T1, T4 errorFromBody. ✅
- §7 testing (adapter/stream/client/factory/messages/gated integration) → T2-8. ✅
- Out-of-scope (no previous_response_id, hosted tools, fanout, Bedrock, summaries) → none implemented. ✅

**2. Placeholder scan:** No TBD/TODO. Every code step has complete code; every run step states expected output. Task 4's `StreamChat` placeholder is intentional and explicitly replaced in Task 5 (called out in both tasks). ✅

**3. Type consistency:** `httpx.RetryPolicy{MaxAttempts,BaseDelay,OnStatus}` and `httpx.RetryTransport{Next,Policy}` used identically in T1 and T4. `messagesToInput`/`toolsToResponses`/`blocksFromOutput` signatures (T3) match their call sites in `buildRequest`/`Chat` (T4). Wire field names (`CallID`, `EncryptedContent`, `Arguments`, `Usage.InputTokens`) consistent across `wire.go`, `adapter.go`, `client.go`, `stream.go`. `BlockReasoning`/`EventReasoning` + `ReasoningID`/`ReasoningData` consistent across T2, T3, T5, T6. `Name()` returns `"openai-responses"` in T4 and asserted in T4/T7. ✅
