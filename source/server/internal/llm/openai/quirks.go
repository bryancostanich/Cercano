package openai

// Quirks captures a backend's known deviations from OpenAI Chat Completions.
// Transient-failure retries are deliberately NOT a quirk: retry policy is
// owned by the resilience engine above the adapters, where it is bounded,
// class-driven, and narrated to the user.
type Quirks struct {
	ImagesAsBase64  bool
	NormalizeErrors bool
}

// quirksFor resolves a backend name (the profile's `backend` field) to its
// Quirks. Unknown/empty backends get the defensive default — base64 images
// and error normalization — both harmless when unneeded. `openai` is the one
// backend that opts out of base64 (it fetches image URLs server-side).
func quirksFor(backend string) Quirks {
	switch backend {
	case "openai":
		return Quirks{ImagesAsBase64: false, NormalizeErrors: true}
	default: // gemini, groq, "" or unrecognized
		return Quirks{ImagesAsBase64: true, NormalizeErrors: true}
	}
}
