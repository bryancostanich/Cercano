package llm

import (
	"errors"
	"fmt"
	"time"
)

// ErrorClass is the provider-agnostic failure taxonomy every cloud/local
// adapter normalizes its wire errors into. The resilience engine keys its
// retry/failover policy off the class, never off vendor error types or HTTP
// statuses — those stay inside the adapters, which own wire knowledge.
type ErrorClass string

const (
	// ErrQuota: the account/plan is out of capacity (subscription usage cap,
	// exhausted credits). Retrying the same provider is pointless until the
	// quota resets.
	ErrQuota ErrorClass = "quota"
	// ErrBusy: transient overload — rate limit, 5xx, vendor "overloaded". A
	// short same-provider retry may succeed.
	ErrBusy ErrorClass = "busy"
	// ErrAuth: the credential was rejected (invalid, expired, forbidden).
	ErrAuth ErrorClass = "auth"
	// ErrInvalidRequest: the request itself is malformed; it will fail on any
	// provider and must be surfaced, never retried or failed over.
	ErrInvalidRequest ErrorClass = "invalid_request"
	// ErrContextOverflow: the request's input exceeded the model's context
	// window. Like ErrInvalidRequest it will fail identically on a retry to the
	// same model, but it is its own class so callers can give size-specific
	// guidance (trim the task/context, raise the window, use a bigger-window
	// tier) instead of a generic "bad request". When the provider reports token
	// counts, Error.Used and Error.Limit carry them.
	ErrContextOverflow ErrorClass = "context_overflow"
	// ErrNetwork: transport-level failure before an HTTP response existed
	// (DNS, connection refused/reset, TLS).
	ErrNetwork ErrorClass = "network"
	// ErrUnknown: everything else. Policy treats it like a provider outage
	// (fail over) — a wasted second attempt costs one round-trip, while a
	// missed failover defeats the feature.
	ErrUnknown ErrorClass = "unknown"
)

// Error is a normalized provider failure. Adapters wrap their vendor error in
// one of these at the seam; the vendor error stays reachable via Unwrap for
// logging and tests, but nothing above the adapter may type-switch on it.
type Error struct {
	Class      ErrorClass
	Provider   string        // adapter name, e.g. "anthropic", "openai-responses"
	StatusCode int           // HTTP status when one existed; 0 otherwise
	RetryAfter time.Duration // server-suggested wait; 0 when the server gave none
	Err        error         // the vendor error, wrapped

	// Used and Limit carry token counts for ErrContextOverflow when the provider
	// reports them (e.g. llama-server's "request (N tokens) exceeds the available
	// context size (M tokens)"). Both 0 when the provider gave no numbers (e.g.
	// the opaque cloud "Context size has been exceeded").
	Used  int
	Limit int
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s %s", e.Provider, e.Class)
	if e.StatusCode != 0 {
		msg = fmt.Sprintf("%s (%d)", msg, e.StatusCode)
	}
	if e.Class == ErrContextOverflow && e.Limit > 0 {
		msg = fmt.Sprintf("%s (%d tokens used vs %d limit)", msg, e.Used, e.Limit)
	}
	if e.Err != nil {
		msg = fmt.Sprintf("%s: %v", msg, e.Err)
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// ClassOf extracts the ErrorClass from err's chain. Non-normalized errors
// report ErrUnknown; nil reports the empty class.
func ClassOf(err error) ErrorClass {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return ErrUnknown
}
