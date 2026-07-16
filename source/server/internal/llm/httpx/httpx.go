// Package httpx holds small, provider-neutral HTTP helpers shared by the cloud
// LLM clients.
//
// Note there is deliberately NO retry helper here anymore: transient-failure
// retries are owned by the resilience engine above the adapters, where they
// are bounded, class-driven, and narrated to the user. A retry layer hidden in
// the transport is invisible to that engine and skews its policy (see the
// 2026-07-16 quota incident in docs/agent/cloud-failover-audit.md).
package httpx

import (
	"net/http"
	"strconv"
	"time"
)

// Doer is the minimal HTTP "do" interface, satisfied by *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// QuotaRetryAfterFloor separates a transient rate-limit 429 (seconds-scale
// Retry-After, worth one same-provider retry) from a quota-reset 429
// (minutes-to-hours until the plan window resets, where retrying is
// pointless). Adapters that can read a Retry-After use this floor when
// classifying a 429 as quota vs busy. A quota 429 arriving WITHOUT a
// Retry-After classifies busy instead — the cost is one short extra retry
// before failover; the reverse misclassification would skip a retry that
// might have succeeded.
const QuotaRetryAfterFloor = 30 * time.Second

// RetryAfter reads the server-suggested wait from response headers:
// Retry-After-Ms (milliseconds), then Retry-After as delta-seconds or an
// HTTP-date. Zero when absent or unparsable.
func RetryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After-Ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms > 0 {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
