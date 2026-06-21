# Native Tool Calling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire native tool calling end-to-end: layered provider abstraction (Anthropic via Meridian + Ollama) → bounded autonomous agent loop with R-tier concurrency and W/X serialization → permission modes (strict / permissive / bypass) gated agent-side via streaming events → lossless persistence of tool_use/tool_result blocks → CLI repointing of the existing confirm UI onto agent events.

**Architecture:** Per-provider package under `internal/llm/` behind a shared `Provider` interface, each owning its native wire protocol. The agent loop lives in `internal/agent/toolloop.go` and consumes the provider's typed stream events. Confirmation is an agent-side decision surfaced to clients as `PermissionRequired` stream events with paired unary RPCs (`AllowToolCall` / `DenyToolCall`) carrying decisions back. Conversation persistence extends `turns` with a `content_json` column carrying the Anthropic block-array shape verbatim.

**Tech Stack:** Go 1.24+, `github.com/anthropics/anthropic-sdk-go` v1.51+, `github.com/ollama/ollama/api` (official), existing protobuf/gRPC, existing `modernc.org/sqlite`. Drops `github.com/tmc/langchaingo` from the cloud path (kept available, not used).

**Reference spec:** `docs/plans/native_tool_calling.md`

---

## File Structure

**New files:**

```
source/server/internal/llm/provider.go            # Provider interface, Capabilities
source/server/internal/llm/messages.go            # Message, Block, BlockType
source/server/internal/llm/tools.go               # Tool, ToolChoice
source/server/internal/llm/stream.go              # StreamEvent union
source/server/internal/llm/anthropic/client.go    # SDK wiring + UA RoundTripper
source/server/internal/llm/anthropic/adapter.go   # Block ↔ ContentBlock
source/server/internal/llm/anthropic/stream.go    # SSE → StreamEvent
source/server/internal/llm/ollama/client.go       # ollama/api wiring
source/server/internal/llm/ollama/adapter.go     # Block ↔ ollama Message
source/server/internal/llm/ollama/stream.go      # NDJSON → StreamEvent
source/server/internal/agent/toolloop.go          # Bounded autonomous loop
source/server/internal/agent/permissions.go      # PermissionMode + gate decision
source/server/internal/agenttools/catalog.go     # BuildToolCatalog(registry)
source/server/internal/cli/ui/scrollback_tool.go # Folded tool-call entry
source/server/internal/cli/slash/permissions.go  # /strict /permissive /bypass
```

**Modified files:**

```
source/proto/agent.proto                          # new RPCs, new stream events, content_json
source/server/internal/conversation/schema.sql   # ALTER turns ADD content_json
source/server/internal/conversation/store.go     # Append + GetTurns + BlocksJSON
source/server/internal/agent/agent.go             # Wire toolloop into ProcessRequest path
source/server/internal/cli/agentclient/client.go # new RPCs
source/server/internal/cli/ui/model.go           # PermissionRequired event handling
source/server/internal/cli/slash/registry.go     # register permission commands
```

**Test files (paired with each implementation file):**

```
source/server/internal/llm/messages_test.go
source/server/internal/llm/tools_test.go
source/server/internal/llm/anthropic/client_test.go
source/server/internal/llm/anthropic/adapter_test.go
source/server/internal/llm/anthropic/stream_test.go
source/server/internal/llm/ollama/adapter_test.go
source/server/internal/llm/ollama/stream_test.go
source/server/internal/agent/toolloop_test.go
source/server/internal/agent/permissions_test.go
source/server/internal/agenttools/catalog_test.go
source/server/internal/cli/ui/scrollback_tool_test.go
source/server/internal/cli/slash/permissions_test.go
```

---

## Phase 1 — Internal Types

### Task 1: Block + BlockType + Message

**Files:**
- Create: `source/server/internal/llm/messages.go`
- Create: `source/server/internal/llm/messages_test.go`

- [ ] **Step 1: Write the failing test**

`source/server/internal/llm/messages_test.go`:

```go
package llm

import (
	"encoding/json"
	"testing"
)

func TestBlock_RoundTrip_Text(t *testing.T) {
	in := Block{Type: BlockText, Text: "hello"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Block
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != BlockText || out.Text != "hello" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_ToolUse(t *testing.T) {
	in := Block{
		Type:      BlockToolUse,
		ToolUseID: "toolu_01",
		ToolName:  "read_file",
		ToolInput: json.RawMessage(`{"path":"main.go"}`),
	}
	b, _ := json.Marshal(in)
	var out Block
	_ = json.Unmarshal(b, &out)
	if out.ToolName != "read_file" || string(out.ToolInput) != `{"path":"main.go"}` {
		t.Errorf("tool_use round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_ToolResult(t *testing.T) {
	in := Block{
		Type:       BlockToolResult,
		ToolUseRef: "toolu_01",
		Content:    "32 lines",
		IsError:    false,
	}
	b, _ := json.Marshal(in)
	var out Block
	_ = json.Unmarshal(b, &out)
	if out.ToolUseRef != "toolu_01" || out.Content != "32 lines" {
		t.Errorf("tool_result round-trip mismatch: %+v", out)
	}
}

func TestMessage_OrderedBlocks(t *testing.T) {
	m := Message{Role: RoleAssistant, Blocks: []Block{
		{Type: BlockText, Text: "I'll read it."},
		{Type: BlockToolUse, ToolUseID: "u1", ToolName: "read_file"},
	}}
	if len(m.Blocks) != 2 || m.Blocks[1].ToolName != "read_file" {
		t.Errorf("message blocks not preserved: %+v", m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/ -run TestBlock -v`
Expected: FAIL with "undefined: Block" / "undefined: BlockText".

- [ ] **Step 3: Implement minimal types**

`source/server/internal/llm/messages.go`:

```go
package llm

import "encoding/json"

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Block struct {
	Type BlockType `json:"type"`

	Text string `json:"text,omitempty"`

	ToolUseID string          `json:"id,omitempty"`
	ToolName  string          `json:"name,omitempty"`
	ToolInput json.RawMessage `json:"input,omitempty"`

	ToolUseRef string `json:"tool_use_id,omitempty"`
	Content    string `json:"content,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`

	ProviderExtras map[string]any `json:"-"`
}

type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"content"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/llm/ -run TestBlock -v && go test ./internal/llm/ -run TestMessage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/messages.go source/server/internal/llm/messages_test.go
git commit -m "feat(llm): internal Block + Message types"
```

---

### Task 2: Tool + ToolChoice

**Files:**
- Create: `source/server/internal/llm/tools.go`
- Create: `source/server/internal/llm/tools_test.go`

- [ ] **Step 1: Write the failing test**

```go
package llm

import (
	"encoding/json"
	"testing"
)

func TestTool_Fields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	tl := Tool{Name: "read_file", Description: "Read a file", Schema: schema, Permission: PermR}
	if tl.Name != "read_file" || tl.Permission != PermR {
		t.Errorf("Tool fields: %+v", tl)
	}
}

