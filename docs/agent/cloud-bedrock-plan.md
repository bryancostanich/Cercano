# Amazon Bedrock Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `llm.Provider` for Amazon Bedrock over the AWS SDK v2 Converse/ConverseStream API — parity (text/tools/vision/streaming), credentials via the AWS default chain.

**Architecture:** A new `internal/llm/bedrock/` package wraps `aws-sdk-go-v2`'s `bedrockruntime` client behind `llm.Provider`. The client depends on a small `converseAPI` interface (satisfied by `*bedrockruntime.Client`) so the non-streaming path is unit-testable with a fake; the streaming event→`llm.StreamEvent` mapping is a pure function. Credentials resolve via `config.LoadDefaultConfig`; the profile carries region + model.

**Tech Stack:** Go 1.26; `github.com/aws/aws-sdk-go-v2/{config,aws,service/bedrockruntime,service/bedrockruntime/types,service/bedrockruntime/document}`.

Design spec: [cloud-bedrock.md](./cloud-bedrock.md).

## Global Constraints

- **AWS SDK v2 + Converse API only.** No hand-rolled SigV4. `go-openai` is not involved.
- **Credentials via the AWS default chain** (`config.LoadDefaultConfig`); no secret in Cercano's keychain for bedrock.
- **`Name()` returns `"bedrock"`.** Capabilities: `SupportsTools:true, SupportsParallelTools:true, SupportsVision:true, SupportsCaching:false`.
- **`NewClient` returns `(*Client, error)`** (config load can fail). The factory propagates it.
- **Region is required** for bedrock; a missing region is the build-failure mode (clear error → absent provider).
- **Converse takes image *bytes*** (not URL/base64). Resolve via the existing `llm.ResolveImageBytes(ctx, block)`.
- **Tool input/output documents:** convert JSON→document with `document.NewLazyDocument`; convert document→JSON with the document's **`MarshalSmithyDocument()`** method (VERIFIED: `UnmarshalSmithyDocument` into `any`/`json.RawMessage` errors and corrupts numbers — do NOT use it).
- **Streaming mapping is a pure function** `mapStreamEvent(types.ConverseStreamOutput) (llm.StreamEvent, bool)`, unit-tested with synthetic events.
- **Keyless-guard exemption:** `server.go`'s "keyless → absent" guard (`server.go:270`) must add `&& p.Flavor != cloudfactory.FlavorBedrock`.
- **Build/test from `source/server/`.** Test a package: `go test ./internal/llm/bedrock/ -count=1`.
- **Out of scope (do NOT build):** extended-thinking/reasoning, `InvokeModel`, prompt caching, guardrails, inference-profile richer handling, image generation, CLI/proto surfacing of `region`/`aws_profile` (YAML-only). See the spec's "Future improvements" section — leave those as documented follow-ons, do not implement.

---

## File Structure

**New (all in `internal/llm/bedrock/`):**
- `adapter.go` + `adapter_test.go` — `llm` ↔ Converse `types` translation + document/image helpers.
- `client.go` + `client_test.go` — `Config`, `converseAPI` interface, `NewClient`, `Name`, `Capabilities`, `Chat`.
- `stream.go` + `stream_test.go` — pure `mapStreamEvent`, `streamReader`, `StreamChat`.
- `client_integration_test.go` — gated live tests.

**Modified:**
- `source/server/go.mod` / `go.sum` — add AWS deps (Task 1).
- `pkg/config/config.go` — `Region` + `AWSProfile` on `CloudProfile` (Task 4).
- `internal/cloudfactory/factory.go` — fill the `bedrock` case (Task 4).
- `internal/server/server.go:270` — keyless-guard exemption (Task 4).
- `docs/agent/cloud-bedrock.md` — flip status (Task 5).

---

### Task 1: Adapter + AWS dependency

Add the AWS deps and the pure translation between `llm` types and Converse `types`, plus the document/image helpers. No network in tests (base64 images + an `httptest` image server).

**Files:**
- Modify: `source/server/go.mod`, `source/server/go.sum`
- Create: `source/server/internal/llm/bedrock/adapter.go`, `adapter_test.go`

**Interfaces:**
- Produces: `messagesToConverse(ctx, []llm.Message) ([]types.Message, error)`; `systemBlocks(string) []types.SystemContentBlock`; `toolsToConverse([]llm.Tool) *types.ToolConfiguration`; `inferenceConfig(llm.ChatRequest) *types.InferenceConfiguration`; `blocksFromConverse(types.Message) []llm.Block`; `jsonToDocument(json.RawMessage) document.Interface`; `documentToJSON(document.Interface) json.RawMessage`; `imageFormat(string, []byte) types.ImageFormat`.

- [ ] **Step 1: Add the AWS dependencies**

