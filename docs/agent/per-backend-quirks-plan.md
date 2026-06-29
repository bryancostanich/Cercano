# Per-Backend Quirks Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the one `chat_completions` provider behave well on every OpenAI-compatible backend by isolating each backend's known deviations behind a small `Quirks` descriptor, selected by an explicit `backend` field on the profile.

**Architecture:** Canonical format stays OpenAI Chat Completions (`go-openai` remains the engine). A `Quirks` value (resolved from the profile's `backend`) is applied at two seams: a **transport-side** `HTTPDoer` wrapper that retries transient statuses and rewrites array-shaped error bodies into the object shape `go-openai` expects, and a **request-side** step that resolves URL images to inline base64 for backends that won't fetch URLs. Everything stays inside `internal/llm/openai/`, behind `llm.Provider`.

**Tech Stack:** Go (module `cercano/source/server`); `github.com/sashabaranov/go-openai` v1.41.2; standard library (`net/http`, `encoding/json`, `encoding/base64`); `gopkg.in/yaml.v3` for config.

Design spec: [per-backend-quirks.md](./per-backend-quirks.md). Findings it addresses: [llm-backend-notes.md](./llm-backend-notes.md).

## Global Constraints

- **Library isolation:** `go-openai` (`goopenai` import alias) is referenced ONLY inside `internal/llm/openai/`. No other package learns which library serves OpenAI. (From cloud-openai.md.)
- **`go-openai` version:** v1.41.2. `ClientConfig.HTTPClient` is an `openai.HTTPDoer` (`Do(*http.Request) (*http.Response, error)`).
- **Default quirks are defensive:** unknown/empty `backend` → `ImagesAsBase64: true`, `NormalizeErrors: true`, retry on. `openai` is the one backend that opts OUT of base64 (URL passthrough).
- **`NormalizeErrors` and `Retry` are harmless when unneeded:** normalization only fires on a non-2xx, array-shaped body; retry only on the listed statuses. Both are ON for every known backend.
- **Never buffer a 2xx (streaming) body** in the transport wrapper — only non-2xx error bodies are read/rewritten.
- **Retry defaults:** `MaxAttempts: 3`, `BaseDelay: 500ms` (doubling), `OnStatus: [429, 500, 502, 503]`.
- **Build/test:** from `source/server/`. Build: `go build -o bin/cercano ./cmd/cercano/`. Test a package: `go test ./internal/llm/openai/ -v -count=1`.
- **Out of scope (do not build):** hand-rolling the client; tool-fidelity workarounds; model-name aliasing / param-dropping; SSRF hardening of `ResolveImageBytes`; surfacing `backend` over the gRPC/proto `CloudProfile` RPCs or the `/cloud` CLI (YAML-only for now); Responses (SP3) and Bedrock (SP4).

---

## File Structure

**New files (all in `internal/llm/openai/`):**
- `quirks.go` — `Quirks`, `RetryPolicy`, `defaultRetry()`, `quirksFor(backend string) Quirks`.
- `quirks_test.go` — registry table tests.
- `transport.go` — `normalizingDoer` (implements `goopenai.HTTPDoer`): retry + array-error normalization.
- `transport_test.go` — error-normalization (through a `go-openai` client) + retry/body-replay/ctx-cancel (direct `Do`).

**Modified files:**
- `pkg/config/config.go:13` — add `Backend` to `CloudProfile`.
- `pkg/config/config_test.go` — YAML round-trip for `backend`.
- `internal/cloudfactory/factory.go:33` — pass `p.Backend` into `openai.Config`.
- `internal/cloudfactory/factory_test.go` — assert a `backend`-bearing profile still builds.
- `internal/llm/openai/client.go` — `Config.Backend`; `Client.quirks`; `NewClient` installs the doer + stores quirks; `Chat`/`StreamChat` resolve URL images when `ImagesAsBase64`; `resolveImageURLs` helper.
- `internal/llm/openai/client_test.go` (new file, package `openai`) — `NewClient` quirks resolution; `resolveImageURLs`.
- `internal/llm/openai/client_integration_test.go` — gated clean-error assertion.

---

### Task 1: Quirks descriptor + registry

Pure data + a lookup function. No wiring, fully unit-testable in isolation.

**Files:**
- Create: `source/server/internal/llm/openai/quirks.go`
- Test: `source/server/internal/llm/openai/quirks_test.go`

**Interfaces:**
- Produces:
  - `type RetryPolicy struct { MaxAttempts int; BaseDelay time.Duration; OnStatus []int }`
  - `type Quirks struct { ImagesAsBase64 bool; NormalizeErrors bool; Retry RetryPolicy }`
  - `func quirksFor(backend string) Quirks`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/openai/quirks_test.go`:

```go
package openai

import "testing"

func TestQuirksFor(t *testing.T) {
	if quirksFor("openai").ImagesAsBase64 {
		t.Error("openai should pass image URLs through (ImagesAsBase64=false)")
	}
	if g := quirksFor("gemini"); !g.ImagesAsBase64 || !g.NormalizeErrors {
		t.Errorf("gemini needs base64 images + error normalization, got %+v", g)
	}
	for _, b := range []string{"", "nonsense", "groq"} {
		q := quirksFor(b)
		if !q.ImagesAsBase64 || !q.NormalizeErrors {
			t.Errorf("quirksFor(%q) should be defensive, got %+v", b, q)
		}
		if q.Retry.MaxAttempts < 2 || len(q.Retry.OnStatus) == 0 {
			t.Errorf("quirksFor(%q) should retry, got %+v", b, q.Retry)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/openai/ -run TestQuirksFor -v -count=1`
Expected: FAIL — build error, `undefined: quirksFor`.

- [ ] **Step 3: Write minimal implementation**

Create `source/server/internal/llm/openai/quirks.go`:

```go
package openai

import "time"

// RetryPolicy controls transient-failure retries in the transport wrapper.
type RetryPolicy struct {
	MaxAttempts int           // total attempts incl. the first; <2 disables retry
	BaseDelay   time.Duration // first backoff; doubles each subsequent attempt
	OnStatus    []int         // HTTP statuses that trigger a retry
}

// Quirks captures a backend's known deviations from OpenAI Chat Completions.
// The zero value is the strict-OpenAI baseline; quirksFor turns on the
// defensive options that are safe everywhere.
type Quirks struct {
	ImagesAsBase64  bool // resolve URL images to base64 before send
	NormalizeErrors bool // rewrite array-shaped error bodies to object shape
	Retry           RetryPolicy
}

// defaultRetry is the transient-failure policy shared by all known backends.
func defaultRetry() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		OnStatus:    []int{429, 500, 502, 503},
	}
}

// quirksFor resolves a backend name (the profile's `backend` field) to its
// Quirks. Unknown/empty backends get the defensive default — base64 images,
// error normalization, and retry — all harmless when unneeded. `openai` is the
// one backend that opts out of base64 (it fetches image URLs server-side).
func quirksFor(backend string) Quirks {
	switch backend {
	case "openai":
		return Quirks{ImagesAsBase64: false, NormalizeErrors: true, Retry: defaultRetry()}
	case "gemini":
		return Quirks{ImagesAsBase64: true, NormalizeErrors: true, Retry: defaultRetry()}
	case "groq":
		return Quirks{ImagesAsBase64: true, NormalizeErrors: true, Retry: defaultRetry()}
	default: // "" or unrecognized
		return Quirks{ImagesAsBase64: true, NormalizeErrors: true, Retry: defaultRetry()}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/llm/openai/ -run TestQuirksFor -v -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/openai/quirks.go source/server/internal/llm/openai/quirks_test.go
git commit -m "feat(openai): per-backend quirks descriptor + registry"
```

---

### Task 2: Transport wrapper — retry + array-error normalization

`normalizingDoer` wraps an `HTTPDoer`. Two behaviors: retry transient statuses (replaying the request body), and rewrite array-shaped error bodies so `go-openai` parses the real message. Both reach the raw response before `go-openai` does.

**Files:**
- Create: `source/server/internal/llm/openai/transport.go`
- Test: `source/server/internal/llm/openai/transport_test.go`

**Interfaces:**
- Consumes: `Quirks`, `RetryPolicy` (Task 1); `goopenai.HTTPDoer`.
- Produces:
  - `type normalizingDoer struct { next goopenai.HTTPDoer; quirks Quirks }`
  - `func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error)`

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/llm/openai/transport_test.go`:

```go
package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// clientTo builds a go-openai client whose transport is a normalizingDoer
// pointed at srv, with the given quirks.
func clientTo(srv *httptest.Server, q Quirks) *goopenai.Client {
	cfg := goopenai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL
	cfg.HTTPClient = &normalizingDoer{next: &http.Client{}, quirks: q}
	return goopenai.NewClientWithConfig(cfg)
}

func chatReq() goopenai.ChatCompletionRequest {
	return goopenai.ChatCompletionRequest{
		Model:    "m",
		Messages: []goopenai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	}
}

// TestNormalizeArrayError: a Gemini-style array error body yields a clean,
// parsed APIError message instead of go-openai's "unmarshal array" artifact.
func TestNormalizeArrayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `[{"error":{"message":"Cannot fetch content from the provided URL","type":"invalid_request_error","code":400}}]`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: true}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "unmarshal array") {
		t.Fatalf("error not normalized: %v", err)
	}
	if !strings.Contains(err.Error(), "Cannot fetch content") {
		t.Fatalf("real message lost: %v", err)
	}
}

// TestObjectErrorUnchanged: an already-object error body still parses cleanly.
func TestObjectErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad request here","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: true}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil || !strings.Contains(err.Error(), "bad request here") {
		t.Fatalf("expected clean object error, got %v", err)
	}
}

// TestRetryThenSuccess: 503 twice then 200 → one success, three attempts.
func TestRetryThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"high demand"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	q := Quirks{Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected 3 attempts, got %d", hits)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestRetryExhausted: always 503 → error after exactly MaxAttempts attempts.
func TestRetryExhausted(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()

	q := Quirks{NormalizeErrors: true, Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	_, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected 3 attempts, got %d", hits)
	}
}

// TestRetryBodyResent: each attempt re-sends the full request body.
func TestRetryBodyResent(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	q := Quirks{Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("body not resent identically: %#v", bodies)
	}
}

// TestRetryContextCancel: a cancelled context aborts the backoff promptly.
func TestRetryContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	q := Quirks{Retry: RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, OnStatus: []int{503}}}

	done := make(chan struct{})
	go func() {
		clientTo(srv, q).CreateChatCompletion(ctx, chatReq())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("context cancel did not abort backoff")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/llm/openai/ -run 'TestNormalize|TestObject|TestRetry' -v -count=1`
Expected: FAIL — build error, `undefined: normalizingDoer`.

- [ ] **Step 3: Write minimal implementation**

Create `source/server/internal/llm/openai/transport.go`:

```go
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// normalizingDoer wraps an HTTPDoer to repair per-backend response quirks before
// go-openai parses them: it retries transient statuses (replaying the request
// body) and rewrites array-shaped error bodies into the object shape go-openai's
// ErrorResponse expects. 2xx (including streaming) responses pass through
// untouched — their bodies are never buffered here.
type normalizingDoer struct {
	next   goopenai.HTTPDoer
	quirks Quirks
}

func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = b
		}
		resp, err := d.next.Do(req)
		if d.shouldRetry(resp, err, attempt) {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if !sleepBackoff(req.Context(), d.quirks.Retry.BaseDelay, attempt) {
				return nil, req.Context().Err()
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return d.normalize(resp), nil
	}
}

// shouldRetry reports whether to retry. Transport errors (context, DNS, reset)
// are not retried here — only the configured transient HTTP statuses, and only
// while attempts remain.
func (d *normalizingDoer) shouldRetry(resp *http.Response, err error, attempt int) bool {
	rp := d.quirks.Retry
	if attempt >= rp.MaxAttempts || err != nil || resp == nil {
		return false
	}
	for _, s := range rp.OnStatus {
		if resp.StatusCode == s {
			return true
		}
	}
	return false
}

// sleepBackoff waits BaseDelay*2^(attempt-1), aborting on ctx cancellation.
// Returns false if the context ended during the wait.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) bool {
	t := time.NewTimer(base << (attempt - 1))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// normalize rewrites an array-shaped error body to the object shape when the
// backend needs it. Only non-2xx bodies are read; 2xx responses pass through so
// streaming bodies are never consumed.
func (d *normalizingDoer) normalize(resp *http.Response) *http.Response {
	if !d.quirks.NormalizeErrors || resp.StatusCode < 400 {
		return resp
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		resp.ContentLength = 0
		return resp
	}
	if fixed, ok := arrayErrorToObject(body); ok {
		body = fixed
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp
}

// arrayErrorToObject unwraps a `[{...}]` error body to its first object element.
// Returns (newBody, true) only when the body is a non-empty JSON array.
func arrayErrorToObject(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil || len(arr) == 0 {
		return nil, false
	}
	return arr[0], true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/llm/openai/ -run 'TestNormalize|TestObject|TestRetry' -v -count=1`
Expected: PASS (all six).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/openai/transport.go source/server/internal/llm/openai/transport_test.go
git commit -m "feat(openai): normalizing transport — retry + array-error rewrite"
```

---

### Task 3: Wire `backend` through config → factory → client

Add the `Backend` field, thread it to the client, and install the transport wrapper + store the resolved quirks on the client.

**Files:**
- Modify: `source/server/pkg/config/config.go:13-18`
- Modify: `source/server/pkg/config/config_test.go` (append a test)
- Modify: `source/server/internal/cloudfactory/factory.go:33`
- Modify: `source/server/internal/cloudfactory/factory_test.go` (append a test)
- Modify: `source/server/internal/llm/openai/client.go:12-31`
- Create: `source/server/internal/llm/openai/client_test.go`

**Interfaces:**
- Consumes: `quirksFor` (Task 1), `normalizingDoer` (Task 2).
- Produces:
  - `config.CloudProfile.Backend string` (yaml `backend`)
  - `openai.Config.Backend string`
  - `Client.quirks Quirks` (unexported; read by same-package tests)
  - `NewClient` installs `&normalizingDoer{next: &http.Client{}, quirks}` as `ClientConfig.HTTPClient`.

- [ ] **Step 1: Write the failing tests**

Append to `source/server/pkg/config/config_test.go`:

```go
func TestCloudProfileBackendYAML(t *testing.T) {
	var p CloudProfile
	y := "name: g\nflavor: chat_completions\nbackend: gemini\nbase_url: x\nmodel: m\n"
	if err := yaml.Unmarshal([]byte(y), &p); err != nil {
		t.Fatal(err)
	}
	if p.Backend != "gemini" {
		t.Errorf("backend=%q, want gemini", p.Backend)
	}
}
```

This needs the yaml import in the test file. Add it to `config_test.go`'s import block:

```go
import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)
```

Create `source/server/internal/llm/openai/client_test.go`:

```go
package openai

import (
	"reflect"
	"testing"
)

func TestNewClientResolvesQuirks(t *testing.T) {
	c := NewClient(Config{Backend: "gemini", Model: "gemini-2.5-flash"})
	if !reflect.DeepEqual(c.quirks, quirksFor("gemini")) {
		t.Errorf("client quirks = %+v, want %+v", c.quirks, quirksFor("gemini"))
	}
}

func TestNewClientDefaultQuirks(t *testing.T) {
	c := NewClient(Config{}) // empty backend → defensive default
	if !c.quirks.ImagesAsBase64 || !c.quirks.NormalizeErrors {
		t.Errorf("empty backend should get defensive quirks, got %+v", c.quirks)
	}
}
```

Append to `source/server/internal/cloudfactory/factory_test.go`:

```go
func TestBuildChatCompletionsWithBackend(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{
		Name: "g", Flavor: "chat_completions", Backend: "gemini", Model: "gemini-2.5-flash",
	}, "sk")
	if err != nil || p == nil || p.Name() != "openai" {
		t.Fatalf("chat_completions+backend → %v, %v", p, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./pkg/config/ ./internal/llm/openai/ ./internal/cloudfactory/ -run 'Backend|ResolvesQuirks|DefaultQuirks' -v -count=1`
Expected: FAIL — `unknown field Backend` / `c.quirks undefined`.

- [ ] **Step 3: Write the implementations**

In `source/server/pkg/config/config.go`, add the field to `CloudProfile`:

```go
type CloudProfile struct {
	Name    string `yaml:"name"`
	Flavor  string `yaml:"flavor"` // messages | chat_completions | responses | bedrock
	Backend string `yaml:"backend,omitempty"` // chat_completions only: selects per-backend quirks (openai|gemini|groq|…); empty → defensive default
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}
```

In `source/server/internal/cloudfactory/factory.go`, pass it through (line 33):

```go
	case FlavorChatCompletions:
		return openai.NewClient(openai.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model, Backend: p.Backend}), nil
```

In `source/server/internal/llm/openai/client.go`, replace lines 1-31 (imports, `Config`, `Client`, `NewClient`):

```go
package openai

import (
	"context"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// Config holds the OpenAI client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Backend string // selects per-backend quirks; empty → defensive default
}

// Client implements llm.Provider using the OpenAI chat completions API.
type Client struct {
	api    *goopenai.Client
	model  string
	quirks Quirks
}

// NewClient constructs a Client from cfg. The HTTP transport is wrapped in a
// normalizingDoer so per-backend response quirks (transient retries, array-shaped
// error bodies) are repaired before go-openai parses them.
func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	q := quirksFor(cfg.Backend)
	c.HTTPClient = &normalizingDoer{next: &http.Client{}, quirks: q}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model, quirks: q}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./pkg/config/ ./internal/llm/openai/ ./internal/cloudfactory/ -count=1`
Expected: PASS (new + existing tests in all three packages).

- [ ] **Step 5: Commit**

```bash
git add source/server/pkg/config/config.go source/server/pkg/config/config_test.go \
  source/server/internal/cloudfactory/factory.go source/server/internal/cloudfactory/factory_test.go \
  source/server/internal/llm/openai/client.go source/server/internal/llm/openai/client_test.go
git commit -m "feat(openai): thread backend field; install normalizing transport"
```

---

### Task 4: Request-side image URL → base64

When `quirks.ImagesAsBase64`, resolve URL image blocks to inline base64 before building the request, so backends that won't fetch URLs still get the image.

**Files:**
- Modify: `source/server/internal/llm/openai/client.go` (add `resolveImageURLs`; call it in `Chat` and `StreamChat`)
- Modify: `source/server/internal/llm/openai/client_test.go` (append tests)

**Interfaces:**
- Consumes: `llm.ResolveImageBytes(ctx, llm.Block) ([]byte, error)` (`internal/llm/image.go:18`); `llm.Block` fields `Type`, `ImageURL`, `ImageData`, `MediaType`; `llm.BlockImage`.
- Produces: `func resolveImageURLs(ctx context.Context, msgs []llm.Message) ([]llm.Message, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `source/server/internal/llm/openai/client_test.go` (and extend its imports to the block below):

```go
import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

// redPNGBytes returns a small solid-red PNG. http.DetectContentType reports
// "image/png" for it.
func redPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestResolveImageURLs(t *testing.T) {
	pngBytes := redPNGBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	}))
	defer srv.Close()

	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "what color?"},
		{Type: llm.BlockImage, ImageURL: srv.URL},
	}}}

	out, err := resolveImageURLs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	got := out[0].Blocks[1]
	if got.ImageURL != "" {
		t.Error("ImageURL should be cleared after resolution")
	}
	if got.ImageData == "" {
		t.Error("ImageData should be populated")
	}
	if got.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", got.MediaType)
	}
	// Copy-on-write: the caller's original slice must be untouched.
	if msgs[0].Blocks[1].ImageURL != srv.URL {
		t.Error("resolveImageURLs mutated the caller's messages")
	}
}

func TestResolveImageURLsNoop(t *testing.T) {
	// A base64 block (no URL) and a text block pass through unchanged.
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "hi"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAAA"},
	}}}
	out, err := resolveImageURLs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, msgs) {
		t.Errorf("no-op changed messages: %+v", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/llm/openai/ -run TestResolveImageURLs -v -count=1`
Expected: FAIL — `undefined: resolveImageURLs`.

- [ ] **Step 3: Write the implementation**

In `source/server/internal/llm/openai/client.go`, extend the import block to add `encoding/base64`:

```go
import (
	"context"
	"encoding/base64"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)
```

Add the helper (place it after `NewClient`):

```go
// resolveImageURLs replaces URL image blocks with inline base64, so backends
// that won't fetch image URLs (e.g. the Gemini-compat shim) still receive the
// image. Called only when quirks.ImagesAsBase64 is set. Text blocks and
// already-base64 image blocks pass through. The caller's slice is not mutated
// (copy-on-write per message).
func resolveImageURLs(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		blocks := m.Blocks
		copied := false
		for j, b := range m.Blocks {
			if b.Type != llm.BlockImage || b.ImageURL == "" {
				continue
			}
			data, err := llm.ResolveImageBytes(ctx, b)
			if err != nil {
				return nil, err
			}
			if !copied {
				blocks = append([]llm.Block(nil), m.Blocks...)
				copied = true
			}
			blocks[j].ImageURL = ""
			blocks[j].ImageData = base64.StdEncoding.EncodeToString(data)
			blocks[j].MediaType = http.DetectContentType(data)
		}
		out[i].Blocks = blocks
	}
	return out, nil
}
```

In `Chat`, resolve images before building the request:

```go
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		req.Messages = msgs
	}
	resp, err := c.api.CreateChatCompletion(ctx, c.buildRequest(req, false))
	// ... unchanged below ...
```

In `StreamChat`, the same guard:

```go
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return nil, err
		}
		req.Messages = msgs
	}
	stream, err := c.api.CreateChatCompletionStream(ctx, c.buildRequest(req, true))
	// ... unchanged below ...
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/llm/openai/ -count=1`
Expected: PASS (whole package, including Task 1–3 tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/openai/client.go source/server/internal/llm/openai/client_test.go
git commit -m "feat(openai): resolve URL images to base64 for non-fetching backends"
```

---

### Task 5: Gated clean-error integration assertion + flip spec status

Prove end-to-end that a real compat backend's error surfaces a readable message (not a JSON artifact), and mark the design implemented. The integration test stays gated (`INTEGRATION_TEST=1`), so the default suite is unaffected.

**Files:**
- Modify: `source/server/internal/llm/openai/client_integration_test.go` (append one test)
- Modify: `docs/agent/per-backend-quirks.md` (status line)

**Interfaces:**
- Consumes: `liveClient(t)`, `redPNGBase64` already present in the integration file; `c.Chat`.

- [ ] **Step 1: Write the gated test**

Append to `source/server/internal/llm/openai/client_integration_test.go`:

```go
func TestIntegration_CleanError(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A bogus model name is rejected by the backend. With error normalization
	// the message must be human-readable, not go-openai's "unmarshal array".
	_, err := c.Chat(ctx, llm.ChatRequest{
		Model:    "definitely-not-a-real-model-xyz",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("expected an error for a bogus model")
	}
	if strings.Contains(err.Error(), "unmarshal array") {
		t.Errorf("error not normalized: %v", err)
	}
	t.Logf("clean error: %v", err)
}
```

- [ ] **Step 2: Verify it compiles and is skipped without the gate**

Run: `cd source/server && go test ./internal/llm/openai/ -run TestIntegration_CleanError -v -count=1`
Expected: PASS via `SKIP` ("set INTEGRATION_TEST=1 …") — confirms it compiles and is gated. (A live run with `INTEGRATION_TEST=1 OPENAI_API_KEY=… OPENAI_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai OPENAI_MODEL=gemini-2.5-flash` is optional manual verification.)

- [ ] **Step 3: Flip the spec status**

In `docs/agent/per-backend-quirks.md`, change the status line:

```markdown
**Status:** Implemented 2026-06-28. Follow-on robustness work for the
```

- [ ] **Step 4: Run the full server test suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS (pre-existing flaky `TestPendingCarriesPersist` excepted — if it fails, confirm it is unrelated to this change before proceeding).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/openai/client_integration_test.go docs/agent/per-backend-quirks.md
git commit -m "test(openai): gated clean-error assertion; mark quirks layer implemented"
```

---

## Self-Review

**1. Spec coverage** (against `per-backend-quirks.md`):
- §1 Backend identity (config `Backend` field, factory pass-through) → Task 3. ✅
- §2 Quirks descriptor + registry + default table → Task 1. ✅
- §3 Transport `normalizingDoer` (retry + array-error normalization) → Task 2. ✅
- §4 Request-side image base64 (`ResolveImageBytes`, client layer, adapter unchanged) → Task 4. ✅
- §5 Wiring summary (config, factory, NewClient, quirks.go, transport.go) → Tasks 1–4. ✅
- §6 Testing: quirks table (T1), transport error-norm + retry (T2), request image base64 (T4), factory backend (T3), gated integration error path (T5). ✅
- Out-of-scope items (hand-roll, tool fidelity, aliasing, SSRF, proto/CLI surfacing, SP3/SP4) → none implemented. ✅

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code; every command has expected output. ✅

**3. Type consistency:** `Quirks`/`RetryPolicy`/`quirksFor` (T1) used verbatim in `normalizingDoer` (T2) and `NewClient`/`Client.quirks` (T3). `resolveImageURLs(ctx, []llm.Message) ([]llm.Message, error)` (T4) matches its call sites in `Chat`/`StreamChat`. `Config.Backend`/`CloudProfile.Backend` names align across factory and config. `Quirks` is compared with `reflect.DeepEqual` (it contains a slice — not `==`-comparable). ✅