func TestToolChoice_Constants(t *testing.T) {
	cases := []ToolChoice{
		{Type: ToolChoiceAuto},
		{Type: ToolChoiceAny},
		{Type: ToolChoiceNone},
		{Type: ToolChoiceTool, Name: "read_file"},
	}
	for _, c := range cases {
		if c.Type == "" {
			t.Errorf("empty type")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/ -run TestTool -v`
Expected: FAIL with "undefined: Tool" / "undefined: PermR".

- [ ] **Step 3: Implement Tool + ToolChoice + Permission**

`source/server/internal/llm/tools.go`:

```go
package llm

import "encoding/json"

type Permission string

const (
	PermR Permission = "R"
	PermW Permission = "W"
	PermX Permission = "X"
)

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Permission  Permission
}

type ToolChoiceType string

const (
	ToolChoiceAuto ToolChoiceType = "auto"
	ToolChoiceAny  ToolChoiceType = "any"
	ToolChoiceTool ToolChoiceType = "tool"
	ToolChoiceNone ToolChoiceType = "none"
)

type ToolChoice struct {
	Type ToolChoiceType
	Name string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/llm/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/tools.go source/server/internal/llm/tools_test.go
git commit -m "feat(llm): internal Tool + ToolChoice + Permission types"
```

---

### Task 3: StreamEvent union

**Files:**
- Create: `source/server/internal/llm/stream.go`

- [ ] **Step 1: Implement StreamEvent types**

No test for this task — types are exercised by adapter tests in later phases. Stub with concrete types that downstream code will consume.

`source/server/internal/llm/stream.go`:

```go
package llm

import "encoding/json"

type StreamEventType string

const (
	EventMessageStart        StreamEventType = "message_start"
	EventTextDelta           StreamEventType = "text_delta"
	EventToolUseStart        StreamEventType = "tool_use_start"
	EventToolUseInputDelta   StreamEventType = "tool_use_input_delta"
	EventToolUseStop         StreamEventType = "tool_use_stop"
	EventMessageStop         StreamEventType = "message_stop"
	EventError               StreamEventType = "error"
)

type StreamEvent struct {
	Type StreamEventType

	TextDelta string

	ToolUseID    string
	ToolName     string
	ToolInputRaw json.RawMessage

	StopReason string

	ErrText string
}

type StreamReader interface {
	Next() (StreamEvent, bool, error)
	Close() error
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd source/server && go build ./internal/llm/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/llm/stream.go
git commit -m "feat(llm): StreamEvent union + StreamReader interface"
```

---

### Task 4: Provider interface + Capabilities + ChatRequest

**Files:**
- Create: `source/server/internal/llm/provider.go`

- [ ] **Step 1: Implement interface**

No test — exercised by adapter conformance tests later. This file is a contract definition.

`source/server/internal/llm/provider.go`:

```go
package llm

import "context"

type Capabilities struct {
	SupportsTools         bool
	SupportsParallelTools bool
	SupportsCaching       bool
	SupportsVision        bool
	MaxToolsPerCall       int
}

type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []Tool
	ToolChoice  ToolChoice
	MaxTokens   int
	Temperature float64
}

type ChatResponse struct {
	Blocks     []Block
	StopReason string
	InputTokens  int
	OutputTokens int
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd source/server && go build ./internal/llm/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/llm/provider.go
git commit -m "feat(llm): Provider interface + Capabilities + ChatRequest/Response"
```

---

## Phase 2 — Anthropic Adapter

### Task 5: Add anthropic-sdk-go dependency

**Files:**
- Modify: `source/server/go.mod`

- [ ] **Step 1: Add the dependency**

Run:
```bash
cd source/server && go get github.com/anthropics/anthropic-sdk-go@v1.51.0 && go mod tidy
```

- [ ] **Step 2: Verify it's in go.mod**

Run: `grep anthropic-sdk-go source/server/go.mod`
Expected: a line like `github.com/anthropics/anthropic-sdk-go v1.51.0`.

- [ ] **Step 3: Commit**

```bash
git add source/server/go.mod source/server/go.sum
git commit -m "deps: add anthropic-sdk-go v1.51.0"
```

---

### Task 6: Anthropic adapter — Block ↔ ContentBlock

**Files:**
- Create: `source/server/internal/llm/anthropic/adapter.go`
- Create: `source/server/internal/llm/anthropic/adapter_test.go`

- [ ] **Step 1: Write the failing test**

`source/server/internal/llm/anthropic/adapter_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"cercano/source/server/internal/llm"
)

func TestToSDKBlock_Text(t *testing.T) {
	got := blockToSDK(llm.Block{Type: llm.BlockText, Text: "hello"})
	if got.OfText == nil || got.OfText.Text != "hello" {
		t.Errorf("text block: %+v", got)
	}
}

func TestToSDKBlock_ToolUse(t *testing.T) {
	got := blockToSDK(llm.Block{
		Type:      llm.BlockToolUse,
		ToolUseID: "u1",
		ToolName:  "read_file",
		ToolInput: json.RawMessage(`{"path":"x"}`),
	})
	if got.OfToolUse == nil || got.OfToolUse.ID != "u1" || got.OfToolUse.Name != "read_file" {
		t.Errorf("tool_use block: %+v", got)
	}
}

func TestToSDKBlock_ToolResult(t *testing.T) {
	got := blockToSDK(llm.Block{
		Type:       llm.BlockToolResult,
		ToolUseRef: "u1",
		Content:    "32 lines",
		IsError:    false,
	})
	if got.OfToolResult == nil || got.OfToolResult.ToolUseID != "u1" {
		t.Errorf("tool_result block: %+v", got)
	}
}

func TestFromSDKBlock_Text(t *testing.T) {
	in := sdk.ContentBlockUnion{OfText: &sdk.TextBlock{Text: "world"}}
	got := blockFromSDK(in)
	if got.Type != llm.BlockText || got.Text != "world" {
		t.Errorf("text from SDK: %+v", got)
	}
}

func TestFromSDKBlock_ToolUse(t *testing.T) {
	in := sdk.ContentBlockUnion{OfToolUse: &sdk.ToolUseBlock{
		ID: "u1", Name: "read_file", Input: json.RawMessage(`{"path":"x"}`),
	}}
	got := blockFromSDK(in)
	if got.Type != llm.BlockToolUse || got.ToolName != "read_file" {
		t.Errorf("tool_use from SDK: %+v", got)
	}
}

func TestRoundTrip(t *testing.T) {
	original := llm.Block{
		Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "read_file",
		ToolInput: json.RawMessage(`{"path":"main.go"}`),
	}
	out := blockFromSDK(blockToSDK(original))
	if out.ToolUseID != original.ToolUseID || out.ToolName != original.ToolName {
		t.Errorf("round-trip: in=%+v out=%+v", original, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/anthropic/ -v`
Expected: FAIL with "undefined: blockToSDK".

- [ ] **Step 3: Implement adapter**

NOTE: The exact SDK type names (`OfText`, `OfToolUse`, `OfToolResult`, `ContentBlockUnion`, etc.) come from `github.com/anthropics/anthropic-sdk-go`'s generated union types. If the SDK version yields different names, mirror them — the principle is "tagged union, one Of* field populated per block kind". After running `go get` in Task 5, browse `~/go/pkg/mod/github.com/anthropics/anthropic-sdk-go@v1.51.0/messages.go` to confirm the type shape, and adapt the code below.

`source/server/internal/llm/anthropic/adapter.go`:

```go
package anthropic

import (
	"encoding/json"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"cercano/source/server/internal/llm"
)

func blockToSDK(b llm.Block) sdk.ContentBlockParamUnion {
	switch b.Type {
	case llm.BlockText:
		return sdk.NewTextBlock(b.Text)
	case llm.BlockToolUse:
		return sdk.NewToolUseBlock(b.ToolUseID, b.ToolInput, b.ToolName)
	case llm.BlockToolResult:
		return sdk.NewToolResultBlock(b.ToolUseRef, b.Content, b.IsError)
	}
	return sdk.ContentBlockParamUnion{}
}

func blockFromSDK(in sdk.ContentBlockUnion) llm.Block {
	if in.OfText != nil {
		return llm.Block{Type: llm.BlockText, Text: in.OfText.Text}
	}
	if in.OfToolUse != nil {
		var raw json.RawMessage
		if in.OfToolUse.Input != nil {
			raw, _ = json.Marshal(in.OfToolUse.Input)
		}
		return llm.Block{
			Type:      llm.BlockToolUse,
			ToolUseID: in.OfToolUse.ID,
			ToolName:  in.OfToolUse.Name,
			ToolInput: raw,
		}
	}
	return llm.Block{}
}

func messagesToSDK(msgs []llm.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			blocks = append(blocks, blockToSDK(b))
		}
		switch m.Role {
		case llm.RoleUser:
			out = append(out, sdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
			out = append(out, sdk.NewAssistantMessage(blocks...))
		}
	}
	return out
}

func toolsToSDK(tools []llm.Tool) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema map[string]any
		_ = json.Unmarshal(t.Schema, &schema)
		out = append(out, sdk.ToolUnionParam{
			OfTool: &sdk.ToolParam{
				Name:        t.Name,
				Description: sdk.String(t.Description),
				InputSchema: sdk.ToolInputSchemaParam{
					Type:       "object",
					Properties: schema["properties"],
				},
			},
		})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/llm/anthropic/ -v`
Expected: PASS. If type names differ from SDK, refer to the SDK's `messages.go` and rename accordingly.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/anthropic/adapter.go source/server/internal/llm/anthropic/adapter_test.go
git commit -m "feat(llm/anthropic): Block <-> SDK ContentBlock adapter"
```

---

### Task 7: Anthropic client constructor + Capabilities

**Files:**
- Create: `source/server/internal/llm/anthropic/client.go`
- Create: `source/server/internal/llm/anthropic/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package anthropic

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_NewClient_SetsBaseURLAndUA(t *testing.T) {
	var seenURL string
	var seenUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.Path
		seenUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL: srv.URL,
		APIKey:  "dummy",
		Model:   "claude-opus-4-7",
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
	return ChatRequest{ /* populated in Task 8 — leave empty for now if needed */ }
}
```

(`ChatRequest` and `Chat` are filled in Task 8 — the test in this task only verifies constructor wiring; placeholder `simpleReq` and the body of `Chat` will be expanded then. To keep the test failing only for the right reason, make `Chat` return a stub error for now.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/anthropic/ -run TestClient_NewClient -v`
Expected: FAIL with "undefined: NewClient".

- [ ] **Step 3: Implement constructor + Capabilities**

`source/server/internal/llm/anthropic/client.go`:

```go
package anthropic

import (
	"net/http"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	UserAgent string
}

type Client struct {
	cfg Config
	sdk *sdk.Client
}

type uaRoundTripper struct {
	base http.RoundTripper
	ua   string
}

func (u *uaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if u.ua != "" {
		r.Header.Set("User-Agent", u.ua)
	}
	return u.base.RoundTrip(r)
}

func NewClient(cfg Config) *Client {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "dummy"
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, option.WithHTTPClient(&http.Client{
			Transport: &uaRoundTripper{base: http.DefaultTransport, ua: cfg.UserAgent},
		}))
	}
	c := sdk.NewClient(opts...)
	return &Client{cfg: cfg, sdk: &c}
}

func (c *Client) Name() string { return "anthropic" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       true,
		SupportsVision:        true,
		MaxToolsPerCall:       0,
	}
}
```

- [ ] **Step 4: Stub Chat so the test compiles**

Add to `client.go`:

```go
import "context"

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Implemented in Task 8.
	_ = req
	return ChatResponse{}, nil
}
```

Update test's `simpleReq()` to return an empty `ChatRequest{Model: "claude-opus-4-7", MaxTokens: 10}`.

- [ ] **Step 5: Run test**

Run: `cd source/server && go test ./internal/llm/anthropic/ -v`
Expected: TestClient_NewClient_SetsBaseURLAndUA may still fail because Chat is stubbed and doesn't actually hit the server. That's OK at this point. Verify it compiles and runs.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/llm/anthropic/client.go source/server/internal/llm/anthropic/client_test.go
git commit -m "feat(llm/anthropic): client constructor with WithBaseURL + UA RoundTripper"
```

---

### Task 8: Anthropic Chat (non-streaming)

**Files:**
- Modify: `source/server/internal/llm/anthropic/client.go`

- [ ] **Step 1: Implement Chat against the SDK**

Replace the stub:

```go
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messagesToSDK(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		params.Tools = toolsToSDK(req.Tools)
	}
	if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}

	resp, err := c.sdk.Messages.New(ctx, params)
	if err != nil {
		return ChatResponse{}, err
	}
	out := ChatResponse{
		StopReason:   string(resp.StopReason),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}
	for _, b := range resp.Content {
		out.Blocks = append(out.Blocks, blockFromSDK(b))
	}
	return out, nil
}
```

- [ ] **Step 2: Run the constructor test (which now actually hits httptest server)**

Run: `cd source/server && go test ./internal/llm/anthropic/ -run TestClient_NewClient -v`
Expected: PASS. The test fixture returns a stub Anthropic JSON response.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/llm/anthropic/client.go
git commit -m "feat(llm/anthropic): Chat (non-streaming) via SDK"
```

---

### Task 9: Anthropic StreamChat (SSE → StreamEvent)

**Files:**
- Modify: `source/server/internal/llm/anthropic/client.go`
- Create: `source/server/internal/llm/anthropic/stream.go`
- Create: `source/server/internal/llm/anthropic/stream_test.go`

- [ ] **Step 1: Write the failing test**

`source/server/internal/llm/anthropic/stream_test.go`:

```go
package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

const sseFixture = `event: message_start
data: {"type":"message_start","message":{"id":"m_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"u1","name":"read_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"x\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`

func TestStreamChat_EmitsExpectedEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseFixture))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()

	got := []llm.StreamEventType{}
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, ev.Type)
	}
	want := []llm.StreamEventType{
		llm.EventMessageStart,
		llm.EventTextDelta,
		llm.EventToolUseStart,
		llm.EventToolUseInputDelta,
		llm.EventToolUseStop,
		llm.EventMessageStop,
	}
	if len(got) < len(want) {
		t.Fatalf("got %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("event %d: got %s want %s", i, got[i], w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/llm/anthropic/ -run TestStreamChat -v`
Expected: FAIL with "undefined: StreamChat" / "method not on Client".

- [ ] **Step 3: Implement StreamChat**

`source/server/internal/llm/anthropic/stream.go`:

```go
package anthropic

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	sdkpkg "github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"cercano/source/server/internal/llm"
)

type streamReader struct {
	stream *sdkpkg.Stream[sdk.MessageStreamEventUnion]
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	if !s.stream.Next() {
		if err := s.stream.Err(); err != nil {
			return llm.StreamEvent{}, false, err
		}
		return llm.StreamEvent{}, false, nil
	}
	raw := s.stream.Current()
	return convertEvent(raw), true, nil
}

func (s *streamReader) Close() error { return s.stream.Close() }

func convertEvent(raw sdk.MessageStreamEventUnion) llm.StreamEvent {
	switch v := raw.AsAny().(type) {
	case sdk.MessageStartEvent:
		return llm.StreamEvent{Type: llm.EventMessageStart}
	case sdk.ContentBlockStartEvent:
		if v.ContentBlock.Type == "tool_use" {
			return llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: v.ContentBlock.ID,
				ToolName:  v.ContentBlock.Name,
			}
		}
		return llm.StreamEvent{Type: llm.EventMessageStart}
	case sdk.ContentBlockDeltaEvent:
		switch v.Delta.Type {
		case "text_delta":
			return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: v.Delta.Text}
		case "input_json_delta":
			return llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: v.Delta.PartialJSON}
		}
	case sdk.ContentBlockStopEvent:
		return llm.StreamEvent{Type: llm.EventToolUseStop}
	case sdk.MessageDeltaEvent:
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: string(v.Delta.StopReason)}
	case sdk.MessageStopEvent:
		return llm.StreamEvent{Type: llm.EventMessageStop}
	}
	return llm.StreamEvent{Type: llm.EventError, ErrText: "unknown event"}
}
```

Add to `client.go`:

```go
func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (llm.StreamReader, error) {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messagesToSDK(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		params.Tools = toolsToSDK(req.Tools)
	}
	st := c.sdk.Messages.NewStreaming(ctx, params)
	return &streamReader{stream: st}, nil
}
```

NOTE on SDK types: the names `MessageStreamEventUnion`, `MessageStartEvent`, etc. are best-effort guesses based on common SDK patterns. After Task 5, browse the SDK source at `~/go/pkg/mod/github.com/anthropics/anthropic-sdk-go@v1.51.0/` and confirm the exact union type and case names. Adapt the switch cases to match.

- [ ] **Step 4: Run the test**

Run: `cd source/server && go test ./internal/llm/anthropic/ -run TestStreamChat -v`
Expected: PASS. If the SDK's SSE stream type can't accept an httptest server (because it expects the real Anthropic endpoint), the test may need to use the SDK's `ssestream.NewStream` constructor with a custom reader instead. Adapt to whatever the SDK exposes.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/anthropic/stream.go source/server/internal/llm/anthropic/stream_test.go source/server/internal/llm/anthropic/client.go
git commit -m "feat(llm/anthropic): StreamChat with SSE -> StreamEvent translation"
```

---

## Phase 3 — Ollama Adapter

### Task 10: Add ollama/api dependency + Ollama adapter

**Files:**
- Modify: `source/server/go.mod`
- Create: `source/server/internal/llm/ollama/client.go`
- Create: `source/server/internal/llm/ollama/adapter.go`
- Create: `source/server/internal/llm/ollama/adapter_test.go`

- [ ] **Step 1: Add dependency**

```bash
cd source/server && go get github.com/ollama/ollama/api@latest && go mod tidy
```

- [ ] **Step 2: Write the failing test**

```go
package ollama

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBlockToOllama_Text(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role:   llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
	})
	if msg.Role != "user" || msg.Content != "hi" {
		t.Errorf("ollama msg: %+v", msg)
	}
}