Run:
```bash
cd source/server
go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
```
Expected: `go.mod` gains `aws-sdk-go-v2/service/bedrockruntime` and `aws-sdk-go-v2/config` (they may already be present as indirect — `go get` makes them resolvable). `go mod tidy` runs in Step 5 after the imports exist.

- [ ] **Step 2: Write the failing tests**

Create `source/server/internal/llm/bedrock/adapter_test.go`:

```go
package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

func redPNG(t *testing.T) []byte {
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

func TestDocumentRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"city":"Paris","n":3}`)
	got := documentToJSON(jsonToDocument(raw))
	// numbers must survive (n stays 3, not "3")
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", got, err)
	}
	if m["city"] != "Paris" || m["n"].(float64) != 3 {
		t.Fatalf("round-trip lost data: %s", got)
	}
}

func TestMessagesToConverse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(redPNG(t))
	}))
	defer srv.Close()

	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "hi"},
			{Type: llm.BlockImage, ImageURL: srv.URL},
		}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "get_weather", ToolInput: json.RawMessage(`{"city":"Paris"}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "call_1", Content: "sunny"},
		}},
	}
	out, err := messagesToConverse(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out))
	}
	// msg0: user with text + image
	if out[0].Role != types.ConversationRoleUser || len(out[0].Content) != 2 {
		t.Fatalf("msg0 = %+v", out[0])
	}
	if _, ok := out[0].Content[0].(*types.ContentBlockMemberText); !ok {
		t.Errorf("msg0.0 not text: %T", out[0].Content[0])
	}
	img, ok := out[0].Content[1].(*types.ContentBlockMemberImage)
	if !ok || img.Value.Format != types.ImageFormatPng {
		t.Errorf("msg0.1 image wrong: %T fmt=%v", out[0].Content[1], img)
	}
	if bs, ok := img.Value.Source.(*types.ImageSourceMemberBytes); !ok || len(bs.Value) == 0 {
		t.Errorf("image bytes missing")
	}
	// msg1: assistant tool use
	tu, ok := out[1].Content[0].(*types.ContentBlockMemberToolUse)
	if !ok || aws.ToString(tu.Value.ToolUseId) != "call_1" || aws.ToString(tu.Value.Name) != "get_weather" {
		t.Errorf("msg1 tool use wrong: %+v", out[1].Content[0])
	}
	// msg2: user tool result
	tr, ok := out[2].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok || aws.ToString(tr.Value.ToolUseId) != "call_1" || tr.Value.Status != types.ToolResultStatusSuccess {
		t.Errorf("msg2 tool result wrong: %+v", out[2].Content[0])
	}
}

func TestToolsAndSystemAndInference(t *testing.T) {
	tc := toolsToConverse([]llm.Tool{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}})
	if tc == nil || len(tc.Tools) != 1 {
		t.Fatalf("tool config = %+v", tc)
	}
	if ts, ok := tc.Tools[0].(*types.ToolMemberToolSpec); !ok || aws.ToString(ts.Value.Name) != "get_weather" {
		t.Errorf("toolspec wrong: %+v", tc.Tools[0])
	}
	if toolsToConverse(nil) != nil {
		t.Error("nil tools should map to nil config")
	}
	if systemBlocks("") != nil {
		t.Error("empty system should be nil")
	}
	if sb := systemBlocks("sys"); len(sb) != 1 {
		t.Errorf("system blocks = %+v", sb)
	}
	if inferenceConfig(llm.ChatRequest{}) != nil {
		t.Error("no max/temp → nil inference config")
	}
	ic := inferenceConfig(llm.ChatRequest{MaxTokens: 100})
	if ic == nil || aws.ToInt32(ic.MaxTokens) != 100 {
		t.Errorf("inference config = %+v", ic)
	}
}

func TestBlocksFromConverse(t *testing.T) {
	m := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "hello"},
			&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: aws.String("call_1"), Name: aws.String("get_weather"),
				Input: jsonToDocument(json.RawMessage(`{"city":"Paris"}`)),
			}},
		},
	}
	blocks := blocksFromConverse(m)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != llm.BlockText || blocks[0].Text != "hello" {
		t.Errorf("block0 = %+v", blocks[0])
	}
	if blocks[1].Type != llm.BlockToolUse || blocks[1].ToolUseID != "call_1" || blocks[1].ToolName != "get_weather" {
		t.Errorf("block1 = %+v", blocks[1])
	}
	var in map[string]any
	if err := json.Unmarshal(blocks[1].ToolInput, &in); err != nil || in["city"] != "Paris" {
		t.Errorf("block1 input = %s (err %v)", blocks[1].ToolInput, err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/bedrock/ -count=1`
Expected: FAIL — build error, `undefined: documentToJSON` etc.

- [ ] **Step 4: Implement the adapter**

Create `source/server/internal/llm/bedrock/adapter.go`:

```go
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

// jsonToDocument wraps raw JSON as a Converse document (for request bodies).
func jsonToDocument(raw json.RawMessage) document.Interface {
	if len(raw) == 0 {
		return document.NewLazyDocument(map[string]any{})
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return document.NewLazyDocument(map[string]any{})
	}
	return document.NewLazyDocument(v)
}

// documentToJSON serializes a Converse document back to JSON. It uses
// MarshalSmithyDocument — the only round-trip-safe method (UnmarshalSmithyDocument
// into any/json.RawMessage errors and corrupts numbers to strings).
func documentToJSON(d document.Interface) json.RawMessage {
	if d == nil {
		return json.RawMessage("{}")
	}
	b, err := d.MarshalSmithyDocument()
	if err != nil || len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// imageFormat maps a media type (or sniffed bytes) to a Converse image format.
func imageFormat(mediaType string, data []byte) types.ImageFormat {
	mt := mediaType
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	switch {
	case strings.Contains(mt, "png"):
		return types.ImageFormatPng
	case strings.Contains(mt, "jpeg"), strings.Contains(mt, "jpg"):
		return types.ImageFormatJpeg
	case strings.Contains(mt, "gif"):
		return types.ImageFormatGif
	case strings.Contains(mt, "webp"):
		return types.ImageFormatWebp
	default:
		return types.ImageFormatPng
	}
}

// messagesToConverse maps llm messages to Converse messages, resolving image
// blocks to raw bytes (Converse takes bytes, not URLs/base64).
func messagesToConverse(ctx context.Context, msgs []llm.Message) ([]types.Message, error) {
	out := make([]types.Message, 0, len(msgs))
	for _, m := range msgs {
		role := types.ConversationRoleUser
		if m.Role == llm.RoleAssistant {
			role = types.ConversationRoleAssistant
		}
		var content []types.ContentBlock
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				content = append(content, &types.ContentBlockMemberText{Value: b.Text})
			case llm.BlockImage:
				data, err := llm.ResolveImageBytes(ctx, b)
				if err != nil {
					return nil, fmt.Errorf("bedrock: resolve image: %w", err)
				}
				content = append(content, &types.ContentBlockMemberImage{Value: types.ImageBlock{
					Format: imageFormat(b.MediaType, data),
					Source: &types.ImageSourceMemberBytes{Value: data},
				}})
			case llm.BlockToolUse:
				content = append(content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(b.ToolUseID),
					Name:      aws.String(b.ToolName),
					Input:     jsonToDocument(b.ToolInput),
				}})
			case llm.BlockToolResult:
				status := types.ToolResultStatusSuccess
				if b.IsError {
					status = types.ToolResultStatusError
				}
				content = append(content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
					ToolUseId: aws.String(b.ToolUseRef),
					Status:    status,
					Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: b.Content}},
				}})
			}
		}
		out = append(out, types.Message{Role: role, Content: content})
	}
	return out, nil
}

func systemBlocks(system string) []types.SystemContentBlock {
	if system == "" {
		return nil
	}
	return []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: system}}
}

func toolsToConverse(tools []llm.Tool) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}
	out := make([]types.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name:        aws.String(t.Name),
			Description: aws.String(t.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: jsonToDocument(t.Schema)},
		}})
	}
	return &types.ToolConfiguration{Tools: out}
}

func inferenceConfig(req llm.ChatRequest) *types.InferenceConfiguration {
	if req.MaxTokens <= 0 && req.Temperature <= 0 {
		return nil
	}
	ic := &types.InferenceConfiguration{}
	if req.MaxTokens > 0 {
		ic.MaxTokens = aws.Int32(int32(req.MaxTokens))
	}
	if req.Temperature > 0 {
		t := float32(req.Temperature)
		ic.Temperature = &t
	}
	return ic
}

// blocksFromConverse maps a Converse output message to llm blocks.
func blocksFromConverse(m types.Message) []llm.Block {
	var blocks []llm.Block
	for _, c := range m.Content {
		switch v := c.(type) {
		case *types.ContentBlockMemberText:
			if v.Value != "" {
				blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: v.Value})
			}
		case *types.ContentBlockMemberToolUse:
			blocks = append(blocks, llm.Block{
				Type:      llm.BlockToolUse,
				ToolUseID: aws.ToString(v.Value.ToolUseId),
				ToolName:  aws.ToString(v.Value.Name),
				ToolInput: documentToJSON(v.Value.Input),
			})
		}
	}
	return blocks
}
```

- [ ] **Step 5: Tidy modules, run tests**

Run: `cd source/server && go mod tidy && go test ./internal/llm/bedrock/ -count=1`
Expected: PASS (4 tests). `go mod tidy` promotes the AWS deps to direct in go.mod.

- [ ] **Step 6: Commit**

```bash
git add source/server/go.mod source/server/go.sum source/server/internal/llm/bedrock/adapter.go source/server/internal/llm/bedrock/adapter_test.go
git commit -m "feat(bedrock): AWS deps + llm<->Converse adapter"
```

---

### Task 2: Client — Config, NewClient, Chat, Name, Capabilities

The non-streaming client over the Converse API, unit-tested with a fake `converseAPI`.

**Files:**
- Create: `source/server/internal/llm/bedrock/client.go`, `client_test.go`

**Interfaces:**
- Consumes: the adapter funcs (Task 1).
- Produces: `bedrock.Config{Region, Model, AWSProfile, BaseURL string}`; `converseAPI` interface; `NewClient(Config) (*Client, error)`; `(*Client).Name()`, `.Capabilities()`, `.Chat(ctx, llm.ChatRequest) (llm.ChatResponse, error)`; placeholder `.StreamChat` (replaced in Task 3). `Client` struct has fields `api converseAPI` and `model string` (so tests construct `&Client{api: fake, model: "m"}`).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/bedrock/client_test.go`:

```go
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

type fakeAPI struct {
	out *bedrockruntime.ConverseOutput
	err error
	in  *bedrockruntime.ConverseInput
}

func (f *fakeAPI) Converse(ctx context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	f.in = in
	return f.out, f.err
}
func (f *fakeAPI) ConverseStream(ctx context.Context, in *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	return nil, fmt.Errorf("not used in this test")
}

func TestChatMapsResponse(t *testing.T) {
	fake := &fakeAPI{out: &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "hello"},
				&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String("call_1"), Name: aws.String("get_weather"),
					Input: jsonToDocument(json.RawMessage(`{"city":"Paris"}`)),
				}},
			},
		}},
		Usage: &types.TokenUsage{InputTokens: aws.Int32(7), OutputTokens: aws.Int32(3)},
	}}
	c := &Client{api: fake, model: "anthropic.claude-x"}
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 2 || resp.Blocks[0].Text != "hello" || resp.Blocks[1].ToolName != "get_weather" {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 3 || resp.StopReason != "end_turn" {
		t.Errorf("usage/stop = %d/%d/%q", resp.InputTokens, resp.OutputTokens, resp.StopReason)
	}
	// request shape: model + system carried.
	if aws.ToString(fake.in.ModelId) != "anthropic.claude-x" || len(fake.in.System) != 1 {
		t.Errorf("request = model %q system %d", aws.ToString(fake.in.ModelId), len(fake.in.System))
	}
	if c.Name() != "bedrock" || !c.Capabilities().SupportsVision || !c.Capabilities().SupportsTools {
		t.Errorf("name/caps wrong")
	}
}

