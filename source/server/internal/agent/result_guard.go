package agent

import "fmt"

// maxResultWindowFraction bounds how much of the caller's context window a
// single tool result may consume.
//
// One eighth is chosen so a caller can absorb several large results in a turn
// and still have room to reason: at 1/8, four maximal results occupy half the
// window. Tighter (1/16) starts truncating legitimately large reads on small
// local models; looser (1/4) lets two results crowd out the conversation, which
// is close to the failure this guard exists to prevent.
//
// This is deliberately a different mechanism from the constructor ceiling in
// agenttools: that one is absolute and context-free (it must be, since MCP and
// external hosts have no caller model), while this one is the only layer that
// knows the window actually in play. A 32 KiB result is ~25% of a 32k window but
// ~4% of a 200k one; only here can that distinction be made.
const maxResultWindowFraction = 8

// capToolResultForWindow bounds content against the caller's context window,
// returning the possibly-truncated text.
//
// window <= 0 means the window is unknown (the caller could not resolve one).
// In that case the constructor ceiling is the only bound and this is a no-op —
// guessing a window here would truncate cloud-sized results on no evidence.
//
// Truncation is rune-safe and always announced, matching the Truncated/Note
// contract the result constructors use, so the model can tell a partial view
// from a complete one.
func capToolResultForWindow(content string, window int) (string, bool) {
	if window <= 0 {
		return content, false
	}
	// tokens -> bytes at the same ~4 bytes/token ratio the budget estimator uses.
	maxBytes := (window / maxResultWindowFraction) * 4
	if len(content) <= maxBytes {
		return content, false
	}
	kept := truncateUTF8Boundary(content, maxBytes)
	note := fmt.Sprintf(
		"\n… (tool result truncated: %d of %d bytes shown, capped at 1/%d of this model's context; narrow the request for the rest)",
		len(kept), len(content), maxResultWindowFraction)
	return kept + note, true
}

// truncateUTF8Boundary cuts s to at most maxBytes without splitting a rune.
func truncateUTF8Boundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
