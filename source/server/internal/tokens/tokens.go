// Package tokens provides a cheap, dependency-free token-count estimate used
// for telemetry (e.g. estimating the cloud tokens avoided by handling content
// locally). It is intentionally low-level so both internal/mcp and the
// capability layer can import it.
package tokens

// Estimate returns an approximate token count for content.
func Estimate(content string) int {
	if len(content) == 0 {
		return 0
	}
	return len(content) / 4
}
