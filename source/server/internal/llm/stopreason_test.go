package llm

import "testing"

func TestIsLengthTruncation(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"length", true},        // OpenAI / llama-server / ollama
		{"max_tokens", true},    // Anthropic / Bedrock
		{"", false},             // no reason reported
		{"stop", false},         // natural completion
		{"end_turn", false},     // Anthropic natural completion
		{"tool_use", false},     // emitted a tool call
		{"tool_calls", false},   // OpenAI emitted a tool call
		{"stop_sequence", false},
		{"content_filter", false},
		{"LENGTH", false}, // case-sensitive: adapters copy the wire value verbatim
	}
	for _, c := range cases {
		if got := IsLengthTruncation(c.reason); got != c.want {
			t.Errorf("IsLengthTruncation(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
}
