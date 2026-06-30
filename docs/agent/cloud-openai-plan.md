# OpenAI Provider + Vision Plumbing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add image content to the `llm` message model + every provider adapter (vision plumbing), and add an OpenAI Chat-Completions `llm.Provider` (text + tools + images) wired in via the `chat_completions` flavor.

**Architecture:** Extend `llm.Block` with a `BlockImage` (base64 OR URL) + a shared `ResolveImageBytes` helper. Teach the anthropic and ollama adapters to translate images (ollama fetches URLs server-side). Add `internal/llm/openai/` (`client`/`adapter`/`stream`) wrapping `github.com/sashabaranov/go-openai`, behind the `llm.Provider` interface, registered by `cloudfactory`.

**Tech Stack:** Go, `github.com/sashabaranov/go-openai`, the `anthropic-sdk-go` + `ollama/api` libs already in use, the `internal/llm` interface.

## Global Constraints

- Module path `cercano/source/server`; Go commands from `source/server/`.
- Specs: `docs/agent/vision-input.md`, `docs/agent/cloud-openai.md`.
- `BlockImage` carries **exactly one** of `ImageData` (base64) / `ImageURL` (http(s)); `MediaType` required for base64.
- Image representation supports **both** base64 and URL. Cloud providers (anthropic, openai) take either natively; **Ollama takes bytes only** → resolve URL→bytes via the shared helper.
- OpenAI client uses `sashabaranov/go-openai`, isolated to `internal/llm/openai/` (swap-to-handrolled-later stays cheap).
- `chat_completions` factory case → `openai.NewClient`; capabilities `{SupportsTools:true, SupportsParallelTools:true, SupportsCaching:false, SupportsVision:true}`.
- Adapters with image support set `Capabilities().SupportsVision = true` (anthropic already does).
- Inbound path (CLI attach / proto field) and full SSRF hardening of the URL fetch are OUT OF SCOPE (tested at adapter level only).
- Commit messages must NOT contain the word "Claude".
- Build gate `go build ./...`; test gate `go test ./<pkg>/ -count=1`.

---

### Task 1: `llm` image block + `ResolveImageBytes`

**Files:**
- Modify: `source/server/internal/llm/messages.go`
- Create: `source/server/internal/llm/image.go`
- Test: `source/server/internal/llm/image_test.go`

**Interfaces:**
- Produces: `llm.BlockImage BlockType`; `Block.MediaType`/`Block.ImageData`/`Block.ImageURL` fields; `func ResolveImageBytes(ctx context.Context, b Block) ([]byte, error)`.

- [ ] **Step 1: Write failing test**

Create `image_test.go`:

```go
package llm

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveImageBytes_Base64(t *testing.T) {
	raw := []byte("PNGDATA")
	b := Block{Type: BlockImage, MediaType: "image/png", ImageData: base64.StdEncoding.EncodeToString(raw)}
	got, err := ResolveImageBytes(context.Background(), b)
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("base64 → %q, %v", got, err)
	}
}

func TestResolveImageBytes_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("REMOTEBYTES"))
	}))
	defer srv.Close()
	b := Block{Type: BlockImage, ImageURL: srv.URL}
	got, err := ResolveImageBytes(context.Background(), b)
	if err != nil || string(got) != "REMOTEBYTES" {
		t.Fatalf("url → %q, %v", got, err)
	}
}

func TestResolveImageBytes_BadBase64(t *testing.T) {
	if _, err := ResolveImageBytes(context.Background(), Block{Type: BlockImage, ImageData: "!!!notb64"}); err == nil {
		t.Error("expected error on bad base64")
	}
}

func TestResolveImageBytes_Neither(t *testing.T) {
	if _, err := ResolveImageBytes(context.Background(), Block{Type: BlockImage}); err == nil {
		t.Error("expected error when neither data nor url set")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/llm/ -run ResolveImageBytes -count=1`
Expected: FAIL — `BlockImage`/fields/`ResolveImageBytes` undefined.

