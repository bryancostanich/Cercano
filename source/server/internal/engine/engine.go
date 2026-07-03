package engine

import (
	"context"
	"encoding/json"
	"time"
)

// ModelInfo represents a model available on the InferenceEngine.
type ModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// CompletionResult holds the output of a text generation call along with token usage.
type CompletionResult struct {
	Output       string
	InputTokens  int
	OutputTokens int
}

// ChatMessage is one message in a chat conversation.
// Tool-use turns set ToolCalls (assistant) or ToolCallID + Content (tool result).
type ChatMessage struct {
	Role       string     `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // role=tool only
	Name       string     `json:"name,omitempty"`         // role=tool: tool name
}

// ToolCall is a single tool invocation requested by the assistant.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc is the function payload of a ToolCall.
type ToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolSchemaJSON is the on-the-wire format Ollama expects in the chat /tools field.
type ToolSchemaJSON struct {
	Type     string           `json:"type"` // always "function"
	Function ToolFunctionJSON `json:"function"`
}

// ToolFunctionJSON is the function descriptor inside a ToolSchemaJSON.
type ToolFunctionJSON struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is the input to ChatWithTools.
type ChatRequest struct {
	Model    string           `json:"model"`
	Messages []ChatMessage    `json:"messages"`
	Tools    []ToolSchemaJSON `json:"tools,omitempty"`
}

// ChatResponse is the output of ChatWithTools.
// If ToolCalls is non-empty, the assistant wants the loop to run them and re-call.
// Otherwise Content is the final response.
type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	InputTokens  int
	OutputTokens int
}

// InferenceEngine defines the interface for local text generation backends.
// GenOptions carries per-call sampling parameters for a completion.
type GenOptions struct {
	// Temperature is the sampling temperature. Nil uses the engine's default;
	// a pointer to 0 requests greedy decoding (deterministic output).
	Temperature *float64
}

// Greedy returns GenOptions pinned to temperature 0 (deterministic decoding).
func Greedy() GenOptions {
	t := 0.0
	return GenOptions{Temperature: &t}
}

type InferenceEngine interface {
	Complete(ctx context.Context, model, prompt, systemPrompt string, opts GenOptions) (CompletionResult, error)
	CompleteStream(ctx context.Context, model, prompt, systemPrompt string, opts GenOptions, onToken func(string)) (CompletionResult, error)
	ChatWithTools(ctx context.Context, req ChatRequest) (ChatResponse, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Name() string
}

// EmbeddingService defines the interface for generating semantic embeddings.
type EmbeddingService interface {
	Embed(ctx context.Context, model, text string) ([]float64, error)
	Name() string
}

// ConfigurableEngine defines the interface for engines that support dynamic endpoint configuration and health monitoring.
type ConfigurableEngine interface {
	SetBaseURL(url string)
	GetActiveURL() string
	IsUsingFallback() bool
	StartHealthMonitor(ctx context.Context, interval time.Duration, failureThreshold int)
}
