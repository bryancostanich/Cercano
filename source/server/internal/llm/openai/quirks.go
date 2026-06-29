package openai

import "time"

// RetryPolicy controls transient-failure retries in the transport wrapper.
type RetryPolicy struct {
	MaxAttempts int           // total attempts incl. the first; <2 disables retry
	BaseDelay   time.Duration // first backoff; doubles each subsequent attempt
	OnStatus    []int         // HTTP statuses that trigger a retry
}

// Quirks captures a backend's known deviations from OpenAI Chat Completions.
// The zero value is the strict-OpenAI baseline; quirksFor turns on the
// defensive options that are safe everywhere.
type Quirks struct {
	ImagesAsBase64  bool // resolve URL images to base64 before send
	NormalizeErrors bool // rewrite array-shaped error bodies to object shape
	Retry           RetryPolicy
}

// defaultRetry is the transient-failure policy shared by all known backends.
func defaultRetry() RetryPolicy {
	return RetryPolicy{
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