- [ ] **Step 3: Add the block const + fields**

In `messages.go`, add to the `const (...)` block: `BlockImage BlockType = "image"`. Add to the `Block` struct (after `IsError`):

```go
	MediaType string `json:"media_type,omitempty"` // image: "image/png" etc (required for base64)
	ImageData string `json:"image_data,omitempty"` // image: base64 bytes
	ImageURL  string `json:"image_url,omitempty"`  // image: http(s) URL
```

- [ ] **Step 4: Implement `ResolveImageBytes`**

Create `image.go`:

```go
package llm

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// maxImageBytes caps a fetched image to guard memory. Generous for screenshots.
const maxImageBytes = 20 << 20 // 20 MiB

// ResolveImageBytes returns the raw bytes of an image block: decoding ImageData
// (base64) or fetching ImageURL (http GET, bounded). Used by adapters whose
// provider can't take a URL (Ollama). NOTE: the URL fetch is an SSRF surface
// once an inbound path supplies untrusted URLs — hardening lands with that path.
func ResolveImageBytes(ctx context.Context, b Block) ([]byte, error) {
	switch {
	case b.ImageData != "":
		data, err := base64.StdEncoding.DecodeString(b.ImageData)
		if err != nil {
			return nil, fmt.Errorf("decode image base64: %w", err)
		}
		return data, nil
	case b.ImageURL != "":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.ImageURL, nil)
		if err != nil {
			return nil, fmt.Errorf("image url request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch image url: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch image url: status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read image url: %w", err)
		}
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("image block has neither ImageData nor ImageURL")
	}
}
```

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./internal/llm/ -count=1 && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/messages.go source/server/internal/llm/image.go source/server/internal/llm/image_test.go
git commit -m "feat(llm): image block + ResolveImageBytes (base64 or URL)"
```

---

### Task 2: Anthropic adapter — image translation

**Files:**
- Modify: `source/server/internal/llm/anthropic/adapter.go`
- Test: `source/server/internal/llm/anthropic/adapter_test.go`

**Interfaces:**
- Consumes: `llm.BlockImage`, the block image fields.

- [ ] **Step 1: Write failing test**

Append to `adapter_test.go` (read the file for its package + existing helpers first):

```go
func TestBlockToSDK_ImageBase64(t *testing.T) {
	b := llm.Block{Type: llm.BlockImage, MediaType: "image/png", ImageData: "QUJD"}
	got := blockToSDK(b)
	// The union must be a non-zero image block. Assert it is not the zero value
	// (a text/empty union); exact field access depends on the SDK shape.
	if isZeroContentBlock(got) {
		t.Error("expected an image content block, got zero union")
	}
}

func TestBlockToSDK_ImageURL(t *testing.T) {
	b := llm.Block{Type: llm.BlockImage, ImageURL: "https://x/y.png"}
	if isZeroContentBlock(blockToSDK(b)) {
		t.Error("expected an image content block for URL, got zero union")
	}
}
```

Add a tiny `isZeroContentBlock(sdk.ContentBlockParamUnion) bool` test helper that reports whether the union is the zero value (compare against `sdk.ContentBlockParamUnion{}` via reflect.DeepEqual). If the existing tests already have a cleaner assertion pattern for the union, use that instead.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/llm/anthropic/ -run Image -count=1`
Expected: FAIL — image returns the zero union (no image case yet).

- [ ] **Step 3: Add the image case to `blockToSDK`**

In `adapter.go` `blockToSDK`, add before the final `return`:

```go
	case llm.BlockImage:
		if b.ImageURL != "" {
			return sdk.NewImageBlock(sdk.URLImageSourceParam{URL: b.ImageURL})
		}
		return sdk.NewImageBlockBase64(b.MediaType, b.ImageData)
```