func TestChatError(t *testing.T) {
	c := &Client{api: &fakeAPI{err: fmt.Errorf("boom")}, model: "m"}
	_, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewClientRequiresRegion(t *testing.T) {
	if _, err := NewClient(Config{Model: "m"}); err == nil {
		t.Fatal("expected error when region is empty")
	}
	c, err := NewClient(Config{Region: "us-east-1", Model: "m"})
	if err != nil || c == nil || c.Name() != "bedrock" {
		t.Fatalf("NewClient(region) → %v, %v", c, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/bedrock/ -run 'TestChat|TestNewClient' -count=1`
Expected: FAIL — `undefined: Client` / `NewClient`.

- [ ] **Step 3: Implement the client**

Create `source/server/internal/llm/bedrock/client.go`:

```go
package bedrock

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

// Config holds the Bedrock client configuration.
type Config struct {
	Region     string // required, e.g. "us-east-1"
	Model      string // Bedrock model id or inference-profile id
	AWSProfile string // optional ~/.aws named profile
	BaseURL    string // optional endpoint override (VPC/private)
}

// converseAPI is the subset of *bedrockruntime.Client the provider uses, so the
// non-streaming path is unit-testable with a fake.
type converseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, in *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// Client implements llm.Provider over the Bedrock Converse API.
type Client struct {
	api   converseAPI
	model string
}

// NewClient builds a Client. Credentials resolve via the AWS default chain
// (env / ~/.aws / SSO / IAM). Region is required. Returns an error because the
// AWS config load can fail.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: region is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AWSProfile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.AWSProfile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: load AWS config: %w", err)
	}
	api := bedrockruntime.NewFromConfig(awsCfg, func(o *bedrockruntime.Options) {
		if cfg.BaseURL != "" {
			o.BaseEndpoint = aws.String(cfg.BaseURL)
		}
	})
	return &Client{api: api, model: cfg.Model}, nil
}

func (c *Client) Name() string { return "bedrock" }

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

// Chat sends a non-streaming Converse request and maps the output to blocks.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	msgs, err := messagesToConverse(ctx, req.Messages)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	out, err := c.api.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId:         aws.String(modelOr(c.model, req.Model)),
		Messages:        msgs,
		System:          systemBlocks(req.System),
		ToolConfig:      toolsToConverse(req.Tools),
		InferenceConfig: inferenceConfig(req),
	})
	if err != nil {
		return llm.ChatResponse{}, fmt.Errorf("bedrock: converse: %w", err)
	}
	resp := llm.ChatResponse{StopReason: string(out.StopReason)}
	if m, ok := out.Output.(*types.ConverseOutputMemberMessage); ok {
		resp.Blocks = blocksFromConverse(m.Value)
	}
	if out.Usage != nil {
		resp.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		resp.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
	}
	return resp, nil
}

