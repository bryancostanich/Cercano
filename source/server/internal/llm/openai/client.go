package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Config holds the OpenAI client configuration.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Backend string // selects per-backend quirks; empty → defensive default
	// SupportsVision reports whether the model behind this endpoint can accept
	// image content. Cloud OpenAI passes true; the LOCAL llama-server backend
	// passes the active model's real capability (a text-only GGUF, or a vision
	// GGUF launched without its mmproj, cannot see images and would 500 on one).
	// When false, image blocks are stripped to a text stub before sending.
	SupportsVision bool
}

// Client implements inference.Provider using the OpenAI chat completions API.
type Client struct {
	api            *goopenai.Client
	model          string
	backend        string
	quirks         Quirks
	supportsVision bool
}

// NewClient constructs a Client from cfg. The HTTP transport is wrapped in a
// normalizingDoer so per-backend response quirks (array-shaped error bodies) are
// repaired before go-openai parses them. There is no transport-level retry:
// retry policy is owned by the resilience engine above this adapter.
func NewClient(cfg Config) *Client {
	c := goopenai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		c.BaseURL = cfg.BaseURL
	}
	q := quirksFor(cfg.Backend)
	c.HTTPClient = &normalizingDoer{next: &http.Client{}, quirks: q}
	return &Client{api: goopenai.NewClientWithConfig(c), model: cfg.Model, backend: cfg.Backend, quirks: q, supportsVision: cfg.SupportsVision}
}

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

// visionStub is the placeholder that replaces image blocks bound for a model
// with no vision support. It matches the phrasing agent/toolloop.go uses for
// tool-result images so the two paths degrade identically.
func visionStub(n int) string {
	return fmt.Sprintf("[%d image(s) omitted: the active model has no vision support]", n)
}

// stripImagesForTextOnly replaces every image block with a text stub when the
// target model can't see images, so a text-only (or mmproj-less) local backend
// receives a coherent, image-free request instead of 500ing on "image input is
// not supported". Returns the rewritten messages and the number of images
// stripped (0 means the caller can skip any user notice). The caller's slice is
// not mutated (copy-on-write per message), matching resolveImageURLs.
func stripImagesForTextOnly(msgs []llm.Message) ([]llm.Message, int) {
	stripped := 0
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		imgCount := 0
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage {
				imgCount++
			}
		}
		if imgCount == 0 {
			continue
		}
		blocks := make([]llm.Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage {
				blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: visionStub(1)})
				stripped++
				continue
			}
			blocks = append(blocks, b)
		}
		out[i].Blocks = blocks
	}
	return out, stripped
}

func (c *Client) Name() string { return "openai" }

func (c *Client) Capabilities() inference.Capabilities {
	return inference.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       false,
		SupportsVision:        c.supportsVision,
	}
}

const explicitZeroTemperatureSentinel = -999999.0

func (c *Client) requestContext(ctx context.Context, req llm.ChatRequest) context.Context {
	if req.ConversationID != "" {
		ctx = context.WithValue(ctx, diagnosticConversationIDKey{}, req.ConversationID)
	}
	if req.RequestID != "" {
		ctx = context.WithValue(ctx, diagnosticRequestIDKey{}, req.RequestID)
	}
	return ctx
}

func (c *Client) buildRequest(req llm.ChatRequest, stream bool) goopenai.ChatCompletionRequest {
	r := goopenai.ChatCompletionRequest{
		Model:    modelOr(c.model, req.Model),
		Messages: messagesToOpenAI(req.Messages, req.System),
		Tools:    toolsToOpenAI(req.Tools),
		Stream:   stream,
	}
	if stream {
		// Request usage on the final chunk so InputTokens/OutputTokens are
		// available for EventMessageStop.
		r.StreamOptions = &goopenai.StreamOptions{IncludeUsage: true}
	}
	if req.MaxTokens > 0 {
		r.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		if *req.Temperature == 0 {
			// go-openai models Temperature as a non-pointer float32 with
			// omitempty, so assigning 0 would be omitted and local tool use
			// would fall back to the runtime's high/default sampling. Encode a
			// private sentinel and let normalizingDoer rewrite it to literal 0
			// just before the request leaves this process.
			r.Temperature = explicitZeroTemperatureSentinel
		} else {
			r.Temperature = float32(*req.Temperature)
		}
	}
	return r
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

func logRequestDiagnostics(req llm.ChatRequest, wire goopenai.ChatCompletionRequest, backend string, stream bool) {
	body, err := json.Marshal(wire)
	bodyBytes := 0
	if err == nil {
		bodyBytes = len(body)
	}
	toolsBytes := 0
	if b, err := json.Marshal(wire.Tools); err == nil {
		toolsBytes = len(b)
	}
	messagesBytes := 0
	if b, err := json.Marshal(wire.Messages); err == nil {
		messagesBytes = len(b)
	}
	// This is a cheap upper-bound-ish diagnostic for postmortems, not billing
	// accounting. Provider usage on successful calls remains authoritative.
	approxPromptTokens := (messagesBytes + toolsBytes + 3) / 4
	log.Printf("[openai] request diagnostics: conv=%s request_id=%s backend=%s model=%s stream=%t messages=%d message_bytes=%d tools=%d tool_schema_bytes=%d body_bytes=%d approx_prompt_tokens=%d max_tokens=%d temperature_set=%t",
		req.ConversationID, req.RequestID, backend, wire.Model, stream, len(wire.Messages), messagesBytes, len(wire.Tools), toolsBytes, bodyBytes, approxPromptTokens, wire.MaxTokens, req.Temperature != nil)
}

// Chat sends a non-streaming chat completion request and returns mapped blocks + usage.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if !c.supportsVision {
		req.Messages, _ = stripImagesForTextOnly(req.Messages)
	}
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		req.Messages = msgs
	}
	wire := c.buildRequest(req, false)
	logRequestDiagnostics(req, wire, c.backend, false)
	resp, err := c.api.CreateChatCompletion(c.requestContext(ctx, req), wire)
	if err != nil {
		log.Printf("[openai] request failed: conv=%s request_id=%s backend=%s model=%s stream=false error=%v", req.ConversationID, req.RequestID, c.backend, wire.Model, err)
		return llm.ChatResponse{}, c.normalize(err)
	}
	out := llm.ChatResponse{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		Model:        resp.Model,
	}
	if len(resp.Choices) > 0 {
		out.Blocks = blocksFromOpenAI(resp.Choices[0].Message)
		out.StopReason = string(resp.Choices[0].FinishReason)
	}
	return out, nil
}

// StreamChat opens a streaming chat completion and returns a StreamReader that
// emits llm.StreamEvents following the START→DELTA→STOP contract.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if !c.supportsVision {
		req.Messages, _ = stripImagesForTextOnly(req.Messages)
	}
	if c.quirks.ImagesAsBase64 {
		msgs, err := resolveImageURLs(ctx, req.Messages)
		if err != nil {
			return nil, err
		}
		req.Messages = msgs
	}
	wire := c.buildRequest(req, true)
	logRequestDiagnostics(req, wire, c.backend, true)
	stream, err := c.api.CreateChatCompletionStream(c.requestContext(ctx, req), wire)
	if err != nil {
		log.Printf("[openai] request failed: conv=%s request_id=%s backend=%s model=%s stream=true error=%v", req.ConversationID, req.RequestID, c.backend, wire.Model, err)
		return nil, c.normalize(err)
	}
	r := newStreamReader(stream)
	r.normalize = c.normalize
	return r, nil
}