VERIFY against `anthropic-sdk-go` v1.51.0: the exact constructors are
`sdk.NewImageBlockBase64(mediaType, base64Data string)` and a URL image source.
If the names differ (e.g. `NewImageBlock` takes a different union, or the URL
source type is named differently), read the SDK's image block constructors
(`go doc github.com/anthropics/anthropic-sdk-go | grep -i image`) and use the
actual ones. The behavior required: base64 → base64 image source with media type;
URL → url image source.

- [ ] **Step 4: Run, verify pass; build**

Run: `go test ./internal/llm/anthropic/ -count=1 && go build ./...`
Expected: PASS. (`Capabilities().SupportsVision` is already `true`.)

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/anthropic/
git commit -m "feat(anthropic): translate image blocks (base64 + URL)"
```

---

### Task 3: Ollama adapter — image translation (+ URL fetch)

**Files:**
- Modify: `source/server/internal/llm/ollama/adapter.go`
- Modify: `source/server/internal/llm/ollama/client.go` (Capabilities, and thread ctx to the adapter)
- Test: `source/server/internal/llm/ollama/adapter_test.go`

**Interfaces:**
- Consumes: `llm.ResolveImageBytes`, `llm.BlockImage`.
- Produces: `messageToOllama(ctx context.Context, m llm.Message) (api.Message, error)` (signature changes to take ctx + return error).

- [ ] **Step 1: Write failing test**

Append to `adapter_test.go`:

```go
func TestMessageToOllama_ImageBase64(t *testing.T) {
	m := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "what is this"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "QUJD"}, // "ABC"
	}}
	out, err := messageToOllama(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "what is this" || len(out.Images) != 1 || string(out.Images[0]) != "ABC" {
		t.Errorf("got content=%q images=%v", out.Content, out.Images)
	}
}

func TestMessageToOllama_ImageURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("XYZ"))
	}))
	defer srv.Close()
	m := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockImage, ImageURL: srv.URL}}}
	out, err := messageToOllama(context.Background(), m)
	if err != nil || len(out.Images) != 1 || string(out.Images[0]) != "XYZ" {
		t.Fatalf("got %v / %v", out.Images, err)
	}
}
```

(Add imports `context`, `net/http`, `net/http/httptest` to the test file.)

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/llm/ollama/ -run Image -count=1`
Expected: FAIL — `messageToOllama` has the old signature / no image handling.

- [ ] **Step 3: Update `messageToOllama`**

Change the signature to `func messageToOllama(ctx context.Context, m llm.Message) (api.Message, error)`. In the block loop, add an image case and return the built message + nil at the end (and propagate fetch errors):

```go
		case llm.BlockImage:
			data, err := llm.ResolveImageBytes(ctx, b)
			if err != nil {
				return api.Message{}, fmt.Errorf("ollama image: %w", err)
			}
			out.Images = append(out.Images, api.ImageData(data))
```

The early tool-result return becomes `return api.Message{...}, nil`. Final `return out, nil`. (Confirm `api.Message.Images` is `[]api.ImageData` and `ImageData` is `[]byte` in the resolved `ollama/api` — adjust the conversion if it's a plain `[][]byte`.)

- [ ] **Step 4: Update callers + Capabilities**

In `client.go` (and anywhere else calling `messageToOllama`), thread `ctx` and handle the returned error (the `Chat`/`StreamChat` methods have a `ctx`; on a messages-translation error, return it). Set `SupportsVision: true` in the ollama `Capabilities()`.

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./internal/llm/ollama/ -count=1 && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/ollama/
git commit -m "feat(ollama): translate image blocks; fetch URL images; vision capability"
```

---

### Task 4: OpenAI package — adapter (text, tools, images)

**Files:**
- Create: `source/server/internal/llm/openai/adapter.go`
- Test: `source/server/internal/llm/openai/adapter_test.go`
- Modify: `source/server/go.mod` / `go.sum` (add `github.com/sashabaranov/go-openai`)

**Interfaces:**
- Produces: `messagesToOpenAI([]llm.Message, system string) []openai.ChatCompletionMessage`; `toolsToOpenAI([]llm.Tool) []openai.Tool`; `blocksFromOpenAI(openai.ChatCompletionMessage) []llm.Block`.

- [ ] **Step 1: Add the dependency**

```bash
cd source/server && go get github.com/sashabaranov/go-openai@latest
```

- [ ] **Step 2: Write failing test**

Create `adapter_test.go`:

```go
package openai

import (
	"testing"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

func TestMessagesToOpenAI_SystemAndText(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
	}, "be terse")
	if len(msgs) != 2 || msgs[0].Role != goopenai.ChatMessageRoleSystem || msgs[0].Content != "be terse" {
		t.Fatalf("system msg wrong: %+v", msgs)
	}
	if msgs[1].Role != goopenai.ChatMessageRoleUser || msgs[1].Content != "hi" {
		t.Fatalf("user msg wrong: %+v", msgs[1])
	}
}