func TestBlockToOllama_ToolUse_InAssistant(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role: llm.RoleAssistant,
		Blocks: []llm.Block{{
			Type: llm.BlockToolUse, ToolUseID: "u1",
			ToolName: "read_file", ToolInput: json.RawMessage(`{"path":"x"}`),
		}},
	})
	if msg.Role != "assistant" || len(msg.ToolCalls) != 1 {
		t.Errorf("expected one tool_call, got %+v", msg)
	}
	if msg.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("function name: %+v", msg.ToolCalls[0])
	}
}

func TestBlockToOllama_ToolResult_InUser(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role: llm.RoleUser,
		Blocks: []llm.Block{{
			Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "32 lines",
		}},
	})
	if msg.Role != "tool" || msg.Content != "32 lines" {
		t.Errorf("tool result in user msg: %+v", msg)
	}
}
```

- [ ] **Step 3: Implement adapter**

`source/server/internal/llm/ollama/adapter.go`:

```go
package ollama

import (
	"encoding/json"

	api "github.com/ollama/ollama/api"
	"cercano/source/server/internal/llm"
)

func messageToOllama(m llm.Message) api.Message {
	out := api.Message{}
	switch m.Role {
	case llm.RoleAssistant:
		out.Role = "assistant"
	case llm.RoleSystem:
		out.Role = "system"
	default:
		out.Role = "user"
	}
	var text string
	for _, b := range m.Blocks {
		switch b.Type {
		case llm.BlockText:
			text += b.Text
		case llm.BlockToolUse:
			var args api.ToolCallFunctionArguments
			_ = json.Unmarshal(b.ToolInput, &args)
			out.ToolCalls = append(out.ToolCalls, api.ToolCall{
				Function: api.ToolCallFunction{Name: b.ToolName, Arguments: args},
			})
		case llm.BlockToolResult:
			// Ollama wants tool results in a separate message with role=tool.
			return api.Message{Role: "tool", Content: b.Content}
		}
	}
	out.Content = text
	return out
}

func toolsToOllama(tools []llm.Tool) []api.Tool {
	out := make([]api.Tool, 0, len(tools))
	for _, t := range tools {
		var params api.ToolFunction
		params.Name = t.Name
		params.Description = t.Description
		var schema map[string]any
		_ = json.Unmarshal(t.Schema, &schema)
		params.Parameters = api.ToolFunctionParameters{Type: "object"}
		// best-effort: cram schema properties through; concrete shape depends on api types
		out = append(out, api.Tool{Type: "function", Function: params})
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `cd source/server && go test ./internal/llm/ollama/ -v`
Expected: PASS (with adjustments to match actual `api.Message` / `api.ToolCall` struct shape — browse `~/go/pkg/mod/github.com/ollama/ollama/api@<version>/` to confirm exact field names).

- [ ] **Step 5: Commit**

```bash
git add source/server/go.mod source/server/go.sum source/server/internal/llm/ollama/
git commit -m "feat(llm/ollama): Block <-> ollama Message adapter"
```

---

### Task 11: Ollama client + Capabilities + Chat

**Files:**
- Create: `source/server/internal/llm/ollama/client.go`
- Create: `source/server/internal/llm/ollama/client_test.go`

- [ ] **Step 1: Write failing test**

```go
package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Chat_BasicResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "qwen3-coder",
			"created_at": "2026-01-01T00:00:00Z",
			"message": map[string]any{
				"role":    "assistant",
				"content": "hello",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "qwen3-coder"})
	if !c.Capabilities().SupportsTools {
		t.Errorf("expected tool support")
	}
	resp, err := c.Chat(t.Context(), ChatRequest{Model: "qwen3-coder", MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) == 0 || resp.Blocks[0].Text != "hello" {
		t.Errorf("response blocks: %+v", resp.Blocks)
	}
}
```

- [ ] **Step 2: Implement client**

`source/server/internal/llm/ollama/client.go`:

```go
package ollama

import (
	"context"
	"net/http"
	"net/url"

	api "github.com/ollama/ollama/api"
	"cercano/source/server/internal/llm"
)

type Config struct {
	BaseURL string
	Model   string
}

type Client struct {
	cfg Config
	api *api.Client
}

type ChatRequest = llm.ChatRequest
type ChatResponse = llm.ChatResponse

func NewClient(cfg Config) *Client {
	u, _ := url.Parse(cfg.BaseURL)
	cli := api.NewClient(u, http.DefaultClient)
	return &Client{cfg: cfg, api: cli}
}

func (c *Client) Name() string { return "ollama" }

func (c *Client) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: false,
		SupportsCaching:       false,
		SupportsVision:        false,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	msgs := make([]api.Message, 0, len(req.Messages))
	if req.System != "" {
		msgs = append(msgs, api.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, messageToOllama(m))
	}
	freq := &api.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   boolPtr(false),
		Tools:    toolsToOllama(req.Tools),
	}
	var got *api.ChatResponse
	err := c.api.Chat(ctx, freq, func(r api.ChatResponse) error {
		got = &r
		return nil
	})
	if err != nil {
		return ChatResponse{}, err
	}
	out := ChatResponse{StopReason: "end_turn"}
	if got.Message.Content != "" {
		out.Blocks = append(out.Blocks, llm.Block{Type: llm.BlockText, Text: got.Message.Content})
	}
	for _, tc := range got.Message.ToolCalls {
		raw, _ := json.Marshal(tc.Function.Arguments)
		out.Blocks = append(out.Blocks, llm.Block{
			Type: llm.BlockToolUse, ToolName: tc.Function.Name, ToolInput: raw,
		})
	}
	return out, nil
}

func boolPtr(b bool) *bool { return &b }
```

(Add `import "encoding/json"` at top.)

- [ ] **Step 3: Run test**

Run: `cd source/server && go test ./internal/llm/ollama/ -v`
Expected: PASS, possibly after adjusting struct field names to match the actual `api` package version installed.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/llm/ollama/client.go source/server/internal/llm/ollama/client_test.go
git commit -m "feat(llm/ollama): client + Chat (non-streaming)"
```

---

### Task 12: Ollama StreamChat

**Files:**
- Create: `source/server/internal/llm/ollama/stream.go`
- Create: `source/server/internal/llm/ollama/stream_test.go`

- [ ] **Step 1: Write failing test**

```go
package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestStreamChat_EmitsTextAndToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]any{
			"model": "qwen3-coder",
			"message": map[string]any{"role": "assistant", "content": "Hi"},
			"done": false,
		})
		_ = enc.Encode(map[string]any{
			"model": "qwen3-coder",
			"message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []map[string]any{{
					"function": map[string]any{"name": "read_file", "arguments": map[string]any{"path": "x"}},
				}},
			},
			"done": true,
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "qwen3-coder"})
	rdr, err := c.StreamChat(t.Context(), ChatRequest{Model: "qwen3-coder", MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()

	gotText := false
	gotTool := false
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta && ev.TextDelta == "Hi" {
			gotText = true
		}
		if ev.Type == llm.EventToolUseStart && ev.ToolName == "read_file" {
			gotTool = true
		}
	}
	if !gotText || !gotTool {
		t.Errorf("missing events: text=%v tool=%v", gotText, gotTool)
	}
}
```

- [ ] **Step 2: Implement stream**

`source/server/internal/llm/ollama/stream.go`:

```go
package ollama

import (
	"context"
	"encoding/json"

	api "github.com/ollama/ollama/api"
	"cercano/source/server/internal/llm"
)

type streamReader struct {
	ch     chan llm.StreamEvent
	cancel context.CancelFunc
	err    error
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok := <-s.ch
	if !ok {
		return llm.StreamEvent{}, false, s.err
	}
	return ev, true, nil
}

