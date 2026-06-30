package gitflow

import (
	"context"
	"testing"
)

func TestSafetyRefRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	head, _ := r.RevParse(ctx, "HEAD")
	if err := r.RecordSafety(ctx, "land", "HEAD"); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadSafety(ctx, "land")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Fatalf("safety ref: got %s want %s", got, head)
	}
	if _, err := r.ReadSafety(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error reading missing safety ref")
	}
}
