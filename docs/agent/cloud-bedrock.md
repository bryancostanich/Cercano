# Amazon Bedrock Provider — Design

**Status:** Designed 2026-06-29. Sub-project 4 (final) of the multi-cloud effort
(foundation = [cloud-profiles.md](./cloud-profiles.md); SP2 = [cloud-openai.md](./cloud-openai.md);
SP3 = [cloud-responses.md](./cloud-responses.md)).

Add an `llm.Provider` for **Amazon Bedrock**, wired in via the `bedrock` flavor the
foundation reserved. Parity with the other backends: text, tool calling, vision
(image input), and streaming.

## Core decisions

- **AWS SDK for Go v2 + the Converse API.** Use `aws-sdk-go-v2`'s
  `service/bedrockruntime` client and its `Converse` / `ConverseStream`
  operations. The SDK handles SigV4 request signing, the AWS credential chain, and
  retries — none of which we want to hand-roll (unlike a bearer token, AWS auth is
  genuinely hard to get right). Converse is the **unified** message API: its
  content-block model (text / image / toolUse / toolResult, plus system blocks and
  a tool config) maps almost 1:1 onto our `llm.Block` model and works across model
  families (Claude, Llama, Mistral, Nova, …). This is the one provider where we
  adopt a heavy SDK rather than hand-roll; it's behind `llm.Provider`, so nothing
  upstream sees AWS.
- **Credentials via the AWS default chain.** `NewClient` uses
  `config.LoadDefaultConfig` so credentials resolve the standard AWS way — env
  vars, `~/.aws/credentials` + `config` profiles, SSO, and IAM roles. No AWS secret
  is stored in Cercano's keychain. The profile selects **region** (required) and,
  optionally, a named `~/.aws` profile.
- **Stateless, like every other provider.** Each turn sends the full conversation;
  no server-side session state. The provider is a pure function of its inputs
  behind `llm.Provider`.

## 1. Package structure

`internal/llm/bedrock/` (mirrors the other provider packages):
- `client.go` — `Config{Region, Model, AWSProfile, BaseURL}`;
  `NewClient(Config) (*Client, error)`; `Name() → "bedrock"`; `Capabilities()`;
  `Chat(ctx, llm.ChatRequest) (llm.ChatResponse, error)`;
  `StreamChat(ctx, llm.ChatRequest) (llm.StreamReader, error)`.
- `adapter.go` — pure translation, `llm` ↔ Bedrock Converse `types`.
- `stream.go` — a `streamReader` over `ConverseStream`'s event stream, plus a
  **pure** event-translation function (see §4).

The provider implements the existing `llm.Provider` interface verbatim
(`provider.go:30`), so the tool-loop, router, and coordinator consume it
identically to anthropic/openai/responses/ollama.

`NewClient` returns an **error** (unlike the other providers' `NewClient`) because
`config.LoadDefaultConfig` can fail (bad region, unresolvable credentials). The
factory propagates it (it already returns `(llm.Provider, error)`).

## 2. Config & credentials

`CloudProfile` (`pkg/config/config.go`) gains:

```go
Region     string `yaml:"region,omitempty"`      // bedrock: AWS region (required), e.g. "us-east-1"
AWSProfile string `yaml:"aws_profile,omitempty"` // bedrock: optional ~/.aws named profile
```

`Model` carries the Bedrock model id or inference-profile id (e.g.
`anthropic.claude-3-5-sonnet-20240620-v1:0`, or a cross-region inference profile
like `us.anthropic.claude-3-5-sonnet-20240620-v1:0`). `BaseURL` stays an optional
endpoint override (VPC/private endpoints); empty → the SDK's default for the region.

```yaml
cloud_profiles:
  - name: bedrock-claude
    flavor: bedrock
    region: us-east-1
    model: anthropic.claude-3-5-sonnet-20240620-v1:0
    # aws_profile: my-sso-profile   # optional
```

`NewClient` builds the SDK config:

```
cfg, err := config.LoadDefaultConfig(ctx,
    config.WithRegion(c.Region),
    // config.WithSharedConfigProfile(c.AWSProfile) when AWSProfile != "")
)
// bedrockruntime.NewFromConfig(cfg, optional WithEndpoint(BaseURL))
```

**Wiring consequences (both must be handled):**
- `cloudfactory.BuildCloudProvider(p, apiKey)` ignores `apiKey` for bedrock and
  passes `p.Region`/`p.Model`/`p.AWSProfile` into `bedrock.Config`. A missing
  `Region` is the bedrock build-failure mode (clear error → goes absent).
- SP1's **keyless-profile guard** in the server (which sends a profile with no
  keychain key to the absent provider) must **exempt** the `bedrock` flavor: a
  bedrock profile legitimately has no keychain secret. The guard becomes "keyless
  AND not bedrock AND no base_url → absent" (verify the exact condition in
  `server.go` during implementation and add the bedrock exception).

