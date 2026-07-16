package llm

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
	// Tier is the capability-tier name (a config.Tier value, e.g.
	// "fast_light_text") the pinned Model was resolved FROM — routing
	// metadata, never sent on the wire. It exists so the failover composite
	// can re-resolve the same tier in the backup vendor's namespace instead
	// of degrading to the backup's default model (experience-preserving
	// failover; see docs/agent/cloud-failover-audit.md). Empty means "the
	// provider's default model" and needs no translation.
	Tier string
}

type ChatResponse struct {
	Blocks       []Block
	StopReason   string
	InputTokens  int
	OutputTokens int
	// Model is the model that actually served the call, from the provider's
	// response envelope (empty when the envelope doesn't carry one). On a
	// failed-over call this names the BACKUP's model — per-request records
	// must not claim the requested model served it.
	Model string
}

// The provider seam moved to internal/inference (inference.Provider) with
// methods Infer / Stream. Capabilities, ChatRequest, and ChatResponse remain
// here as the chat envelope/vocabulary the seam speaks (inference aliases them
// as Call / Result). context is still imported by nothing here now — see below.
