package llm

import (
	"regexp"
	"strconv"
	"strings"
)

// llamaServerOverflowRe matches llama-server's context-overflow message, which
// reports both counts, e.g.:
//
//	request (21156 tokens) exceeds the available context size (16384 tokens)
var llamaServerOverflowRe = regexp.MustCompile(`request \((\d+) tokens?\) exceeds the available context size \((\d+) tokens?\)`)

// DetectContextOverflow reports whether a provider error message describes the
// input exceeding the model's context window, and returns any token counts the
// message carried (used, limit). It recognizes both the llama-server form (which
// includes counts) and the opaque OpenAI-compatible cloud form (which does not).
// Counts are 0 when the message did not include them.
//
// Adapters call this from their normalize seam so a context overflow becomes the
// dedicated ErrContextOverflow class with counts attached, instead of falling
// through to ErrInvalidRequest / ErrUnknown with an opaque passthrough.
func DetectContextOverflow(msg string) (overflow bool, used, limit int) {
	if m := llamaServerOverflowRe.FindStringSubmatch(msg); m != nil {
		used, _ = strconv.Atoi(m[1])
		limit, _ = strconv.Atoi(m[2])
		return true, used, limit
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "context size has been exceeded"):
		return true, 0, 0
	case strings.Contains(lower, "exceeds the available context size"):
		return true, 0, 0
	case strings.Contains(lower, "maximum context length") && strings.Contains(lower, "token"):
		// OpenAI's classic phrasing: "This model's maximum context length is
		// 8192 tokens, however you requested 9000 tokens".
		return true, 0, 0
	default:
		return false, 0, 0
	}
}