## 3. Translation (`adapter.go`)

The project's message model (`llm.Message{Role, Blocks[]}`) maps onto Converse
`types`:

**Outbound — `llm.ChatRequest` → `ConverseInput` parts:**
- `System` → `[]types.SystemContentBlock` (a single text block when non-empty).
- `Messages` → `[]types.Message` (role user/assistant), each with content blocks:
  - `BlockText` → `types.ContentBlockMemberText`.
  - `BlockImage` → `types.ContentBlockMemberImage` carrying **raw bytes** +
    format. Converse takes image bytes (not a URL or base64 string), so resolve
    both `ImageData` and `ImageURL` to bytes with the existing
    `llm.ResolveImageBytes(ctx, block)`; map `MediaType` (or a sniff) → the
    Converse image format (`png`/`jpeg`/`gif`/`webp`).
  - `BlockToolUse{ToolUseID, ToolName, ToolInput}` → `types.ContentBlockMemberToolUse`
    (`toolUseId`, `name`, `input` as a document from the JSON).
  - `BlockToolResult{ToolUseRef, Content, IsError}` → `types.ContentBlockMemberToolResult`
    (`toolUseId`, content text block, `status` success/error).
- `Tools` → `types.ToolConfiguration` of `types.ToolMemberToolSpec`
  (`name`, `description`, `inputSchema` = the JSON schema as a document).
  `ToolChoice` → Converse `toolChoice` when set (auto/any/tool); default omit.
- `MaxTokens`/`Temperature` → `types.InferenceConfiguration`
  (`maxTokens`/`temperature`) when set.

**Inbound — `ConverseOutput` → `[]llm.Block`:** the output `message.content` blocks
map back — text → `BlockText`; `toolUse` → `BlockToolUse{ToolUseID:toolUseId,
ToolName:name, ToolInput:<json of input>}`. `stopReason` → `StopReason`;
`usage` (`inputTokens`/`outputTokens`) → token counts.

Converse documents (`document.Interface` / `smithy` document) carry tool
input/output as structured JSON; the adapter converts between those and our
`json.RawMessage` tool fields.

## 4. Streaming (`stream.go`)

`ConverseStream` returns an event stream of a typed union
(`types.ConverseStreamOutput`): `MessageStart`, `ContentBlockStart` (tool use
begins), `ContentBlockDelta` (text or tool-input fragments), `ContentBlockStop`,
`MessageStop`, and `Metadata` (carries `usage`).

- The **event → `llm.StreamEvent` mapping is a pure function** over the SDK's event
  union values, so it's unit-testable without a live AWS stream (construct the SDK
  event structs in tests and assert the emitted `llm.StreamEvent`). The
  `streamReader` is a thin loop that pulls SDK events off the channel and runs them
  through this mapper.
