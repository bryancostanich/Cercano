package llm

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
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

// ProviderOf extracts the concrete provider recorded on a normalized provider
// error. It returns an empty string when err is nil, unnormalized, or carries no
// provider name.
func ProviderOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) && e.Provider != "" {
		return e.Provider
	}
	return ""
}

// Retryable reports whether re-running the SAME provider once may succeed.
// The transient classes — ErrBusy (overload) and ErrNetwork (transport reset/
// refused/TLS) — are re-runnable by definition. ErrUnknown gets a single cheap
// attempt too: its own policy already concedes "a wasted second attempt costs
// one round-trip," and if the retry also fails the caller still falls over.
//
// "Once" is not enforced here — it lives at the call sites (the resilience
// engine's single-shot guard and the runner's straight-line turn retry). This
// predicate only decides class membership, so every retry site shares one
// definition of "transient" instead of hardcoding class comparisons.
func Retryable(class ErrorClass) bool {
	return class == ErrBusy || class == ErrNetwork || class == ErrUnknown
}

// Failoverable reports whether re-serving a failed request on a different
// provider/model could plausibly help. Invalid requests generally fail
// everywhere, except for provider/model availability mismatches where another
// provider may serve the requested capability. Context overflow is not broadly
// failoverable here: a caller may allow it only after proving the fallback
// target has a larger known context window.
func Failoverable(class ErrorClass, err error) bool {
	return FailoverableToWindow(class, err, 0, 0, false)
}

// FailoverableToWindow reports whether a fallback target with targetWindow may
// plausibly serve the failed request. Context overflow is allowed only when the
// target window is known and strictly larger than the failed attempt's window.
func FailoverableToWindow(class ErrorClass, err error, failedWindow, targetWindow int, targetWindowKnown bool) bool {
	if class == ErrContextOverflow {
		return failedWindow > 0 && targetWindowKnown && targetWindow > failedWindow
	}
	if class == ErrInvalidRequest {
		return IsProviderModelUnavailable(err)
	}
	return true
}

// IsProviderModelUnavailable reports whether an invalid-request-shaped error
// actually means the selected provider/model cannot serve the requested model,
// making another provider a reasonable fallback target.
func IsProviderModelUnavailable(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "model:") && (strings.Contains(m, "not_found") || strings.Contains(m, "not found")) {
		return true
	}
	return strings.Contains(m, "model not found") ||
		strings.Contains(m, "model_not_found") ||
		strings.Contains(m, "unsupported model") ||
		strings.Contains(m, "model unavailable") ||
		strings.Contains(m, "model is not supported")
}

// IsNetworkError reports whether err is a transport-level failure that should
// classify as ErrNetwork: a *url.Error from the initial request round-trip, a
// *net.OpError from a mid-stream read/write (the SDK stream decoders surface
// raw net errors, NOT url.Error, so those must be caught here), or a bare
// syscall errno (ECONNRESET, EPIPE, ETIMEDOUT) underneath either. Adapters
// call this before falling through to ErrUnknown so a dropped connection is
// classified as the transient failure it is.
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return true
	}
	var se *os.SyscallError
	if errors.As(err, &se) {
		return true
	}
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}