// StreamChat is implemented in stream.go (Task 3). Placeholder so the package
// compiles and satisfies llm.Provider; replaced next task.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, fmt.Errorf("bedrock: streaming not yet implemented")
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/bedrock/ -count=1`
Expected: PASS (adapter tests + the three client tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/bedrock/client.go source/server/internal/llm/bedrock/client_test.go
git commit -m "feat(bedrock): Converse client + Chat (non-streaming)"
```

---

### Task 3: Streaming — pure mapper + streamReader + StreamChat

Map Converse stream events to `llm.StreamEvent` (pure, unit-tested), wrap the SDK event stream, and replace the placeholder `StreamChat`.

**Files:**
- Create: `source/server/internal/llm/bedrock/stream.go`, `stream_test.go`
- Modify: `source/server/internal/llm/bedrock/client.go` (replace placeholder `StreamChat`)

**Interfaces:**
- Consumes: `llm.StreamEvent`, the event consts; the `converseAPI` (Task 2).
- Produces: `mapStreamEvent(types.ConverseStreamOutput) (llm.StreamEvent, bool)`; `newStreamReader(*bedrockruntime.ConverseStreamEventStream) *streamReader` implementing `llm.StreamReader`; the real `(*Client).StreamChat`.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/llm/bedrock/stream_test.go`:

```go
package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

