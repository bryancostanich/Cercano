package fallback

import (
	"context"
	"errors"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	goopenai "github.com/sashabaranov/go-openai"
)

// ShouldFailover reports whether err is the kind of primary-provider failure a
// backup could plausibly serve: auth failures (401/403), rate limits / quota
// exhaustion (429), server errors (5xx), and network or timeout errors.
//
// Two classes deliberately do NOT fail over:
//   - context cancellation — the caller gave up; retrying anywhere is wrong.
//   - request-shaped 4xx (400, 404, 413, …) — the request itself is bad, so
//     the backup would reject it too.
//
// Errors with no recognizable status code DO fail over: a wasted second
// attempt costs one round-trip, while a missed failover defeats the feature.
func ShouldFailover(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if status, ok := statusCode(err); ok {
		return failoverStatus(status)
	}
	return true
}

// failoverStatus is the agreed trigger set over HTTP status codes.
func failoverStatus(code int) bool {
	switch {
	case code == 401 || code == 403 || code == 429:
		return true
	case code >= 500:
		return true
	default:
		return false
	}
}

// statusCode extracts an HTTP status from the typed errors our cloud SDKs
// return (the anthropic SDK for flavor=messages; go-openai for the
// chat_completions and responses flavors). Bedrock's AWS-chain errors carry no
// HTTP status here and fall into the unknown-error path, which fails over.
func statusCode(err error) (int, bool) {
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		return ae.StatusCode, true
	}
	var oe *goopenai.APIError
	if errors.As(err, &oe) {
		return oe.HTTPStatusCode, true
	}
	var re *goopenai.RequestError
	if errors.As(err, &re) {
		return re.HTTPStatusCode, true
	}
	return 0, false
}
