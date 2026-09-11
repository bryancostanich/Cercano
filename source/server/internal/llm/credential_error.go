package llm

import (
	"context"
	"errors"
	"fmt"
)

// CredentialError carries safe recovery metadata independently of vendor prose.
// Cause remains available to errors.Is/As, but is deliberately excluded from
// Error: keychains, token endpoints and URL wrappers may include secrets.
// Profile names the stored credential, not a hard-coded provider default.
type CredentialError struct {
	Class    ErrorClass
	Provider string
	Profile  string
	Method   string
	Reason   string
	Cause    error
}

const (
	AuthSubscription    = "subscription"
	CredentialMissing   = "missing"
	CredentialExpired   = "expired"
	CredentialRejected  = "rejected"
	CredentialStore     = "store_unavailable"
	CredentialMalformed = "malformed"
	CredentialRefresh   = "refresh_failed"
	CredentialSource    = "source_failed"
)

func (e *CredentialError) Error() string {
	return fmt.Sprintf("%s credential %s (%s)", e.Provider, e.Class, e.Reason)
}
func (e *CredentialError) Unwrap() error { return e.Cause }

// TokenEndpointError contains only a bounded, allowlisted OAuth error code,
// never the response body or error_description. It is not itself a login
// request: invalid_grant from a refresh becomes one at the credential source.
type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("token endpoint HTTP %d (%s)", e.StatusCode, e.Code)
}

// NormalizeCredentialFailure must precede generic URL/network classification:
// SDK transports also wrap local credential errors inside *url.Error.
func NormalizeCredentialFailure(err error, provider, profile string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var credential *CredentialError
	if errors.As(err, &credential) {
		return &Error{Class: credential.Class, Provider: provider, Err: credential}
	}
	class := ErrCredential
	if IsNetworkError(err) {
		class = ErrNetwork
	}
	return &Error{Class: class, Provider: provider, Err: &CredentialError{
		Class: class, Provider: provider, Profile: profile, Method: AuthSubscription, Reason: CredentialSource, Cause: err,
	}}
}

// RefreshFailure distinguishes rejected user grants from client configuration
// and token service failures. Unknown endpoint errors do not demand login.
func RefreshFailure(err error, provider, profile string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	class, reason := ErrCredential, CredentialRefresh
	var endpoint *TokenEndpointError
	if errors.As(err, &endpoint) {
		switch {
		case (endpoint.StatusCode == 400 || endpoint.StatusCode == 401) && endpoint.Code == "invalid_grant":
			class, reason = ErrLoginRequired, CredentialRejected
		case endpoint.StatusCode == 429 || endpoint.StatusCode >= 500:
			class = ErrBusy
		}
	} else if IsNetworkError(err) {
		class = ErrNetwork
	}
	return &CredentialError{Class: class, Provider: provider, Profile: profile, Method: AuthSubscription, Reason: reason, Cause: err}
}