func TestMapStreamEvent(t *testing.T) {
	cases := []struct {
		name  string
		in    types.ConverseStreamOutput
		emit  bool
		etype llm.StreamEventType
	}{
		{"start", &types.ConverseStreamOutputMemberMessageStart{Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant}}, true, llm.EventMessageStart},
		{"text", &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberText{Value: "hi"}}}, true, llm.EventTextDelta},
		{"toolstart", &types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("call_1"), Name: aws.String("get_weather")}}}}, true, llm.EventToolUseStart},
		{"toolinput", &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(`{"city":`)}}}}, true, llm.EventToolUseInputDelta},
		{"blockstop", &types.ConverseStreamOutputMemberContentBlockStop{Value: types.ContentBlockStopEvent{}}, true, llm.EventToolUseStop},
		{"msgstop", &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}}, true, llm.EventMessageStop},
		{"metadata", &types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(9), OutputTokens: aws.Int32(4)}}}, true, llm.EventMessageStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, emit := mapStreamEvent(tc.in)
			if emit != tc.emit || (emit && ev.Type != tc.etype) {
				t.Fatalf("got (%+v, %v), want type %v emit %v", ev, emit, tc.etype, tc.emit)
			}
		})
	}

	// field-level checks
	ev, _ := mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("call_1"), Name: aws.String("get_weather")}}}})
	if ev.ToolUseID != "call_1" || ev.ToolName != "get_weather" {
		t.Errorf("tool start fields = %+v", ev)
	}
	ev, _ = mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberText{Value: "hi"}}})
	if ev.TextDelta != "hi" {
		t.Errorf("text delta = %q", ev.TextDelta)
	}
	ev, _ = mapStreamEvent(&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(9), OutputTokens: aws.Int32(4)}}})
	if ev.InputTokens != 9 || ev.OutputTokens != 4 {
		t.Errorf("metadata usage = %d/%d", ev.InputTokens, ev.OutputTokens)
	}

	// a non-tool content block start emits nothing
	if _, emit := mapStreamEvent(&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{}}); emit {
		t.Error("non-tool block start should not emit")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/llm/bedrock/ -run TestMapStreamEvent -count=1`
Expected: FAIL — `undefined: mapStreamEvent`.

- [ ] **Step 3: Implement the stream mapper + reader**

Create `source/server/internal/llm/bedrock/stream.go`:

```go
package bedrock

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

// mapStreamEvent translates one Converse stream event into an llm.StreamEvent.
// The bool is false for events we don't surface (e.g. a non-tool block start).
// It is a pure function so it can be unit-tested with synthetic events.
//
// ContentBlockStop always maps to EventToolUseStop: the tool-loop's flushTool is
// a no-op when no tool block is open, so a text block's stop is harmless. Metadata
// (usage) and MessageStop both map to EventMessageStop — the tool-loop accumulates
// stop fields with >0 guards, so the two events compose (StopReason from one,
// token usage from the other) without clobbering.
func mapStreamEvent(ev types.ConverseStreamOutput) (llm.StreamEvent, bool) {
	switch e := ev.(type) {
	case *types.ConverseStreamOutputMemberMessageStart:
		return llm.StreamEvent{Type: llm.EventMessageStart}, true
	case *types.ConverseStreamOutputMemberContentBlockStart:
		if tu, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
			return llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: aws.ToString(tu.Value.ToolUseId),
				ToolName:  aws.ToString(tu.Value.Name),
			}, true
		}
		return llm.StreamEvent{}, false
	case *types.ConverseStreamOutputMemberContentBlockDelta:
		switch d := e.Value.Delta.(type) {
		case *types.ContentBlockDeltaMemberText:
			return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: d.Value}, true
		case *types.ContentBlockDeltaMemberToolUse:
			return llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: aws.ToString(d.Value.Input)}, true
		}
		return llm.StreamEvent{}, false
	case *types.ConverseStreamOutputMemberContentBlockStop:
		return llm.StreamEvent{Type: llm.EventToolUseStop}, true
	case *types.ConverseStreamOutputMemberMessageStop:
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: string(e.Value.StopReason)}, true
	case *types.ConverseStreamOutputMemberMetadata:
		se := llm.StreamEvent{Type: llm.EventMessageStop}
		if e.Value.Usage != nil {
			se.InputTokens = int(aws.ToInt32(e.Value.Usage.InputTokens))
			se.OutputTokens = int(aws.ToInt32(e.Value.Usage.OutputTokens))
		}
		return se, true
	}
	return llm.StreamEvent{}, false
}

