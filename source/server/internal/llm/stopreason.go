package llm

// Canonical stop/finish reasons as they arrive from the adapters. Each provider
// reports truncation-at-the-output-cap under one of these strings:
//
//	OpenAI / llama-server / ollama : "length"     (choices[].finish_reason / done_reason)
//	Anthropic / Bedrock            : "max_tokens" (stop_reason / StopReason)
//
// The adapters copy these through verbatim into ChatResponse.StopReason and
// StreamEvent.StopReason, so this package can classify them provider-agnostically.
const (
	StopReasonLength    = "length"
	StopReasonMaxTokens = "max_tokens"
)

// IsLengthTruncation reports whether a stop/finish reason indicates the model's
// output was cut off because it reached the per-turn output-token cap (as opposed
// to finishing naturally, emitting a tool call, or stopping on a stop sequence).
//
// A truncated response is the signature of "the model tried to emit more than the
// output budget allowed" — e.g. a Write tool call whose arguments were sliced off
// mid-JSON. Callers use this to distinguish a genuine malformed-input authoring
// error from a size-limit truncation, which need opposite guidance.
func IsLengthTruncation(stopReason string) bool {
	switch stopReason {
	case StopReasonLength, StopReasonMaxTokens:
		return true
	default:
		return false
	}
}
