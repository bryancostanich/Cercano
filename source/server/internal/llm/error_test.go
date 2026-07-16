package llm

import (
	"context"
	"errors"
	"fmt"
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