// streamReader pulls SDK Converse stream events off the channel and runs each
// through mapStreamEvent. Pull-based, no background goroutine.
type streamReader struct {
	es     *bedrockruntime.ConverseStreamEventStream
	events <-chan types.ConverseStreamOutput
	queued []llm.StreamEvent
}

func newStreamReader(es *bedrockruntime.ConverseStreamEventStream) *streamReader {
	return &streamReader{es: es, events: es.Events()}
}

func (r *streamReader) Next() (llm.StreamEvent, bool, error) {
	for len(r.queued) == 0 {
		ev, ok := <-r.events
		if !ok {
			if err := r.es.Err(); err != nil {
				return llm.StreamEvent{}, false, err
			}
			return llm.StreamEvent{}, false, nil
		}
		if se, emit := mapStreamEvent(ev); emit {
			r.queued = append(r.queued, se)
		}
	}
	se := r.queued[0]
	r.queued = r.queued[1:]
	return se, true, nil
}

func (r *streamReader) Close() error { return r.es.Close() }
```

In `source/server/internal/llm/bedrock/client.go`, replace the placeholder `StreamChat`:

```go
// StreamChat opens a Converse stream and returns an llm.StreamReader.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	msgs, err := messagesToConverse(ctx, req.Messages)
	if err != nil {
		return nil, err
	}
	out, err := c.api.ConverseStream(ctx, &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(modelOr(c.model, req.Model)),
		Messages:        msgs,
		System:          systemBlocks(req.System),
		ToolConfig:      toolsToConverse(req.Tools),
		InferenceConfig: inferenceConfig(req),
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock: converse stream: %w", err)
	}
	return newStreamReader(out.GetStream()), nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./internal/llm/bedrock/ -count=1`
Expected: PASS (adapter + client + stream mapper tests). The streamReader/StreamChat wiring is exercised by the gated integration test in Task 5.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/bedrock/stream.go source/server/internal/llm/bedrock/stream_test.go source/server/internal/llm/bedrock/client.go
git commit -m "feat(bedrock): Converse stream mapper + StreamChat"
```

---

### Task 4: Config fields + factory + keyless-guard exemption

Wire the `bedrock` flavor end-to-end: add `region`/`aws_profile` to the profile, fill the factory case, and exempt bedrock from the keyless→absent guard.

**Files:**
- Modify: `source/server/pkg/config/config.go` (CloudProfile fields)
- Modify: `source/server/pkg/config/config_test.go` (append)
- Modify: `source/server/internal/cloudfactory/factory.go` (bedrock case + import)
- Modify: `source/server/internal/cloudfactory/factory_test.go` (append)
- Modify: `source/server/internal/server/server.go:270` (guard exemption)
- Modify: `source/server/internal/server/cloud_profiles_test.go` (append a guard-exemption test)