func (s *streamReader) Close() error {
	s.cancel()
	return nil
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (llm.StreamReader, error) {
	cctx, cancel := context.WithCancel(ctx)
	ch := make(chan llm.StreamEvent, 16)
	r := &streamReader{ch: ch, cancel: cancel}

	msgs := []api.Message{}
	if req.System != "" {
		msgs = append(msgs, api.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, messageToOllama(m))
	}
	freq := &api.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   boolPtr(true),
		Tools:    toolsToOllama(req.Tools),
	}

	go func() {
		defer close(ch)
		err := c.api.Chat(cctx, freq, func(resp api.ChatResponse) error {
			if resp.Message.Content != "" {
				ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: resp.Message.Content}
			}
			for _, tc := range resp.Message.ToolCalls {
				raw, _ := json.Marshal(tc.Function.Arguments)
				ch <- llm.StreamEvent{Type: llm.EventToolUseStart, ToolName: tc.Function.Name, ToolInputRaw: raw}
				ch <- llm.StreamEvent{Type: llm.EventToolUseStop}
			}
			if resp.Done {
				ch <- llm.StreamEvent{Type: llm.EventMessageStop, StopReason: resp.DoneReason}
			}
			return nil
		})
		r.err = err
	}()
	return r, nil
}
```

- [ ] **Step 3: Run test**

Run: `cd source/server && go test ./internal/llm/ollama/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/llm/ollama/stream.go source/server/internal/llm/ollama/stream_test.go
git commit -m "feat(llm/ollama): StreamChat with NDJSON -> StreamEvent"
```

---

## Phase 4 — Persistence

### Task 13: Add content_json column + Store API extension

**Files:**
- Modify: `source/server/internal/conversation/schema.sql`
- Modify: `source/server/internal/conversation/store.go`
- Modify: `source/server/internal/conversation/store_test.go`

- [ ] **Step 1: Write failing test**

Add to `store_test.go`:

```go
func TestStore_AppendWithBlocksJSON_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	convID := "c1"
	_ = s.EnsureConversation(context.Background(), convID, "", "/tmp", "claude", time.Now().Unix())

	blocks := `[{"type":"tool_use","id":"u1","name":"read_file","input":{"path":"main.go"}}]`
	err := s.Append(context.Background(), Turn{
		ID: "t1", ConversationID: convID, Role: "assistant",
		Content: "", BlocksJSON: blocks, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	turns, err := s.GetTurns(context.Background(), convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].BlocksJSON != blocks {
		t.Errorf("blocks not round-tripped: %+v", turns)
	}
}
```

- [ ] **Step 2: Update schema.sql**

```sql
ALTER TABLE turns ADD COLUMN content_json TEXT NOT NULL DEFAULT '';
```

(Append this at the bottom of `schema.sql`. Pure-Go sqlite tolerates `ALTER TABLE ADD COLUMN`. On first start the migration runs once.)

- [ ] **Step 3: Add BlocksJSON to Turn struct in store.go**

Add field to the `Turn` struct:

```go
type Turn struct {
	ID, ConversationID, Role, Content string
	BlocksJSON                        string
	TokensIn, TokensOut, LatencyMs    int
	CreatedAt                         int64
}
```

Update INSERT and SELECT statements in store.go to include `content_json`. The `Append` method's insert becomes:

```sql
INSERT INTO turns (id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
```

The `GetTurns` SELECT becomes:

```sql
SELECT id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at
FROM turns WHERE conversation_id = ? ORDER BY created_at
```

- [ ] **Step 4: Run test**

Run: `cd source/server && go test ./internal/conversation/ -v`
Expected: PASS. Existing text-only tests still pass since `BlocksJSON` defaults to empty.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/conversation/
git commit -m "feat(conversation): add content_json column for tool-call blocks"
```

---

## Phase 5 — Permission Modes

### Task 14: PermissionMode + GateDecision

**Files:**
- Create: `source/server/internal/agent/permissions.go`
- Create: `source/server/internal/agent/permissions_test.go`

- [ ] **Step 1: Write failing test**

```go
package agent

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestGateDecision_Strict_AllConfirm(t *testing.T) {
	cases := []struct {
		tier llm.Permission
		want bool
	}{
		{llm.PermR, false},
		{llm.PermW, true},
		{llm.PermX, true},
	}
	for _, c := range cases {
		got := GateDecision(ModeStrict, c.tier)
		if got != c.want {
			t.Errorf("Strict %s: got %v want %v", c.tier, got, c.want)
		}
	}
}

func TestGateDecision_Permissive(t *testing.T) {
	cases := []struct {
		tier llm.Permission
		want bool
	}{
		{llm.PermR, false},
		{llm.PermW, false},
		{llm.PermX, true},
	}
	for _, c := range cases {
		got := GateDecision(ModePermissive, c.tier)
		if got != c.want {
			t.Errorf("Permissive %s: got %v want %v", c.tier, got, c.want)
		}
	}
}

func TestGateDecision_Bypass_NoConfirm(t *testing.T) {
	for _, tier := range []llm.Permission{llm.PermR, llm.PermW, llm.PermX} {
		if GateDecision(ModeBypass, tier) {
			t.Errorf("Bypass %s: should not require confirm", tier)
		}
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]PermissionMode{
		"strict":     ModeStrict,
		"permissive": ModePermissive,
		"bypass":     ModeBypass,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q): %v %v", in, got, err)
		}
	}
	if _, err := ParseMode("garbage"); err == nil {
		t.Errorf("expected error for garbage mode")
	}
}
```

- [ ] **Step 2: Implement**

`source/server/internal/agent/permissions.go`:

```go
package agent

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

type PermissionMode string

const (
	ModeStrict     PermissionMode = "strict"
	ModePermissive PermissionMode = "permissive"
	ModeBypass     PermissionMode = "bypass"
)

func ParseMode(s string) (PermissionMode, error) {
	switch PermissionMode(s) {
	case ModeStrict, ModePermissive, ModeBypass:
		return PermissionMode(s), nil
	}
	return "", fmt.Errorf("unknown permission mode: %q (want strict|permissive|bypass)", s)
}

// GateDecision returns true when a tool call at the given tier requires
// human confirmation under the given mode.
func GateDecision(mode PermissionMode, tier llm.Permission) bool {
	if tier == llm.PermR {
		return false
	}
	switch mode {
	case ModeStrict:
		return true
	case ModePermissive:
		return tier == llm.PermX
	case ModeBypass:
		return false
	}
	return true
}
```

- [ ] **Step 3: Run test**

Run: `cd source/server && go test ./internal/agent/ -run TestGate -v && go test ./internal/agent/ -run TestParseMode -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agent/permissions.go source/server/internal/agent/permissions_test.go
git commit -m "feat(agent): PermissionMode + GateDecision"
```

---

### Task 15: PermissionStore — load/save mode in permissions.yaml

**Files:**
- Modify: `source/server/internal/agent/permissions.go`
- Modify: `source/server/internal/agent/permissions_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestPermissionStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	s, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModePermissive {
		t.Errorf("default mode should be permissive, got %s", s.Mode())
	}
	if err := s.SetMode(ModeStrict); err != nil {
		t.Fatal(err)
	}
	// reload
	s2, _ := LoadPermissionStore(path)
	if s2.Mode() != ModeStrict {
		t.Errorf("mode did not persist: %s", s2.Mode())
	}
}
```

- [ ] **Step 2: Implement PermissionStore**

Add to `permissions.go`:

```go
import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type PermissionStore struct {
	mu   sync.Mutex
	path string
	mode PermissionMode
}

type permsFile struct {
	Mode string `yaml:"mode"`
}

func LoadPermissionStore(path string) (*PermissionStore, error) {
	s := &PermissionStore{path: path, mode: ModePermissive}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f permsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Mode != "" {
		m, err := ParseMode(f.Mode)
		if err == nil {
			s.mode = m
		}
	}
	return s, nil
}

func (s *PermissionStore) Mode() PermissionMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *PermissionStore) SetMode(m PermissionMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	data, _ := yaml.Marshal(permsFile{Mode: string(m)})
	return os.WriteFile(s.path, data, 0o644)
}
```

- [ ] **Step 3: Verify yaml.v3 is in go.mod**

If missing:
```bash
cd source/server && go get gopkg.in/yaml.v3 && go mod tidy
```

- [ ] **Step 4: Run test**

Run: `cd source/server && go test ./internal/agent/ -run TestPermissionStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/permissions.go source/server/internal/agent/permissions_test.go source/server/go.mod source/server/go.sum
git commit -m "feat(agent): PermissionStore with permissions.yaml persistence"
```

---

## Phase 6 — Tool Catalog Helper

### Task 16: BuildToolCatalog from agenttools.Registry

**Files:**
- Create: `source/server/internal/agenttools/catalog.go`
- Create: `source/server/internal/agenttools/catalog_test.go`

- [ ] **Step 1: Write failing test**

```go
package agenttools

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBuildToolCatalog_CoversAllRegistered(t *testing.T) {
	reg := DefaultRegistry()
	cat := BuildToolCatalog(reg)
	if len(cat) != len(reg.All()) {
		t.Errorf("catalog len %d != registry len %d", len(cat), len(reg.All()))
	}
	for _, tl := range cat {
		if tl.Name == "" || tl.Description == "" || len(tl.Schema) == 0 {
			t.Errorf("incomplete catalog entry: %+v", tl)
		}
		switch tl.Permission {
		case llm.PermR, llm.PermW, llm.PermX:
		default:
			t.Errorf("invalid permission tier: %+v", tl)
		}
	}
}

func TestBuildToolCatalog_PreservesPermissionTier(t *testing.T) {
	reg := DefaultRegistry()
	cat := BuildToolCatalog(reg)
	byName := map[string]llm.Tool{}
	for _, tl := range cat {
		byName[tl.Name] = tl
	}
	// rm_file is X-tier
	if byName["rm_file"].Permission != llm.PermX {
		t.Errorf("rm_file should be X, got %v", byName["rm_file"].Permission)
	}
	// read_file is R-tier
	if byName["read_file"].Permission != llm.PermR {
		t.Errorf("read_file should be R, got %v", byName["read_file"].Permission)
	}
}
```

- [ ] **Step 2: Implement**

`source/server/internal/agenttools/catalog.go`:

```go
package agenttools

import (
	"encoding/json"

	"cercano/source/server/internal/llm"
)

func BuildToolCatalog(reg *Registry) []llm.Tool {
	src := reg.All()
	out := make([]llm.Tool, 0, len(src))
	for _, t := range src {
		out = append(out, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      json.RawMessage(t.Schema()),
			Permission:  permissionToLLM(t.Permission()),
		})
	}
	return out
}

func permissionToLLM(p Permission) llm.Permission {
	switch p {
	case PermR:
		return llm.PermR
	case PermW:
		return llm.PermW
	case PermX:
		return llm.PermX
	}
	return llm.PermR
}
```

- [ ] **Step 3: Run test**

Run: `cd source/server && go test ./internal/agenttools/ -run TestBuildToolCatalog -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agenttools/catalog.go source/server/internal/agenttools/catalog_test.go
git commit -m "feat(agenttools): BuildToolCatalog(registry) -> []llm.Tool"
```

---

## Phase 7 — Tool Loop

### Task 17: Tool loop skeleton with mockProvider — happy path

**Files:**
- Create: `source/server/internal/agent/toolloop.go`
- Create: `source/server/internal/agent/toolloop_test.go`

- [ ] **Step 1: Write failing test**

```go
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// mockProvider returns scripted responses for testing the loop.
type mockProvider struct {
	scripts [][]llm.Block // one entry per iteration
	caps    llm.Capabilities
	calls   int
}

func (m *mockProvider) Name() string                  { return "mock" }
func (m *mockProvider) Capabilities() llm.Capabilities { return m.caps }
func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	out := llm.ChatResponse{Blocks: m.scripts[m.calls]}
	m.calls++
	return out, nil
}
func (m *mockProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil // not used by these tests
}

func TestToolLoop_PlainText_TerminatesImmediately(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockText, Text: "Done."},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		ConvHistory: nil, UserInput: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("plain text turn should make 1 call, made %d", prov.calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}

func TestToolLoop_SingleToolCall_FeedsResultAndContinues(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockText, Text: "Reading..."},
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "Got it."}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Errorf("expected 2 calls (tool + continuation), got %d", prov.calls)
	}
	if result.FinalText != "Got it." {
		t.Errorf("final: %q", result.FinalText)
	}
}
```

- [ ] **Step 2: Implement loop skeleton**

`source/server/internal/agent/toolloop.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

type ToolLoopInput struct {
	Provider    llm.Provider
	Registry    *agenttools.Registry
	Permissions *PermissionStore
	ConvHistory []llm.Message
	UserInput   string
	Model       string
	System      string

	// PermissionRequester is the callback the loop uses to surface a
	// confirm prompt to the active client (nil = auto-allow, useful in tests).
	PermissionRequester func(ctx context.Context, name string, args json.RawMessage, tier llm.Permission) (allow bool, err error)
}

type ToolLoopResult struct {
	FinalText   string
	FinalBlocks []llm.Block
	Iterations  int
	History     []llm.Message
}

const MaxToolLoopIterations = 10

func RunToolLoop(ctx context.Context, in ToolLoopInput) (ToolLoopResult, error) {
	if !in.Provider.Capabilities().SupportsTools {
		return ToolLoopResult{}, fmt.Errorf("provider %s does not support tools", in.Provider.Name())
	}

	hist := append([]llm.Message{}, in.ConvHistory...)
	hist = append(hist, llm.Message{
		Role:   llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: in.UserInput}},
	})

	catalog := agenttools.BuildToolCatalog(in.Registry)
	mode := ModePermissive
	if in.Permissions != nil {
		mode = in.Permissions.Mode()
	}
	consecutiveErrors := 0

	for iter := 0; iter < MaxToolLoopIterations; iter++ {
		req := llm.ChatRequest{
			Model:    in.Model,
			System:   in.System,
			Messages: hist,
			Tools:    catalog,
			MaxTokens: 4096,
		}
		resp, err := in.Provider.Chat(ctx, req)
		if err != nil {
			return ToolLoopResult{}, err
		}
		hist = append(hist, llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})

		// Find tool_use blocks.
		var toolCalls []llm.Block
		var finalText string
		for _, b := range resp.Blocks {
			if b.Type == llm.BlockToolUse {
				toolCalls = append(toolCalls, b)
			}
			if b.Type == llm.BlockText {
				finalText += b.Text
			}
		}
		if len(toolCalls) == 0 {
			return ToolLoopResult{
				FinalText: finalText, FinalBlocks: resp.Blocks,
				Iterations: iter + 1, History: hist,
			}, nil
		}

		// Execute. (Concurrency + perm gating refined in Tasks 18-19.)
		results := make([]llm.Block, 0, len(toolCalls))
		allErrored := true
		for _, tc := range toolCalls {
			tool, ok := in.Registry.Get(tc.ToolName)
			if !ok {
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: "unknown tool: " + tc.ToolName, IsError: true,
				})
				continue
			}
			tier := permissionToLLM(tool.Permission())
			_ = mode // perm gating wired in Task 19
			res, ierr := tool.Execute(ctx, tc.ToolInput)
			if ierr != nil {
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: ierr.Error(), IsError: true,
				})
				_ = tier
				continue
			}
			allErrored = false
			results = append(results, llm.Block{
				Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
				Content: res.Text, IsError: false,
			})
		}
		hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})

		if allErrored {
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				return ToolLoopResult{
					FinalText: finalText, Iterations: iter + 1, History: hist,
				}, fmt.Errorf("aborted: 3 consecutive iterations of tool errors")
			}
		} else {
			consecutiveErrors = 0
		}
	}
	return ToolLoopResult{Iterations: MaxToolLoopIterations, History: hist},
		fmt.Errorf("hit max tool loop iterations (%d)", MaxToolLoopIterations)
}

func permissionToLLM(p agenttools.Permission) llm.Permission {
	switch p {
	case agenttools.PermR:
		return llm.PermR
	case agenttools.PermW:
		return llm.PermW
	case agenttools.PermX:
		return llm.PermX
	}
	return llm.PermR
}
```

- [ ] **Step 3: Run tests**

Run: `cd source/server && go test ./internal/agent/ -run TestToolLoop -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go
git commit -m "feat(agent): tool loop skeleton — plain text + single tool call paths"
```

---

### Task 18: Concurrent R-tier dispatch, serialized W/X

**Files:**
- Modify: `source/server/internal/agent/toolloop.go`
- Modify: `source/server/internal/agent/toolloop_test.go`

- [ ] **Step 1: Write failing test**

Add to `toolloop_test.go`:

```go
func TestToolLoop_RTierRunsConcurrently(t *testing.T) {
	// Two R-tier calls in one assistant turn should both complete.
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
				{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true, SupportsParallelTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" {
		t.Errorf("final: %q", result.FinalText)
	}

	// Both results should be in history.
	var found1, found2 bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" { found1 = true }
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u2" { found2 = true }
		}
	}
	if !found1 || !found2 {
		t.Errorf("missing tool results: u1=%v u2=%v", found1, found2)
	}
}
```

- [ ] **Step 2: Implement partitioned execution**

Update the execution loop in `toolloop.go`:

```go
// Partition by tier.
type pendingCall struct {
	block llm.Block
	tool  agenttools.Tool
	tier  llm.Permission
}
var rCalls, wxCalls []pendingCall
for _, tc := range toolCalls {
	tool, ok := in.Registry.Get(tc.ToolName)
	if !ok {
		results = append(results, llm.Block{
			Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
			Content: "unknown tool: " + tc.ToolName, IsError: true,
		})
		continue
	}
	tier := permissionToLLM(tool.Permission())
	pc := pendingCall{block: tc, tool: tool, tier: tier}
	if tier == llm.PermR {
		rCalls = append(rCalls, pc)
	} else {
		wxCalls = append(wxCalls, pc)
	}
}

// R-tier: concurrent.
type rr struct {
	idx int
	res llm.Block
}
rChan := make(chan rr, len(rCalls))
for i, pc := range rCalls {
	go func(i int, pc pendingCall) {
		res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
		out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
		if err != nil {
			out.Content = err.Error()
			out.IsError = true
		} else {
			out.Content = res.Text
		}
		rChan <- rr{idx: i, res: out}
	}(i, pc)
}
rResults := make([]llm.Block, len(rCalls))
for range rCalls {
	r := <-rChan
	rResults[r.idx] = r.res
}
results = append(results, rResults...)

// W/X-tier: serial. (Perm gating wired in Task 19.)
for _, pc := range wxCalls {
	res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
	if err != nil {
		out.Content = err.Error()
		out.IsError = true
	} else {
		out.Content = res.Text
	}
	results = append(results, out)
}

// Re-evaluate allErrored across all results
allErrored = true
for _, r := range results {
	if !r.IsError {
		allErrored = false
		break
	}
}
```

(Replace the old execution loop with this. `allErrored` is computed from the combined results.)

- [ ] **Step 3: Run test**

Run: `cd source/server && go test ./internal/agent/ -run TestToolLoop -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go
git commit -m "feat(agent): partition R/W/X — R-tier concurrent, W/X serial"
```

---

### Task 19: Permission gate via PermissionRequester callback

**Files:**
- Modify: `source/server/internal/agent/toolloop.go`
- Modify: `source/server/internal/agent/toolloop_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestToolLoop_UserDeniesWTier_TerminatesTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "write_file",
				ToolInput: json.RawMessage(`{"path":"/tmp/x","content":"x"}`)},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	dir := t.TempDir()
	perms, _ := LoadPermissionStore(dir + "/perms.yaml")
	_ = perms.SetMode(ModeStrict) // force W to require confirm

	requester := func(ctx context.Context, name string, args json.RawMessage, tier llm.Permission) (bool, error) {
		return false, nil // deny
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester, UserInput: "write x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("denial should NOT cause another loop iteration; calls=%d", prov.calls)
	}
	// Last user message should carry an error tool_result.
	last := result.History[len(result.History)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) == 0 || !last.Blocks[0].IsError {
		t.Errorf("expected error tool_result, got %+v", last)
	}
}
```

- [ ] **Step 2: Implement gate in the W/X loop**

Replace the W/X execution loop:

```go
for _, pc := range wxCalls {
	if GateDecision(mode, pc.tier) {
		if in.PermissionRequester == nil {
			// no requester == auto-deny in test mode
			results = append(results, llm.Block{
				Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
				Content: "no permission requester wired", IsError: true,
			})
			// hard turn end
			hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})
			return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist}, nil
		}
		allow, err := in.PermissionRequester(ctx, pc.block.ToolName, pc.block.ToolInput, pc.tier)
		if err != nil {
			return ToolLoopResult{}, err
		}
		if !allow {
			results = append(results, llm.Block{
				Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
				Content: "user denied execution", IsError: true,
			})
			hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})
			return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist}, nil
		}
	}
	res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
	if err != nil {
		out.Content = err.Error()
		out.IsError = true
	} else {
		out.Content = res.Text
	}
	results = append(results, out)
}
```

- [ ] **Step 3: Run tests**

Run: `cd source/server && go test ./internal/agent/ -run TestToolLoop -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go
git commit -m "feat(agent): permission gate via PermissionRequester, denial = hard turn-end"
```

---

### Task 20: Test the 3-strike + iteration cap guards

**Files:**
- Modify: `source/server/internal/agent/toolloop_test.go`

- [ ] **Step 1: Write tests**

```go
// register a single failing tool for cleaner scripted scenarios
type alwaysFailTool struct{}

func (alwaysFailTool) Name() string        { return "always_fail" }
func (alwaysFailTool) Description() string { return "always fails" }
func (alwaysFailTool) Permission() agenttools.Permission { return agenttools.PermR }
func (alwaysFailTool) Schema() string      { return `{"type":"object"}` }
func (alwaysFailTool) Execute(ctx context.Context, args json.RawMessage) (agenttools.Result, error) {
	return agenttools.Result{}, fmt.Errorf("always-fail")
}

func TestToolLoop_3StrikeErrorGuard(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u3", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.NewRegistry()
	reg.MustRegister(alwaysFailTool{})
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "3 consecutive") {
		t.Errorf("expected 3-strike abort, got %v", err)
	}
	if prov.calls != 3 {
		t.Errorf("expected exactly 3 calls before abort, got %d", prov.calls)
	}
}

func TestToolLoop_IterationCap(t *testing.T) {
	// 11 iterations of "call tool again" — should abort at 10.
	scripts := make([][]llm.Block, 11)
	for i := range scripts {
		scripts[i] = []llm.Block{{
			Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("u%d", i),
			ToolName: "list_dir", ToolInput: json.RawMessage(`{"path":"."}`),
		}}
	}
	prov := &mockProvider{scripts: scripts, caps: llm.Capabilities{SupportsTools: true}}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "max tool loop iterations") {
		t.Errorf("expected iteration cap abort, got %v", err)
	}
	if prov.calls != 10 {
		t.Errorf("expected exactly 10 calls before cap, got %d", prov.calls)
	}
}
```

Add `import "fmt"` and `"strings"` to the test file.

- [ ] **Step 2: Run tests**

Run: `cd source/server && go test ./internal/agent/ -run TestToolLoop -v`
Expected: PASS. If the existing implementation correctly tracks `consecutiveErrors` and bails at iter == MaxToolLoopIterations, these pass with no code changes.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/agent/toolloop_test.go
git commit -m "test(agent): coverage for 3-strike guard and iteration cap"
```

---

## Phase 8 — gRPC Surface

### Task 21: Proto changes — new RPCs + streaming events + content_json

**Files:**
- Modify: `source/proto/agent.proto`

- [ ] **Step 1: Add to the Agent service**

Inside `service Agent { ... }`:

```proto
  // SetPermissionMode changes the agent's session permission mode.
  rpc SetPermissionMode (SetPermissionModeRequest) returns (SetPermissionModeResponse) {}

  // GetPermissionMode reads the current mode.
  rpc GetPermissionMode (GetPermissionModeRequest) returns (GetPermissionModeResponse) {}

  // AllowToolCall + DenyToolCall are how a client replies to a
  // PermissionRequired stream event.
  rpc AllowToolCall (AllowToolCallRequest) returns (AllowToolCallResponse) {}
  rpc DenyToolCall  (DenyToolCallRequest)  returns (DenyToolCallResponse)  {}

  // GetProviderCapabilities reports what the active provider supports.
  rpc GetProviderCapabilities (GetProviderCapabilitiesRequest) returns (GetProviderCapabilitiesResponse) {}
```

- [ ] **Step 2: Add new messages**

Append:

```proto
message SetPermissionModeRequest  { string mode = 1; }     // "strict" | "permissive" | "bypass"
message SetPermissionModeResponse { bool ok = 1; string error = 2; }

message GetPermissionModeRequest  {}
message GetPermissionModeResponse { string mode = 1; }

message AllowToolCallRequest  { string tool_use_id = 1; }
message AllowToolCallResponse { bool ok = 1; }

message DenyToolCallRequest  { string tool_use_id = 1; }
message DenyToolCallResponse { bool ok = 1; }

message GetProviderCapabilitiesRequest {}
message GetProviderCapabilitiesResponse {
  bool supports_tools = 1;
  bool supports_parallel_tools = 2;
  bool supports_caching = 3;
  bool supports_vision = 4;
  int32 max_tools_per_call = 5;
}

// Tool-call event blocks for streaming responses.
message ToolUseStart   { string tool_use_id = 1; string tool_name = 2; }
message ToolUseStop    { string tool_use_id = 1; string args_summary = 2; }
message ToolExecStart  { string tool_use_id = 1; }
message ToolExecComplete {
  string tool_use_id = 1;
  string summary = 2;
  bool is_error = 3;
}
message PermissionRequired {
  string tool_use_id = 1;
  string tool_name = 2;
  string args_json = 3;
  string tier = 4;       // "W" | "X"
}
```

- [ ] **Step 3: Extend StreamProcessResponse**

Change the `oneof payload`:

```proto
message StreamProcessResponse {
  oneof payload {
    ProgressUpdate    progress = 1;
    ProcessRequestResponse final_response = 2;
    TokenDelta        token_delta = 3;
    ToolUseStart      tool_use_start = 4;
    ToolUseStop       tool_use_stop = 5;
    ToolExecStart     tool_exec_start = 6;
    ToolExecComplete  tool_exec_complete = 7;
    PermissionRequired permission_required = 8;
  }
}
```

- [ ] **Step 4: Extend PersistedTurn**

```proto
message PersistedTurn {
  string id = 1;
  string conversation_id = 2;
  string role = 3;
  string content = 4;
  int32  tokens_in = 5;
  int32  tokens_out = 6;
  int32  latency_ms = 7;
  int64  created_at = 8;
  string content_json = 9;  // ordered block array for tool-calling turns
}
```

- [ ] **Step 5: Regenerate protobuf**

Run:
```bash
cd source/server && make proto    # or: protoc --go_out=. --go-grpc_out=. ../proto/agent.proto
```

(If `make proto` doesn't exist, check the existing build for the exact `protoc` invocation. The generated Go files are typically at `source/server/pkg/proto/`.)

- [ ] **Step 6: Verify build**

Run: `cd source/server && go build ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/
git commit -m "proto(agent): SetPermissionMode + Allow/DenyToolCall + tool-call stream events + content_json"
```

---

### Task 22: Wire PermissionRequester through gRPC pending-decision map

**Files:**
- Modify: `source/server/cmd/cercano/main.go` (or wherever the gRPC server is constructed)
- Modify: `source/server/internal/agent/agent.go`

- [ ] **Step 1: Add a PendingDecisions map**

Create `source/server/internal/agent/pending.go`:

```go
package agent

import (
	"context"
	"sync"
)

type Decision struct {
	Allow bool
}

type PendingDecisions struct {
	mu       sync.Mutex
	channels map[string]chan Decision
}

func NewPendingDecisions() *PendingDecisions {
	return &PendingDecisions{channels: map[string]chan Decision{}}
}

func (p *PendingDecisions) Wait(ctx context.Context, toolUseID string) (bool, error) {
	ch := make(chan Decision, 1)
	p.mu.Lock()
	p.channels[toolUseID] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.channels, toolUseID)
		p.mu.Unlock()
	}()
	select {
	case d := <-ch:
		return d.Allow, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (p *PendingDecisions) Resolve(toolUseID string, allow bool) bool {
	p.mu.Lock()
	ch, ok := p.channels[toolUseID]
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- Decision{Allow: allow}
	return true
}
```

- [ ] **Step 2: Test it**

Create `source/server/internal/agent/pending_test.go`:

```go
package agent

import (
	"context"
	"testing"
	"time"
)

func TestPending_WaitResolves(t *testing.T) {
	p := NewPendingDecisions()
	go func() {
		time.Sleep(10 * time.Millisecond)
		p.Resolve("u1", true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	allow, err := p.Wait(ctx, "u1")
	if err != nil || !allow {
		t.Errorf("expected allow=true, err=nil; got %v %v", allow, err)
	}
}

func TestPending_WaitTimesOut(t *testing.T) {
	p := NewPendingDecisions()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Wait(ctx, "u1")
	if err == nil {
		t.Errorf("expected ctx timeout error")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd source/server && go test ./internal/agent/ -run TestPending -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/agent/pending.go source/server/internal/agent/pending_test.go
git commit -m "feat(agent): PendingDecisions map for AllowToolCall/DenyToolCall RPCs"
```

---

### Task 23: gRPC service handlers — SetPermissionMode, Allow/DenyToolCall, GetProviderCapabilities

**Files:**
- Modify: existing gRPC server impl (likely `source/server/internal/grpc/` or wherever `agent.RegisterAgentServer` is called)

- [ ] **Step 1: Implement handlers**

Locate the existing service implementation (search `RegisterAgentServer` or `UnimplementedAgentServer`). Add handlers that:

- `SetPermissionMode`: parses the mode string via `agent.ParseMode`, calls `permStore.SetMode(...)`, returns ok or error.
- `GetPermissionMode`: returns `string(permStore.Mode())`.
- `AllowToolCall`: calls `pendingDecisions.Resolve(tool_use_id, true)`, returns ok.
- `DenyToolCall`: calls `pendingDecisions.Resolve(tool_use_id, false)`, returns ok.
- `GetProviderCapabilities`: reads the active provider, maps `Capabilities()` to the proto response.

- [ ] **Step 2: Inject permStore + pendingDecisions when constructing the service**

In `main.go` (or wherever the service is wired):

```go
permStore, _ := agent.LoadPermissionStore("/Users/<user>/.config/cercano/permissions.yaml")
pending := agent.NewPendingDecisions()
service := NewAgentService(permStore, pending, /* existing deps */)
```

- [ ] **Step 3: Verify build**

Run: `cd source/server && go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/grpc/ source/server/cmd/cercano/main.go
git commit -m "feat(agent): gRPC handlers — SetPermissionMode, Allow/DenyToolCall, GetProviderCapabilities"
```

---

### Task 24: Wire tool loop into StreamProcessRequest with streaming events

**Files:**
- Modify: server-side StreamProcessRequest handler

- [ ] **Step 1: Adapt the handler to call RunToolLoop**

When the request would route to cloud (and provider supports tools), call `RunToolLoop` with a `PermissionRequester` that:
1. Sends `PermissionRequired{tool_use_id, tool_name, args_json, tier}` on the stream
2. Calls `pendingDecisions.Wait(ctx, tool_use_id)`
3. Returns the result

Tool-execution events (`ToolUseStart`, `ToolUseStop`, `ToolExecStart`, `ToolExecComplete`) are emitted from inside the loop. Refactor `RunToolLoop` to accept an optional event sink (`func(StreamEvent)`) and emit events at the right boundaries.

- [ ] **Step 2: Refactor loop to emit events**

Add to `ToolLoopInput`:

```go
EventSink func(ev StreamEvent)
```

Where `agent.StreamEvent` is a small new type (or reuse `llm.StreamEvent` if it fits). Define:

```go
type LoopEventKind string

const (
	LoopToolUseStart      LoopEventKind = "tool_use_start"
	LoopToolUseStop       LoopEventKind = "tool_use_stop"
	LoopToolExecStart     LoopEventKind = "tool_exec_start"
	LoopToolExecComplete  LoopEventKind = "tool_exec_complete"
	LoopPermissionRequired LoopEventKind = "permission_required"
)

type LoopEvent struct {
	Kind       LoopEventKind
	ToolUseID  string
	ToolName   string
	ArgsJSON   string
	Tier       string
	Summary    string
	IsError    bool
}
```

Emit `LoopToolUseStart` before scheduling execution; `LoopToolExecStart` before invoking each tool; `LoopToolExecComplete` after; `LoopPermissionRequired` before `PermissionRequester` callback.

- [ ] **Step 3: Test it**

Add to `toolloop_test.go`:

```go
func TestToolLoop_EmitsExpectedEvents(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "list_dir",
				ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	var events []LoopEventKind
	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
		EventSink: func(ev LoopEvent) { events = append(events, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []LoopEventKind{
		LoopToolUseStart, LoopToolExecStart, LoopToolExecComplete,
	}
	for _, k := range wantKinds {
		found := false
		for _, e := range events {
			if e == k { found = true; break }
		}
		if !found {
			t.Errorf("missing event %s", k)
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd source/server && go test ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go source/server/internal/grpc/
git commit -m "feat(agent): tool loop emits events; wire into StreamProcessRequest"
```

---

## Phase 9 — CLI Wiring

### Task 25: agentclient — wrappers for new RPCs

**Files:**
- Modify: `source/server/internal/cli/agentclient/client.go`

- [ ] **Step 1: Add wrappers**

```go
func (c *Client) SetPermissionMode(ctx context.Context, mode string) error {
	res, err := c.svc.SetPermissionMode(ctx, &pb.SetPermissionModeRequest{Mode: mode})
	if err != nil { return err }
	if !res.Ok { return fmt.Errorf("%s", res.Error) }
	return nil
}

func (c *Client) GetPermissionMode(ctx context.Context) (string, error) {
	res, err := c.svc.GetPermissionMode(ctx, &pb.GetPermissionModeRequest{})
	if err != nil { return "", err }
	return res.Mode, nil
}

func (c *Client) AllowToolCall(ctx context.Context, toolUseID string) error {
	_, err := c.svc.AllowToolCall(ctx, &pb.AllowToolCallRequest{ToolUseId: toolUseID})
	return err
}

func (c *Client) DenyToolCall(ctx context.Context, toolUseID string) error {
	_, err := c.svc.DenyToolCall(ctx, &pb.DenyToolCallRequest{ToolUseId: toolUseID})
	return err
}

type ProviderCaps struct {
	SupportsTools, SupportsParallelTools, SupportsCaching, SupportsVision bool
	MaxToolsPerCall int32
}

func (c *Client) GetProviderCapabilities(ctx context.Context) (ProviderCaps, error) {
	res, err := c.svc.GetProviderCapabilities(ctx, &pb.GetProviderCapabilitiesRequest{})
	if err != nil { return ProviderCaps{}, err }
	return ProviderCaps{
		SupportsTools: res.SupportsTools,
		SupportsParallelTools: res.SupportsParallelTools,
		SupportsCaching: res.SupportsCaching,
		SupportsVision: res.SupportsVision,
		MaxToolsPerCall: res.MaxToolsPerCall,
	}, nil
}
```

- [ ] **Step 2: Add streaming event channel cases**

In the streaming reader, add cases for new `oneof` payloads — emit them as typed events to the channel consumed by the UI model.

- [ ] **Step 3: Verify build**

Run: `cd source/server && go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/cli/agentclient/client.go
git commit -m "feat(cli/agentclient): wrappers for permission + tool-call RPCs"
```

---

### Task 26: Slash commands /strict /permissive /bypass /mode

**Files:**
- Create: `source/server/internal/cli/slash/permissions.go`
- Create: `source/server/internal/cli/slash/permissions_test.go`
- Modify: `source/server/internal/cli/slash/registry.go` (register commands)

- [ ] **Step 1: Write failing test**

```go
package slash

import "testing"

func TestRegisterPermissions_RegistersAll(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)
	for _, n := range []string{"strict", "permissive", "bypass", "mode"} {
		if _, ok := r.cmds[n]; !ok {
			t.Errorf("missing /%s", n)
		}
	}
}

func TestSlash_StrictSetsMode(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)  // nil client = dry-run dispatch
	res, _ := r.Dispatch("/strict")
	if res.Kind != ResultSetPermissionMode || res.PermissionMode != "strict" {
		t.Errorf("strict: %+v", res)
	}
}

func TestSlash_ModeArgRequired(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)
	res, _ := r.Dispatch("/mode")
	if res.Kind != ResultText {
		t.Errorf("expected usage hint, got %+v", res)
	}
}
```

- [ ] **Step 2: Add ResultSetPermissionMode to ResultKind**

In `registry.go`:

```go
const (
	// ... existing ...
	ResultSetPermissionMode
)

type Result struct {
	// ... existing ...
	PermissionMode string
}
```

- [ ] **Step 3: Implement commands**

`source/server/internal/cli/slash/permissions.go`:

```go
package slash

import (
	"cercano/source/server/internal/cli/agentclient"
)

func RegisterPermissions(r *Registry, _ *agentclient.Client) {
	for _, m := range []string{"strict", "permissive", "bypass"} {
		mode := m
		r.Register(Command{
			Name: mode,
			Help: "Set permission mode to " + mode + ".",
			Handler: func(args []string) Result {
				return Result{Kind: ResultSetPermissionMode, PermissionMode: mode}
			},
		})
	}
	r.Register(Command{
		Name: "mode",
		Help: "Set permission mode: /mode <strict|permissive|bypass>.",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultText, Text: "usage: /mode <strict|permissive|bypass>"}
			}
			switch args[0] {
			case "strict", "permissive", "bypass":
				return Result{Kind: ResultSetPermissionMode, PermissionMode: args[0]}
			default:
				return Result{Kind: ResultText, Text: "unknown mode: " + args[0]}
			}
		},
	})
}
```

- [ ] **Step 4: Register in main**

Wherever existing `Register*` functions are called for the slash registry, add `RegisterPermissions(r, client)`.

- [ ] **Step 5: Run tests + Commit**

Run: `cd source/server && go test ./internal/cli/slash/ -v`
Expected: PASS.

```bash
git add source/server/internal/cli/slash/permissions.go source/server/internal/cli/slash/permissions_test.go source/server/internal/cli/slash/registry.go
git commit -m "feat(cli/slash): /strict /permissive /bypass /mode commands"
```

---

### Task 27: UI model — handle ResultSetPermissionMode → RPC + status indicator

**Files:**
- Modify: `source/server/internal/cli/ui/model.go`

- [ ] **Step 1: Handle the new ResultKind**

In `runSlash` (or wherever slash results are dispatched), add:

```go
case slash.ResultSetPermissionMode:
	go func() {
		_ = m.agent.SetPermissionMode(context.Background(), res.PermissionMode)
	}()
	m.permissionMode = res.PermissionMode
	m.entries = append(m.entries, systemEntry("Permission mode → " + res.PermissionMode))
```

- [ ] **Step 2: Add permissionMode field and load on startup**

Add to `Model`:

```go
permissionMode string
```

In `Init` (or first message after connect), call `GetPermissionMode` to populate it.

- [ ] **Step 3: Render in status bar**

In `renderStatus`, append a mode chip. Use color: strict=red, permissive=amber, bypass=lime (or whatever palette has handy).

```go
modeColor := m.palette.AccentLime
switch m.permissionMode {
case "strict":     modeColor = m.palette.Error
case "permissive": modeColor = m.palette.Primary
case "bypass":     modeColor = m.palette.AccentLime
}
modeChip := lipgloss.NewStyle().Foreground(modeColor).Render("[" + m.permissionMode + "]")
```

Insert into the status bar layout next to the existing pieces.

- [ ] **Step 4: Verify**

Run: `cd source/server && go build ./...`
Expected: success.

Manually launch `cercano`, run `/strict`, `/permissive`, `/bypass`. Verify the chip in the status bar updates and the RPC is sent.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/ui/model.go
git commit -m "feat(cli/ui): permission mode chip in status bar + RPC dispatch"
```

---

### Task 28: Handle PermissionRequired streaming event → confirm UI → AllowToolCall/DenyToolCall RPC

**Files:**
- Modify: `source/server/internal/cli/ui/model.go`
- Modify: `source/server/internal/cli/ui/confirm_test.go`

- [ ] **Step 1: Add a streaming event case**

When the agentclient stream emits a `PermissionRequired` event (Task 25), translate to a `pendingConfirmMsg`:

```go
case agentclient.PermissionRequiredEvent:
	m.pendingConfirm = &pendingToolCall{
		ToolUseID: ev.ToolUseID,
		Name:      ev.ToolName,
		Args:      ev.ArgsJSON,
		Permission: ev.Tier,
	}
```

- [ ] **Step 2: Update resolveConfirmKey to RPC back**

When user accepts/denies, instead of locally invoking a tool, RPC:

```go
case "y":
	id := m.pendingConfirm.ToolUseID
	m.pendingConfirm = nil
	go func() { _ = m.agent.AllowToolCall(context.Background(), id) }()
	return m, nil
case "n", "esc":
	id := m.pendingConfirm.ToolUseID
	m.pendingConfirm = nil
	go func() { _ = m.agent.DenyToolCall(context.Background(), id) }()
	m.entries = append(m.entries, systemEntry("denied"))
	return m, nil
```

- [ ] **Step 3: Update confirm_test to drive via the event path**

Existing tests assume `m.pendingConfirm` was set locally by the slash dispatcher. They still work — just verify the resolution path calls `AllowToolCall`/`DenyToolCall` instead of an inline `InvokeTool` cmd. Mock the agent if needed:

```go
type stubAgent struct {
	allowed, denied []string
}
func (s *stubAgent) AllowToolCall(ctx context.Context, id string) error {
	s.allowed = append(s.allowed, id); return nil
}
func (s *stubAgent) DenyToolCall(ctx context.Context, id string) error {
	s.denied = append(s.denied, id); return nil
}
```

Update `minimalModel()` to take a stub:

```go
func TestResolveConfirmKey_Y_SendsAllow(t *testing.T) {
	stub := &stubAgent{}
	m := minimalModel()
	m.agent = stub
	m.pendingConfirm = &pendingToolCall{ToolUseID: "u1", Name: "write_file", Permission: "W"}
	next, _ := m.resolveConfirmKey("y")
	// goroutine fires asynchronously; sleep a tick
	time.Sleep(10 * time.Millisecond)
	if next.pendingConfirm != nil {
		t.Errorf("y should clear pending")
	}
	if len(stub.allowed) != 1 || stub.allowed[0] != "u1" {
		t.Errorf("expected AllowToolCall called for u1, got %v", stub.allowed)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd source/server && go test ./internal/cli/ui/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/ui/
git commit -m "feat(cli/ui): handle PermissionRequired events; reply via AllowToolCall/DenyToolCall RPC"
```

---

### Task 29: Folded scrollback tool-call entries with expand/collapse

**Files:**
- Create: `source/server/internal/cli/ui/scrollback_tool.go`
- Create: `source/server/internal/cli/ui/scrollback_tool_test.go`
- Modify: `source/server/internal/cli/ui/model.go`

- [ ] **Step 1: Write failing test**

```go
package ui

import (
	"strings"
	"testing"
)

func TestToolEntry_FoldedRender(t *testing.T) {
	e := ToolEntry{
		ToolName: "read_file",
		ArgsSummary: `path="main.go"`,
		Status: ToolStatusComplete,
		ResultSummary: "32 lines",
		Folded: true,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80))
	if !strings.Contains(s, "▸ read_file") {
		t.Errorf("expected fold marker + name, got: %q", s)
	}
	if !strings.Contains(s, "32 lines") {
		t.Errorf("expected result summary, got: %q", s)
	}
	if strings.Count(s, "\n") > 0 {
		t.Errorf("folded should be one line, got newlines in: %q", s)
	}
}

func TestToolEntry_ExpandedRender(t *testing.T) {
	e := ToolEntry{
		ToolName: "read_file",
		ArgsSummary: `path="main.go"`,
		FullArgs: `{"path":"main.go"}`,
		FullResult: "package main\n\nimport ...",
		Status: ToolStatusComplete,
		Folded: false,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80))
	if !strings.Contains(s, "▾ read_file") {
		t.Errorf("expected unfold marker, got: %q", s)
	}
	if !strings.Contains(s, `"path":"main.go"`) {
		t.Errorf("expanded should show full args, got: %q", s)
	}
}

func TestToolEntry_InProgress(t *testing.T) {
	e := ToolEntry{ToolName: "grep", Status: ToolStatusInProgress, Folded: true}
	s := stripAnsiCSI(renderToolEntry(e, 80))
	if !strings.Contains(s, "grep") {
		t.Errorf("name missing: %q", s)
	}
}
```

- [ ] **Step 2: Implement**

`source/server/internal/cli/ui/scrollback_tool.go`:

```go
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type ToolStatus int

const (
	ToolStatusInProgress ToolStatus = iota
	ToolStatusComplete
	ToolStatusError
)

type ToolEntry struct {
	ToolUseID     string
	ToolName      string
	ArgsSummary   string
	FullArgs      string
	FullResult    string
	ResultSummary string
	Status        ToolStatus
	Folded        bool
}

func renderToolEntry(e ToolEntry, width int) string {
	marker := "▸"
	if !e.Folded {
		marker = "▾"
	}
	statusBit := ""
	switch e.Status {
	case ToolStatusInProgress:
		statusBit = lipgloss.NewStyle().Faint(true).Render("…")
	case ToolStatusComplete:
		statusBit = lipgloss.NewStyle().Foreground(lipgloss.Color("#BDF000")).
			Render("✓ " + e.ResultSummary)
	case ToolStatusError:
		statusBit = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).
			Render("⚠ " + e.ResultSummary)
	}
	line := fmt.Sprintf("%s %s %s   %s",
		marker, e.ToolName, lipgloss.NewStyle().Faint(true).Render(e.ArgsSummary), statusBit)
	if e.Folded {
		return line
	}
	body := []string{line}
	if e.FullArgs != "" {
		body = append(body, lipgloss.NewStyle().Faint(true).Render("  args: " + e.FullArgs))
	}
	if e.FullResult != "" {
		body = append(body, "  " + indent(e.FullResult, "  "))
	}
	return strings.Join(body, "\n")
}

func indent(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n" + prefix)
}
```

- [ ] **Step 3: Wire into the model's scrollback**

In `model.go`, the entries slice should now accept `ToolEntry` alongside text entries. Either type-tag entries, or add ToolEntries as a parallel structure. The renderer walks both.

Add a `toolEntries map[string]*ToolEntry` keyed by `tool_use_id`. Streaming events:
- `ToolUseStart` → create entry with status InProgress
- `ToolUseStop` → fill ArgsSummary
- `ToolExecComplete` → set status + ResultSummary
- key press: `tab` while a tool entry is highlighted → toggle Folded (introduce a focus index)

For V1, simpler: every tool entry renders folded by default, no per-entry focus. Add a slash command `/expand last` or just leave un-foldable for the very first iteration. Mark as TODO in a follow-up.

- [ ] **Step 4: Run tests**

Run: `cd source/server && go test ./internal/cli/ui/ -run TestToolEntry -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/ui/scrollback_tool.go source/server/internal/cli/ui/scrollback_tool_test.go source/server/internal/cli/ui/model.go
git commit -m "feat(cli/ui): folded tool-call scrollback entries; expand/collapse stub"
```

---

## Phase 10 — Integration & Cleanup

### Task 30: Replace langchaingo path with new Anthropic adapter in main wiring

**Files:**
- Modify: `source/server/cmd/cercano/main.go`
- Modify: `source/server/internal/agent/agent.go`

- [ ] **Step 1: Stop constructing `llm.CloudModelProvider`; construct anthropic.Client instead**

Find where `llm.NewCloudModelProvider(...)` is called. Replace with:

```go
if cfg.CloudProvider == "anthropic" {
	c := anthropicpkg.NewClient(anthropicpkg.Config{
		BaseURL:   cfg.CloudBaseURL,
		APIKey:    cfg.CloudAPIKey,
		Model:     cfg.CloudModel,
		UserAgent: "claude-cli/1.0",
	})
	cloudProvider = c
}
```

The `Agent`'s old `provider.Process(req)` path becomes a wrapper that calls `cloudProvider.Chat(...)` with a single user-text message. The new tool-enabled path calls `RunToolLoop(...)`.

- [ ] **Step 2: Remove langchaingo import**

Once the cloud path no longer references it (the local-only path still uses it temporarily — that's fine), drop unused references. Leave `internal/llm/langchain.go` in place but un-imported; delete in a later cleanup.

- [ ] **Step 3: Verify build + tests**

Run: `cd source/server && go build ./... && go test ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add source/server/cmd/cercano/main.go source/server/internal/agent/agent.go
git commit -m "refactor(agent): cloud path uses new anthropic adapter instead of langchaingo"
```

---

### Task 31: Update /tool slash command to flow through agent gate

**Files:**
- Modify: `source/server/internal/cli/slash/tools.go`
- Modify: `source/server/internal/cli/ui/model.go`

- [ ] **Step 1: Route /tool through gRPC InvokeTool but gate via PermissionRequired**

Currently `/tool` emits `ResultInvokeTool` and the model directly invokes. With the new flow, the agent's `InvokeTool` RPC should also consult the permission mode and emit `PermissionRequired` if needed.

In the agent's `InvokeTool` handler:

```go
if agent.GateDecision(permStore.Mode(), permissionToLLM(tool.Permission())) {
	// Need to surface as PermissionRequired but /tool isn't on the
	// streaming RPC. Simplest: synchronously prompt via a side stream
	// channel keyed by a synthesized tool_use_id. Or: refuse here and
	// instruct the user that /tool requires bypass mode for X-tier.
}
```

Choice for V1 minimum: when `/tool` is used to invoke a W/X-tier tool, the agent returns a "permission required" error, prompting the user to either use `/bypass` or have the agent invoke it via chat. Document this limitation in `/tool` help text.

Update `/tool` help text:

```
help: "Invoke a tool directly: /tool <name> <json-args>. R-tier runs silently; W/X-tier requires /bypass or invoke via chat (model-driven tool calls use the streaming confirm prompt)."
```

- [ ] **Step 2: Update existing tests + commit**

```bash
git add source/server/internal/cli/slash/tools.go
git commit -m "feat(cli/slash): /tool W/X-tier requires bypass mode (direct invoke can't stream confirm)"
```

---

### Task 32: Edit docs/plans/cli.md to remove embedding-based tool selection

**Files:**
- Modify: `docs/plans/cli.md`

- [ ] **Step 1: Remove the line**

Find the sentence "tool selection for ambiguous intent (embedding similarity over tool descriptions, LLM fallback only)" and remove the parenthetical, since the agent now picks tools natively with parameters. The surrounding text about "algorithmic over LLM" stays.

- [ ] **Step 2: Add reference to the new design**

Append to the relevant section:

```markdown
> Tool selection is now handled by native tool calling — the model emits structured tool_use blocks via the provider's tool-calling channel. See `docs/plans/native_tool_calling.md`.
```

- [ ] **Step 3: Commit**

```bash
git add docs/plans/cli.md
git commit -m "docs(plans/cli): remove embedding-based tool selection; native tool calling supersedes it"
```

---

### Task 33: Mark docs/plans/dispatch.md as superseded

**Files:**
- Modify: `docs/plans/dispatch.md`

- [ ] **Step 1: Update status line**

Replace the existing status note with:

```markdown
> Status: **Superseded** by `docs/plans/native_tool_calling.md` for V1. The native-tool-calling design covers both Anthropic and Ollama uniformly and includes the confirm-gating UI dispatch.md deferred. The host-LLM cancellation, conversation_id continuity, and MCP progress-event goals can move to a follow-up if still wanted.
```

- [ ] **Step 2: Commit**

```bash
git add docs/plans/dispatch.md
git commit -m "docs(plans/dispatch): mark superseded by native_tool_calling.md"
```

---

### Task 34: Integration test — full turn with a tool call via httptest Anthropic

**Files:**
- Create: `source/server/internal/agent/integration_test.go`

- [ ] **Step 1: Write integration test**

```go
package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm/anthropic"
)

func TestIntegration_FullTurnWithToolCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// First call: model says "I'll list the dir" and emits a tool_use.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "m_1", "type": "message", "role": "assistant",
				"model": "claude",
				"content": []map[string]any{
					{"type": "text", "text": "Listing."},
					{"type": "tool_use", "id": "u1", "name": "list_dir",
						"input": map[string]any{"path": "."}},
				},
				"stop_reason": "tool_use",
				"usage":       map[string]int{"input_tokens": 10, "output_tokens": 5},
			})
		} else {
			// Second call: model summarizes and stops.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "m_2", "type": "message", "role": "assistant",
				"model": "claude",
				"content": []map[string]any{
					{"type": "text", "text": "Done."},
				},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 20, "output_tokens": 3},
			})
		}
	}))
	defer srv.Close()

	prov := anthropic.NewClient(anthropic.Config{
		BaseURL: srv.URL, APIKey: "dummy", Model: "claude",
	})
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Model: "claude", UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 round-trips, got %d", calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}
```

- [ ] **Step 2: Run test**

Run: `cd source/server && go test ./internal/agent/ -run TestIntegration -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/agent/integration_test.go
git commit -m "test(agent): integration — full tool-call turn through anthropic adapter"
```

---

### Task 35: Update the implementation plan's status

**Files:**
- Modify: `docs/plans/native_tool_calling.md`

- [ ] **Step 1: Update the Status section**

Replace `Status` with:

```markdown
## Status

Design approved. Implementation complete:
- [x] Provider layer (Anthropic + Ollama adapters)
- [x] Tool catalog assembly
- [x] Bounded autonomous tool loop with R-tier concurrency, W/X serialization
- [x] Permission modes (strict / permissive / bypass) stored agent-side
- [x] PermissionRequired streaming + Allow/DenyToolCall RPCs
- [x] Persistence with content_json column on turns
- [x] CLI confirm UI repointed to streaming events
- [x] CLI slash commands /strict /permissive /bypass /mode
- [x] CLI folded tool-call scrollback entries (expand/collapse stubbed)
- [x] Integration tests across both providers
```

- [ ] **Step 2: Commit**

```bash
git add docs/plans/native_tool_calling.md
git commit -m "docs(plans/native_tool_calling): mark implementation complete"
```

---

## Self-Review Notes

- All spec sections have corresponding tasks: provider architecture (Tasks 1-12), persistence (13), permission modes (14-15), tool catalog (16), agent loop (17-20), gRPC surface (21-24), CLI wiring (25-29), migration & cleanup (30-33), integration tests (34), status update (35).
- Anthropic SDK type names (`OfText`, `OfToolUse`, `MessageStreamEventUnion`, etc.) are marked as guesses — Task 6 and Task 9 include an explicit "browse the SDK source and adapt" instruction since the exact names depend on the actual v1.51.0 generated code.
- Ollama API types (`api.Message`, `api.ChatRequest.Tools`, `api.ToolCall`) are similarly best-effort; Task 10 calls out the cross-check.
- Tasks that depend on partly-known external library shapes (5, 6, 9, 10, 11, 12) instruct the engineer to verify against actual module source after `go get`.
- The `/tool` slash command handling of W/X-tier is intentionally limited in V1 (Task 31) — direct invoke requires bypass mode because the unary InvokeTool RPC can't stream a confirm prompt. Model-driven tool calls (the normal chat path) flow through the streaming gate correctly. This limitation is documented in `/tool` help text.
- Folded-entry expand/collapse is stubbed in Task 29 — V1 renders everything folded; per-entry focus + tab-to-expand is a follow-up.