func TestMessagesToOpenAI_ImageURLAndBase64(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look"},
		{Type: llm.BlockImage, ImageURL: "https://x/y.png"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "QUJD"},
	}}}, "")
	m := msgs[len(msgs)-1]
	if len(m.MultiContent) != 3 {
		t.Fatalf("want 3 parts, got %d", len(m.MultiContent))
	}
	if m.MultiContent[1].ImageURL.URL != "https://x/y.png" {
		t.Errorf("url part = %q", m.MultiContent[1].ImageURL.URL)
	}
	if m.MultiContent[2].ImageURL.URL != "data:image/png;base64,QUJD" {
		t.Errorf("data-uri part = %q", m.MultiContent[2].ImageURL.URL)
	}
}

func TestMessagesToOpenAI_ToolUseAndResult(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "c1", ToolName: "read", ToolInput: []byte(`{"p":"x"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "c1", Content: "FILE"}}},
	}, "")
	asst := msgs[0]
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "c1" || asst.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("assistant tool_call wrong: %+v", asst)
	}
	tool := msgs[1]
	if tool.Role != goopenai.ChatMessageRoleTool || tool.ToolCallID != "c1" || tool.Content != "FILE" {
		t.Fatalf("tool result wrong: %+v", tool)
	}
}
```

- [ ] **Step 3: Run, verify fail**

Run: `go test ./internal/llm/openai/ -count=1`
Expected: FAIL — undefined functions.

- [ ] **Step 4: Implement `adapter.go`**

```go
package openai

import (
	"fmt"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// messagesToOpenAI maps the block-based llm messages to OpenAI chat messages.
// system (when non-empty) becomes a leading system message.
func messagesToOpenAI(msgs []llm.Message, system string) []goopenai.ChatCompletionMessage {
	out := make([]goopenai.ChatCompletionMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, goopenai.ChatCompletionMessage{Role: goopenai.ChatMessageRoleSystem, Content: system})
	}
	for _, m := range msgs {
		// A tool_result block becomes its own role:"tool" message.
		isToolResult := false
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				out = append(out, goopenai.ChatCompletionMessage{
					Role: goopenai.ChatMessageRoleTool, ToolCallID: b.ToolUseRef, Content: b.Content,
				})
				isToolResult = true
			}
		}
		if isToolResult {
			continue
		}

		cm := goopenai.ChatCompletionMessage{Role: roleToOpenAI(m.Role)}
		var text string
		var parts []goopenai.ChatMessagePart
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				text += b.Text
				parts = append(parts, goopenai.ChatMessagePart{Type: goopenai.ChatMessagePartTypeText, Text: b.Text})
			case llm.BlockImage:
				url := b.ImageURL
				if url == "" {
					url = fmt.Sprintf("data:%s;base64,%s", b.MediaType, b.ImageData)
				}
				parts = append(parts, goopenai.ChatMessagePart{
					Type:     goopenai.ChatMessagePartTypeImageURL,
					ImageURL: &goopenai.ChatMessageImageURL{URL: url},
				})
			case llm.BlockToolUse:
				cm.ToolCalls = append(cm.ToolCalls, goopenai.ToolCall{
					ID: b.ToolUseID, Type: goopenai.ToolTypeFunction,
					Function: goopenai.FunctionCall{Name: b.ToolName, Arguments: string(b.ToolInput)},
				})
			}
		}
		// Use MultiContent only when there's an image; otherwise plain Content
		// (some compat endpoints reject MultiContent for text-only messages).
		hasImage := false
		for _, p := range parts {
			if p.Type == goopenai.ChatMessagePartTypeImageURL {
				hasImage = true
			}
		}
		if hasImage {
			cm.MultiContent = parts
		} else {
			cm.Content = text
		}
		out = append(out, cm)
	}
	return out
}

func roleToOpenAI(r llm.Role) string {
	switch r {
	case llm.RoleAssistant:
		return goopenai.ChatMessageRoleAssistant
	case llm.RoleSystem:
		return goopenai.ChatMessageRoleSystem
	default:
		return goopenai.ChatMessageRoleUser
	}
}

func toolsToOpenAI(tools []llm.Tool) []goopenai.Tool {
	out := make([]goopenai.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, goopenai.Tool{
			Type: goopenai.ToolTypeFunction,
			Function: &goopenai.FunctionDefinition{
				Name: t.Name, Description: t.Description, Parameters: t.Schema,
			},
		})
	}
	return out
}

// blocksFromOpenAI maps a completed assistant message to llm blocks.
func blocksFromOpenAI(m goopenai.ChatCompletionMessage) []llm.Block {
	var blocks []llm.Block
	if m.Content != "" {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, llm.Block{
			Type: llm.BlockToolUse, ToolUseID: tc.ID, ToolName: tc.Function.Name,
			ToolInput: []byte(tc.Function.Arguments),
		})
	}
	return blocks
}
```

VERIFY against the resolved `go-openai`: `ChatMessagePart`, `ChatMessagePartTypeText/ImageURL`, `ChatMessageImageURL{URL}`, `Tool`, `FunctionDefinition{Parameters any}`, `ToolCall{ID,Type,Function}`, `FunctionCall{Name,Arguments}`, role consts. `FunctionDefinition.Parameters` is `any` — passing `t.Schema` (`json.RawMessage`) is accepted and marshals through; if the version requires a `json.RawMessage`/`map`, adapt.

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./internal/llm/openai/ -count=1 && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/openai/ source/server/go.mod source/server/go.sum
git commit -m "feat(openai): adapter — messages, tools, images"
```

---

### Task 5: OpenAI client (`Chat`) + factory case

**Files:**
- Create: `source/server/internal/llm/openai/client.go`
- Test: `source/server/internal/llm/openai/client_test.go`
- Modify: `source/server/internal/cloudfactory/factory.go`

**Interfaces:**
- Consumes: the adapter funcs (Task 4), `llm.Provider`, `cloudfactory.FlavorChatCompletions`.
- Produces: `openai.Config{BaseURL,APIKey,Model}`, `openai.NewClient(Config) *Client`, `Client` implementing `Name/Capabilities/Chat` (StreamChat stub here, real in Task 6).

- [ ] **Step 1: Write failing test**

Create `client_test.go` — point the client's `BaseURL` at an `httptest` server that returns a canned Chat Completions JSON with a tool call, and assert `Chat` returns the mapped blocks + token usage:

```go
package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hello" || resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if c.Name() != "openai" || !c.Capabilities().SupportsTools || !c.Capabilities().SupportsVision {
		t.Errorf("name/caps wrong")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/llm/openai/ -run ClientChat -count=1`
Expected: FAIL — `NewClient`/`Config`/`Chat` undefined.

- [ ] **Step 3: Implement `client.go`**

```go
package openai

import (
	"context"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

type Client struct {
	api   *goopenai.Client
	model string
}

func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model}
}

func (c *Client) Name() string { return "openai" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true, SupportsParallelTools: true, SupportsCaching: false, SupportsVision: true}
}