**Interfaces:**
- Consumes: `bedrock.NewClient`/`bedrock.Config` (Task 2); `FlavorBedrock` (already defined, `factory.go:20`).

- [ ] **Step 1: Write the failing tests**

Append to `source/server/pkg/config/config_test.go`:

```go
func TestCloudProfileBedrockYAML(t *testing.T) {
	var p CloudProfile
	y := "name: b\nflavor: bedrock\nregion: us-east-1\naws_profile: sso\nmodel: anthropic.claude-x\n"
	if err := yaml.Unmarshal([]byte(y), &p); err != nil {
		t.Fatal(err)
	}
	if p.Region != "us-east-1" || p.AWSProfile != "sso" {
		t.Errorf("region=%q aws_profile=%q", p.Region, p.AWSProfile)
	}
}
```

(If `config_test.go` lacks the yaml import, add `"gopkg.in/yaml.v3"`.)

Append to `source/server/internal/cloudfactory/factory_test.go`:

```go
func TestBuildBedrockProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "b", Flavor: "bedrock", Region: "us-east-1", Model: "anthropic.claude-x"}, "")
	if err != nil || p == nil || p.Name() != "bedrock" {
		t.Fatalf("bedrock → %v, %v", p, err)
	}
}

func TestBuildBedrockMissingRegion(t *testing.T) {
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "b", Flavor: "bedrock", Model: "m"}, ""); err == nil {
		t.Error("bedrock without a region should error")
	}
}
```

Append to `source/server/internal/server/cloud_profiles_test.go` (the keyless guard must let a region-bearing bedrock profile through rather than going absent):

```go
func TestSetActiveCloudProfileBedrockKeylessOk(t *testing.T) {
	s, r := newTestServer()
	s.currentConfig.CloudProfiles = append(s.currentConfig.CloudProfiles,
		config.CloudProfile{Name: "bedrock-one", Flavor: "bedrock", Region: "us-east-1", Model: "anthropic.claude-x"})
	// No key set for bedrock-one — the keyless guard must NOT send it to absent.
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "bedrock-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true for keyless bedrock (creds via AWS chain), got false: %s", resp.Error)
	}
	if _, absent := r.last.(*legacymodels.AbsentCloudProvider); absent {
		t.Error("keyless bedrock should NOT install the absent provider")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./pkg/config/ ./internal/cloudfactory/ ./internal/server/ -run 'Bedrock' -count=1`
Expected: FAIL — `unknown field Region` / factory returns "not yet supported" / keyless bedrock goes absent (Ok=false).

- [ ] **Step 3: Implement**

In `source/server/pkg/config/config.go`, add to `CloudProfile` (after `Backend`):

```go
	Region     string `yaml:"region,omitempty"`      // bedrock: AWS region (required)
	AWSProfile string `yaml:"aws_profile,omitempty"` // bedrock: optional ~/.aws named profile
```

In `source/server/internal/cloudfactory/factory.go`, add the import:

```go
	"cercano/source/server/internal/llm/bedrock"
```

And the case (after `FlavorResponses`):

```go
	case FlavorBedrock:
		return bedrock.NewClient(bedrock.Config{
			Region: p.Region, Model: p.Model, AWSProfile: p.AWSProfile, BaseURL: p.BaseURL,
		})
```

In `source/server/internal/server/server.go`, update the keyless guard at line 270 — exempt bedrock (it authenticates via the AWS chain, not a keychain key):

```go
	// If neither a key nor a proxy BaseURL is present the profile cannot
	// authenticate — install the absent sentinel rather than wiring a dead
	// provider. Carve-outs: a proxy BaseURL (Meridian) handles auth with an
	// empty key; and bedrock authenticates via the AWS credential chain, so it
	// legitimately has no keychain key (its failure mode is a missing region).
	if key == "" && p.BaseURL == "" && p.Flavor != cloudfactory.FlavorBedrock {
		s.installAbsentCloud("no API key for profile " + p.Name)
		return fmt.Errorf("no API key for profile %s", p.Name)
	}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./pkg/config/ ./internal/cloudfactory/ ./internal/server/ -count=1`
Expected: PASS (new + existing tests in all three packages).

- [ ] **Step 5: Commit**

```bash
git add source/server/pkg/config/config.go source/server/pkg/config/config_test.go source/server/internal/cloudfactory/factory.go source/server/internal/cloudfactory/factory_test.go source/server/internal/server/server.go source/server/internal/server/cloud_profiles_test.go
git commit -m "feat(bedrock): config region/aws_profile, factory wiring, keyless-guard exemption"
```

---

