package anthropic

import (
	"context"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/httpx"
)

// normalize maps a vendor/transport error into the provider-agnostic
// llm.Error taxonomy. Context cancellation passes through untouched — the
// caller gave up, and that must stay visible as ctx error, not a provider
// failure. The vendor error remains reachable via Unwrap.
func (c *Client) normalize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ae *sdk.Error
	if errors.As(err, &ae) {
		ne := &llm.Error{Provider: c.Name(), StatusCode: ae.StatusCode, Err: err}
		if ae.Response != nil {
			ne.RetryAfter = httpx.RetryAfter(ae.Response.Header)
		}
		switch {
		case ae.StatusCode == http.StatusUnauthorized || ae.StatusCode == http.StatusForbidden:
			ne.Class = llm.ErrAuth
		case ae.StatusCode == http.StatusTooManyRequests:
			// Quota must NEVER be retried — it fails over immediately. Detect
			// it by a quota-scale Retry-After OR the message markers Anthropic
			// uses for plan/credit exhaustion, so a quota 429 that arrives
			// without the header still classifies correctly. Only a bare 429
			// with neither signal (indistinguishable from a transient rate
			// limit on the wire) is treated as busy.
			if ne.RetryAfter >= httpx.QuotaRetryAfterFloor || hasQuotaMarker(err.Error()) {
				ne.Class = llm.ErrQuota
			} else {
				ne.Class = llm.ErrBusy
			}
		case ae.StatusCode >= 500: // includes Anthropic's 529 overloaded_error
			ne.Class = llm.ErrBusy
		case ae.StatusCode == http.StatusBadRequest && hasQuotaMarker(err.Error()):
			// API-key accounts report exhausted credits as a 400
			// invalid_request_error, not a 429.
			ne.Class = llm.ErrQuota
		case ae.StatusCode >= 400:
			ne.Class = llm.ErrInvalidRequest
		default:
			ne.Class = llm.ErrUnknown
		}
		return ne
	}
	if llm.IsNetworkError(err) {
		return &llm.Error{Class: llm.ErrNetwork, Provider: c.Name(), Err: err}
	}
	return &llm.Error{Class: llm.ErrUnknown, Provider: c.Name(), Err: err}
}

// hasQuotaMarker matches the phrasings Anthropic uses for plan/credit
// exhaustion ("You have exceeded your usage limit", "Your credit balance is
// too low", quota mentions) as opposed to transient rate limiting.
func hasQuotaMarker(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "usage limit") ||
		strings.Contains(m, "credit balance") ||
		strings.Contains(m, "quota")
}
