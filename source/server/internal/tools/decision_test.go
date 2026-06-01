package tools

import (
	"errors"
	"testing"
)

func TestDecisionString(t *testing.T) {
	cases := map[Decision]string{
		Passed:  "passed",
		Failed:  "failed",
		Skipped: "skipped",
	}
	for d, want := range cases {
		if got := d.String(); got != want {
			t.Errorf("Decision(%d).String() = %q, want %q", d, got, want)
		}
	}
}

func TestSkipReasonImplementsError(t *testing.T) {
	var err error = &SkipReason{Reason: "no manifest"}
	if err.Error() != "no manifest" {
		t.Errorf("SkipReason.Error() = %q, want %q", err.Error(), "no manifest")
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("errors.As did not unwrap SkipReason")
	}
}
