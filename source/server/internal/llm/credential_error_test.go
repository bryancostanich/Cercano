package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestCredentialErrorSurvivesWrappingWithoutLoggingSecrets(t *testing.T) {
	cause := errors.New("access_token=super-secret")
	original := &CredentialError{Class: ErrLoginRequired, Provider: "anthropic", Profile: "work", Method: AuthSubscription, Reason: CredentialExpired, Cause: cause}
	wrapped := &url.Error{Op: "Post", URL: "https://example.invalid?code=super-secret", Err: fmt.Errorf("SDK: %w", original)}
	normalized := NormalizeCredentialFailure(wrapped, "anthropic", "")
	var got *CredentialError
	if !errors.As(normalized, &got) || got != original || !errors.Is(normalized, cause) {
		t.Fatalf("lost identity/cause: %v", normalized)
	}
	if ClassOf(normalized) != ErrLoginRequired || Retryable(ClassOf(normalized)) || Failoverable(ClassOf(normalized), normalized) {
		t.Fatal("login failure may not silently retry or fail over")
	}
	if strings.Contains(normalized.Error(), "super-secret") {
		t.Fatal("secret in error")
	}
}
func TestRefreshFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"invalid grant", &TokenEndpointError{400, "invalid_grant"}, ErrLoginRequired},
		{"revoked grant", &TokenEndpointError{401, "invalid_grant"}, ErrLoginRequired},
		{"bad client", &TokenEndpointError{401, "invalid_client"}, ErrCredential},
		{"permission", &TokenEndpointError{403, "invalid_grant"}, ErrCredential},
		{"server failure with misleading code", &TokenEndpointError{503, "invalid_grant"}, ErrBusy},
		{"rate limited", &TokenEndpointError{429, "unrecognized"}, ErrBusy},
		{"unknown response", &TokenEndpointError{400, "unrecognized"}, ErrCredential},
		{"network", &url.Error{Op: "Post", URL: "https://example.invalid", Err: syscall.ECONNRESET}, ErrNetwork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RefreshFailure(tc.err, "chatgpt", "named-profile")
			var got *CredentialError
			if !errors.As(err, &got) || got.Profile != "named-profile" || got.Class != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("got %+v", err)
			}
		})
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := RefreshFailure(fmt.Errorf("wrapped: %w", err), "anthropic", "p"); got != err {
			t.Fatal(got)
		}
	}
}
func TestNonInteractiveCredentialFailuresDoNotFailover(t *testing.T) {
	for _, class := range []ErrorClass{ErrLoginRequired, ErrCredential, ErrPermission} {
		if Retryable(class) || Failoverable(class, &Error{Class: class}) {
			t.Fatalf("unexpected implicit retry/fallback: %s", class)
		}
	}
}
