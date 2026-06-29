package openai

import (
	"time"

	"cercano/source/server/internal/llm/httpx"
)

// Quirks captures a backend's known deviations from OpenAI Chat Completions.
type Quirks struct {
	ImagesAsBase64  bool
	NormalizeErrors bool
	Retry           httpx.RetryPolicy
}

func defaultRetry() httpx.RetryPolicy {
	return httpx.RetryPolicy{
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
