package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNoOpValidator_ReturnsSkipped(t *testing.T) {
	v := NewNoOpValidator("custom reason text")
	decision, err := v.Validate(context.Background(), "/anywhere")
	if decision != Skipped {
		t.Fatalf("got decision %s, want skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %T (%v)", err, err)
	}
	if !strings.Contains(sr.Reason, "custom reason text") {
		t.Errorf("reason = %q, want it to contain %q", sr.Reason, "custom reason text")
	}
}
