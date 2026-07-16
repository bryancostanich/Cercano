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
	Model      string
	System     string
	Messages   []Message
	Tools      []Tool
	ToolChoice ToolChoice
	MaxTokens  int
	// Temperature is a pointer so 0 ("greedy decoding") is distinguishable
	// from unset (nil = the provider's default). Greedy matters: the
	// compaction summarizer requires it for reproducible, format-conforming
	// output (see compaction-bakeoff-findings.md).
	Temperature *float64
}

type ChatResponse struct {
	Blocks       []Block
	StopReason   string
	InputTokens  int
	OutputTokens int
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)
}