### Task 5: Gated integration tests + flip spec status

Live tests behind `INTEGRATION_TEST=1` (+ AWS creds + region/model) proving real Chat, streaming, and a tool call; mark the design implemented.

**Files:**
- Create: `source/server/internal/llm/bedrock/client_integration_test.go`
- Modify: `docs/agent/cloud-bedrock.md` (status line)

**Interfaces:**
- Consumes: `NewClient`, `Chat`, `StreamChat`.

- [ ] **Step 1: Write the gated tests**

Create `source/server/internal/llm/bedrock/client_integration_test.go`:

```go
package bedrock

// Live integration tests against real Amazon Bedrock. Skipped unless
// INTEGRATION_TEST=1, AWS credentials are resolvable, and BEDROCK_REGION is set.
// Model defaults to anthropic.claude-3-5-sonnet-20240620-v1:0; override with
// BEDROCK_MODEL.
//
//   INTEGRATION_TEST=1 BEDROCK_REGION=us-east-1 \
//     go test ./internal/llm/bedrock/ -run Integration -v

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
		t.Skip("set INTEGRATION_TEST=1 to run live Bedrock tests")
	}
	region := os.Getenv("BEDROCK_REGION")
	if region == "" {
		t.Skip("BEDROCK_REGION not set")
	}
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		model = "anthropic.claude-3-5-sonnet-20240620-v1:0"
	}
	c, err := NewClient(Config{Region: region, Model: model})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestIntegration_Chat(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.Chat(ctx, llm.ChatRequest{
		System:    "You are terse. Reply with exactly one word.",
		MaxTokens: 64,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
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
		MaxTokens: 64,
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Count 1 to 5, space separated."}}}},
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
		MaxTokens: 256,
		Tools:     []llm.Tool{weather},
		Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Call the tool."}}}},
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
```

- [ ] **Step 2: Verify gated tests compile + skip without the gate**

Run: `cd source/server && go test ./internal/llm/bedrock/ -run Integration -v -count=1`
Expected: PASS via SKIP ("set INTEGRATION_TEST=1 …"). Confirms compilation + gating.

- [ ] **Step 3: Flip the spec status**

In `docs/agent/cloud-bedrock.md`, change:

```markdown
**Status:** Implemented 2026-06-29. Sub-project 4 (final) of the multi-cloud effort
```

- [ ] **Step 4: Run the full server suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS. (Pre-existing flaky `TestPendingCarriesPersist` excepted — if it alone fails, confirm it's unrelated before proceeding.)

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/llm/bedrock/client_integration_test.go docs/agent/cloud-bedrock.md
git commit -m "test(bedrock): gated live tests (chat/stream/tools); mark SP4 implemented"
```

---

## Self-Review

**1. Spec coverage** (against `cloud-bedrock.md`):
- Core decisions: AWS SDK v2 + Converse (T1-3), default credential chain (T2 NewClient), stateless (whole design). ✅
- §1 package structure (adapter/client/stream) → T1-3; `NewClient` returns error → T2. ✅
- §2 config & credentials (Region/AWSProfile, LoadDefaultConfig, keyless-guard exemption, factory ignores apiKey, missing-region error) → T2, T4. ✅
- §3 translation (system/text/image-as-bytes/toolUse/toolResult, tool config, inference config, output→blocks, document helpers) → T1. ✅
- §4 streaming (pure mapper + reader, vocabulary mapping) → T3. ✅
- §5 capabilities + factory → T2 (caps), T4 (factory). ✅
- §6 testing (adapter, stream mapper, client via fake, factory, gated integration) → T1-5. ✅
- Future-improvements / out-of-scope (reasoning, InvokeModel, caching, guardrails, image gen, CLI/proto surfacing) → none implemented. ✅

**2. Placeholder scan:** No TBD/TODO. Every code step has complete code; every run step states expected output. Task 2's placeholder `StreamChat` is intentional and replaced in Task 3 (flagged in both). ✅

**3. Type consistency:** `bedrock.Config{Region,Model,AWSProfile,BaseURL}` and `converseAPI` (T2) used by the factory (T4) and stream (T3). Adapter signatures (`messagesToConverse(ctx,…)`, `blocksFromConverse`, `jsonToDocument`/`documentToJSON`, `systemBlocks`, `toolsToConverse`, `inferenceConfig`, `imageFormat`) from T1 match their call sites in `Chat`/`StreamChat` (T2/T3). `mapStreamEvent` signature (T3) matches its test and the reader. `Client{api,model}` fields used by tests in T2. `CloudProfile.Region`/`AWSProfile` names consistent across config, factory, and the bedrock config. The verified `MarshalSmithyDocument` pattern (not `UnmarshalSmithyDocument`) is used in `documentToJSON`. ✅
