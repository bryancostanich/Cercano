package engine

import (
	"context"
	"time"
)

// ModelInfo represents a model available on the InferenceEngine.
type ModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// InferenceEngine defines the interface for local text generation backends.
type InferenceEngine interface {
	Complete(ctx context.Context, model, prompt, systemPrompt string) (string, error)
	CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (string, error)
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
