package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestErrorWrapsVendorError(t *testing.T) {
	vendor := errors.New("vendor said no")
	e := &Error{Class: ErrBusy, Provider: "anthropic", StatusCode: 529, Err: vendor}

	if !errors.Is(e, vendor) {
		t.Fatalf("errors.Is must reach the wrapped vendor error")
	}
	var got *Error
	if !errors.As(fmt.Errorf("turn failed: %w", e), &got) {
		t.Fatalf("errors.As must find *llm.Error through further wrapping")
	}
	if got.Class != ErrBusy || got.StatusCode != 529 {
		t.Fatalf("got %+v, want busy/529", got)
	}
}

func TestErrorMessageNamesProviderAndClass(t *testing.T) {
	e := &Error{Class: ErrQuota, Provider: "anthropic", StatusCode: 429,
		RetryAfter: time.Hour, Err: errors.New("usage limit reached")}
	msg := e.Error()
	for _, want := range []string{"anthropic", "quota", "429", "usage limit reached"} {
		if !containsFold(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestClassOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"nil", nil, ""},
		{"foreign error", errors.New("boom"), ErrUnknown},
		{"direct", &Error{Class: ErrAuth}, ErrAuth},
		{"wrapped", fmt.Errorf("x: %w", &Error{Class: ErrQuota}), ErrQuota},
		{"context cancel stays foreign", context.Canceled, ErrUnknown},
	}
	for _, tc := range cases {
		if got := ClassOf(tc.err); got != tc.want {
			t.Errorf("%s: ClassOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		class ErrorClass
		want  bool
	}{
		{ErrBusy, true},    // transient overload — re-run may succeed
		{ErrNetwork, true}, // transport reset — re-run may succeed
		{ErrUnknown, true}, // one cheap attempt, then failover
		{ErrQuota, false},  // pointless until quota resets
		{ErrAuth, false},   // bad credential won't fix on retry
		{ErrInvalidRequest, false},
		{ErrContextOverflow, false},
	}
	for _, tc := range cases {
		if got := Retryable(tc.class); got != tc.want {
			t.Errorf("Retryable(%q) = %v, want %v", tc.class, got, tc.want)
		}
	}
}

func TestFailoverable(t *testing.T) {
	cases := []struct {
		name  string
		class ErrorClass
		err   error
		want  bool
	}{
		{"context overflow surfaces", ErrContextOverflow, &Error{Class: ErrContextOverflow, Provider: "anthropic"}, false},
		{"invalid request surfaces", ErrInvalidRequest, &Error{Class: ErrInvalidRequest, Provider: "openai", Err: errors.New("bad parameter")}, false},
		{"invalid request model unavailable fails over", ErrInvalidRequest, &Error{Class: ErrInvalidRequest, Provider: "openai", Err: errors.New("model_not_found")}, true},
		{"quota fails over", ErrQuota, &Error{Class: ErrQuota, Provider: "anthropic"}, true},
		{"auth fails over", ErrAuth, &Error{Class: ErrAuth, Provider: "anthropic"}, true},
		{"busy fails over", ErrBusy, &Error{Class: ErrBusy, Provider: "anthropic"}, true},
		{"network fails over", ErrNetwork, &Error{Class: ErrNetwork, Provider: "anthropic"}, true},
		{"unknown fails over", ErrUnknown, errors.New("boom"), true},
	}
	for _, tc := range cases {
		if got := Failoverable(tc.class, tc.err); got != tc.want {
			t.Errorf("%s: Failoverable(%q, %v) = %v, want %v", tc.name, tc.class, tc.err, got, tc.want)
		}
	}
}

func TestIsProviderModelUnavailable(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"model: claude-opus not_found", true},
		{"model not found", true},
		{"model_not_found", true},
		{"unsupported model", true},
		{"model unavailable", true},
		{"model is not supported", true},
		{"request body was invalid", false},
	}
	for _, tc := range cases {
		if got := IsProviderModelUnavailable(errors.New(tc.msg)); got != tc.want {
			t.Errorf("IsProviderModelUnavailable(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestIsNetworkError(t *testing.T) {
	// The regression: a mid-stream "connection reset by peer" arrives as a raw
	// *net.OpError wrapping ECONNRESET — NOT a *url.Error — so the old
	// url.Error-only check missed it and it fell through to ErrUnknown.
	opErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: os.NewSyscallError("read", syscall.ECONNRESET),
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"url.Error", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("dial")}, true},
		{"net.OpError connreset", opErr, true},
		{"wrapped net.OpError", fmt.Errorf("stream: %w", opErr), true},
		{"bare ECONNRESET", syscall.ECONNRESET, true},
		{"bare EPIPE", syscall.EPIPE, true},
		{"foreign error", errors.New("boom"), false},
		{"context cancel", context.Canceled, false},
	}
	for _, tc := range cases {
		if got := IsNetworkError(tc.err); got != tc.want {
			t.Errorf("%s: IsNetworkError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
