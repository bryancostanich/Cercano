package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
			if ne.RetryAfter >= httpx.QuotaRetryAfterFloor {
				ne.Class = llm.ErrQuota
			} else {
				ne.Class = llm.ErrBusy
			}
		case ae.StatusCode >= 500: // includes Anthropic's 529 overloaded_error
			ne.Class = llm.ErrBusy
		case ae.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(err.Error()), "credit balance"):
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
	var ue *url.Error
	if errors.As(err, &ue) {
		return &llm.Error{Class: llm.ErrNetwork, Provider: c.Name(), Err: err}
	}
	return &llm.Error{Class: llm.ErrUnknown, Provider: c.Name(), Err: err}
}