- Mapping onto the existing vocabulary (`stream.go:8-14`): `MessageStart` →
  `EventMessageStart`; text `ContentBlockDelta` → `EventTextDelta`;
  `ContentBlockStart`(toolUse) → `EventToolUseStart` (ToolUseID, ToolName);
  tool-input `ContentBlockDelta` → `EventToolUseInputDelta` (fragment in TextDelta);
  `ContentBlockStop` → `EventToolUseStop` (when a tool block was open);
  `MessageStop` → `EventMessageStop`; `Metadata.usage` → token counts on the stop
  event; an error event / stream error → `EventError`. Tool-call arguments stream
  as fragments and are reassembled by the existing tool-loop exactly as for the
  other providers.

## 5. Capabilities + factory

`Capabilities{SupportsTools:true, SupportsParallelTools:true, SupportsVision:true,
SupportsCaching:false}` (Bedrock prompt caching is model-specific; off for now).

In `cloudfactory.BuildCloudProvider`, fill the reserved case:

```go
case FlavorBedrock:
    return bedrock.NewClient(bedrock.Config{
        Region: p.Region, Model: p.Model, AWSProfile: p.AWSProfile, BaseURL: p.BaseURL,
    })
```

## 6. Testing

- **adapter** round-trips (table tests, no network): system → system block; user
  text; text+image (URL and base64 → resolved bytes + format); single and parallel
  `toolUse`; `toolResult` (success and error); tools → tool config; inference
  config from MaxTokens/Temperature; `ConverseOutput` → blocks + stopReason + usage.
- **stream** mapping: feed synthetic `types.ConverseStreamOutput` values to the
  pure mapper — text deltas → `EventTextDelta`; tool-use start + input deltas
  reassemble; `MessageStop` + `Metadata` → `EventMessageStop` with usage; an error
  → `EventError`.
- **client**: a thin seam over the SDK Converse call mocked via an interface (the
  client depends on a small `converseAPI` interface it defines, so tests inject a
  fake returning a canned `ConverseOutput`), asserting the response maps to blocks.
- **factory**: `bedrock` flavor with a region → a `*bedrock.Client` named
  `"bedrock"`; a bedrock profile missing a region → a clear error.
- **integration (gated, `INTEGRATION_TEST=1` + AWS creds + `BEDROCK_REGION`/`BEDROCK_MODEL`):**
  live `Chat`, streamed `Chat`, and a tool call against a real Bedrock model.

## Future improvements (deferred — captured for follow-on work)

These are intentionally out of scope for SP4's parity baseline. Each is a clean
follow-on once parity ships:

- **Extended thinking / reasoning.** Claude (and some others) on Bedrock expose
  reasoning via Converse `reasoningContent` (a `reasoningText` + `signature`, or
  `redactedContent`) — a *different* mechanism from SP3's encrypted-blob carry.
  A follow-on would map `reasoningContent` to the existing `llm.BlockReasoning`
  (storing the signature/redacted bytes) and request it via Converse's
  `additionalModelRequestFields` (e.g. `reasoning_config`), reusing the
  `EventReasoning` stream event added in SP3. Hooks already exist in the llm core.
- **Prompt caching.** Bedrock supports cache points (`cachePoint` content blocks)
  on supported models; flip `SupportsCaching` and emit cache points when we add
  caching across providers.
- **`InvokeModel` fallback.** For models or features the Converse API doesn't
  cover, an `InvokeModel` path with per-model request/response shapes.
- **Bedrock Guardrails.** `guardrailConfig` on the request for content filtering.
- **Application inference profiles / provisioned throughput.** First-class config
  for ARNs beyond a plain model id (the `Model` field already accepts an inference
  profile id; richer handling is deferred).
- **Image generation / embeddings** via Bedrock (Titan/Nova) — out of the chat
  provider's scope.

## Out of scope (this sub-project)

The Future-improvements items above, plus: no changes to the other providers; no
new keychain UX (bedrock uses the AWS chain); the CLI/proto surfacing of the new
`region`/`aws_profile` fields is YAML-only for now (same posture as the SP2
`backend` field).
