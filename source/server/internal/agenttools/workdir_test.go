package agenttools

import (
	"context"
	"testing"
)

func TestWithWorkDir_RoundTrip(t *testing.T) {
	if got := WorkDirFromContext(context.Background()); got != "" {
		t.Errorf("bare ctx WorkDir = %q, want empty", got)
	}
	ctx := WithWorkDir(context.Background(), "/repo")
	if got := WorkDirFromContext(ctx); got != "/repo" {
		t.Errorf("WorkDirFromContext = %q, want /repo", got)
	}
	// Empty dir is a no-op (does not shadow an outer value).
	if got := WorkDirFromContext(WithWorkDir(ctx, "")); got != "/repo" {
		t.Errorf("empty WithWorkDir should not clear; got %q", got)
	}
}