func (c *Client) buildRequest(req llm.ChatRequest, stream bool) goopenai.ChatCompletionRequest {
	r := goopenai.ChatCompletionRequest{
		Model:    modelOr(c.model, req.Model),
		Messages: messagesToOpenAI(req.Messages, req.System),
		Tools:    toolsToOpenAI(req.Tools),
		Stream:   stream,
	}
	if req.MaxTokens > 0 {
		r.MaxTokens = req.MaxTokens
	}
	return r
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := c.api.CreateChatCompletion(ctx, c.buildRequest(req, false))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	out := llm.ChatResponse{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	if len(resp.Choices) > 0 {
		out.Blocks = blocksFromOpenAI(resp.Choices[0].Message)
		out.StopReason = string(resp.Choices[0].FinishReason)
	}
	return out, nil
}
```

(StreamChat is added in Task 6. To compile `llm.Provider` for the factory now, add a temporary `func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) { return nil, fmt.Errorf("not implemented") }` and remove it in Task 6 — or sequence Task 6 immediately. Mark this stub clearly.)

- [ ] **Step 4: Add the factory case**

In `cloudfactory/factory.go`, add before `default`:

```go
	case FlavorChatCompletions:
		return openai.NewClient(openai.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
```

Add the import `cercano/source/server/internal/llm/openai`. Add a factory test asserting `chat_completions` builds a provider whose `Name()=="openai"`.

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./internal/llm/openai/ ./internal/cloudfactory/ -count=1 && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/openai/client.go source/server/internal/llm/openai/client_test.go source/server/internal/cloudfactory/
git commit -m "feat(openai): Chat client + chat_completions factory case"
```

---

### Task 6: OpenAI streaming (`StreamChat` + tool-call delta accumulation)

**Files:**
- Create: `source/server/internal/llm/openai/stream.go`
- Modify: `source/server/internal/llm/openai/client.go` (real `StreamChat`, drop the stub)
- Test: `source/server/internal/llm/openai/stream_test.go`

**Interfaces:**
- Produces: `func (c *Client) StreamChat(ctx, llm.ChatRequest) (llm.StreamReader, error)` and a `streamReader` emitting `llm.StreamEvent`.

- [ ] **Step 1: Write failing test**

Create `stream_test.go` — point `BaseURL` at an `httptest` server emitting an SSE stream whose tool-call `arguments` are split across two chunks, then a `[DONE]`; assert the reader yields text deltas and a tool-use event with the **reassembled** arguments. Use the canonical Chat Completions SSE shape:

```go
package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, l := range lines {
		fmt.Fprintf(w, "data: %s\n\n", l)
	}
}

func TestStreamChat_ReassemblesToolArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"content":"hi "}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"p\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	rd, err := c.StreamChat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	var text string
	var toolName, toolArgs string
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventToolUse:
			toolName = ev.ToolName
			toolArgs = string(ev.ToolInputRaw)
		}
	}
	if text != "hi " || toolName != "read" || toolArgs != `{"p":"x"}` {
		t.Fatalf("text=%q tool=%q args=%q", text, toolName, toolArgs)
	}
}
```

VERIFY the exact `StreamEventType` const names (`EventTextDelta`, `EventToolUse`, message start/stop) from `internal/llm/stream.go` and match them; the anthropic `stream.go` shows which events the tool-loop consumes — emit the same set.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/llm/openai/ -run StreamChat -count=1`
Expected: FAIL — `StreamChat` not implemented.

- [ ] **Step 3: Implement `stream.go` + real `StreamChat`**

Read `internal/llm/stream.go` (the `StreamEvent` fields + `StreamEventType` consts) and `internal/llm/anthropic/stream.go` (the event sequence the loop expects). Then implement a `streamReader` that wraps `*goopenai.ChatCompletionStream`:

- On each `Recv()`: emit `EventTextDelta` for `delta.Content`; accumulate `delta.ToolCalls[i].Function.Arguments` keyed by `Index`, capturing `ID`/`Name` from the first fragment of each index.
- When a new tool index begins or the stream ends, emit an `EventToolUse` with the accumulated `ToolInputRaw` for the finished index.
- On the final chunk, set `InputTokens`/`OutputTokens` from `Usage` on the terminal event; emit the message-stop event the loop expects.
- Set `req.StreamOptions = &goopenai.StreamOptions{IncludeUsage: true}` in `buildRequest` when `stream==true` so usage arrives.

Because go-openai's `Recv()` is pull-based and one delta may need to produce zero-or-more `llm.StreamEvent`s, the `streamReader` keeps a small queue of pending events drained by `Next()` before calling `Recv()` again. Implement `Next() (llm.StreamEvent, bool, error)` and `Close()`. Replace the Task-5 `StreamChat` stub with:

```go
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	s, err := c.api.CreateChatCompletionStream(ctx, c.buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	return newStreamReader(s), nil
}
```

- [ ] **Step 4: Run, verify pass; build + full package**

Run: `go test ./internal/llm/openai/ -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/openai/stream.go source/server/internal/llm/openai/client.go source/server/internal/llm/openai/stream_test.go
git commit -m "feat(openai): streaming with tool-call delta reassembly"
```

---

### Task 7: Docs

**Files:**
- Modify: `docs/agent/cloud-openai.md`, `docs/agent/vision-input.md` (Status → implemented)
- Modify: `docs/agent/README.md` (common base_url examples)

- [ ] **Step 1: Update status + usage**

Flip both Status lines to implemented (2026-06-27). In the agent README (or cloud-openai.md), add a short "OpenAI-compatible endpoints" table with example `base_url`s (OpenAI default — empty; Gemini `https://generativelanguage.googleapis.com/v1beta/openai`; Groq `https://api.groq.com/openai/v1`) and the `/cloud key`/`use` flow. Note vision is plumbed in adapters but has no inbound capture path yet.

- [ ] **Step 2: Commit**

```bash
git add docs/agent/
git commit -m "docs(agent): OpenAI provider + vision usage; status"
```

---

## Self-Review

**Spec coverage:**
- vision-input §1 image block + ResolveImageBytes → Task 1. ✓
- vision-input §2 anthropic / ollama / openai image translation → Tasks 2, 3, 4. ✓ (ollama URL fetch via ResolveImageBytes → Task 3.)
- vision-input §3 capabilities (SupportsVision) → anthropic already true; ollama Task 3; openai Task 5. ✓
- cloud-openai §1 package structure → Tasks 4–6. ✓
- cloud-openai §2 translation (system/text/tool_use/tool_result/tools/response/images) → Task 4. ✓
- cloud-openai §3 streaming (delta accumulation + IncludeUsage) → Task 6. ✓
- cloud-openai §4 factory + capabilities → Task 5. ✓
- Both inbound paths / SSRF hardening explicitly deferred → not in plan (Global Constraints). ✓

**Placeholder scan:** No TBD/TODO. The "VERIFY against the resolved library" notes (Tasks 2, 4, 5, 6) are bounded — they name the exact symbols and the required behavior; necessary because `go-openai` and `anthropic-sdk-go` field/constructor names vary by version and must be matched against live code. Task 5's `StreamChat` stub is explicitly temporary, removed in Task 6.

**Type consistency:** `BlockImage` + `MediaType`/`ImageData`/`ImageURL` (Task 1) used identically in Tasks 2–4. `ResolveImageBytes(ctx, Block) ([]byte, error)` consumed in Task 3. `messagesToOpenAI`/`toolsToOpenAI`/`blocksFromOpenAI` (Task 4) consumed by `client.go` (Task 5). `openai.Config{BaseURL,APIKey,Model}` + `NewClient` consistent between Task 5 and the factory case. `llm.StreamEvent`/`StreamEventType` const names are flagged for verification against `internal/llm/stream.go` in Task 6 (the plan's `EventTextDelta`/`EventToolUse` are illustrative and must be matched to the real consts).
