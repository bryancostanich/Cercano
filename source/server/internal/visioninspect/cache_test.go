package visioninspect

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// countingVision is a fake VisionService that records how many times Inspect
// ran and can be flipped between success and error.
type countingVision struct {
	available bool
	present   map[string]bool // convID|imageID
	answer    capabilities.VisionAnswer
	err       error
	calls     int
}

func (c *countingVision) Available() bool { return c.available }
func (c *countingVision) Lookup(convID, imageID string) bool {
	return c.present[convID+"|"+imageID]
}
func (c *countingVision) Inspect(_ context.Context, _, _, _ string) (capabilities.VisionAnswer, error) {
	c.calls++
	if c.err != nil {
		return capabilities.VisionAnswer{}, c.err
	}
	return c.answer, nil
}

func TestCache_HitOnRepeatedQuestion(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true},
		answer:    capabilities.VisionAnswer{Answer: "red"},
	}
	c := NewCaching(inner)

	for i := 0; i < 3; i++ {
		ans, err := c.Inspect(context.Background(), "c1", "img_1", "what color?")
		if err != nil || ans.Answer != "red" {
			t.Fatalf("call %d: %v / %q", i, err, ans.Answer)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("expected 1 underlying call for repeated question, got %d", inner.calls)
	}
}

func TestCache_NormalizesQuestion(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true},
		answer:    capabilities.VisionAnswer{Answer: "red"},
	}
	c := NewCaching(inner)

	// Different casing/whitespace must hit the same cache entry.
	_, _ = c.Inspect(context.Background(), "c1", "img_1", "What COLOR?")
	_, _ = c.Inspect(context.Background(), "c1", "img_1", "  what   color?  ")
	if inner.calls != 1 {
		t.Fatalf("normalized questions should share a cache entry, got %d calls", inner.calls)
	}
}

func TestCache_DifferentImageOrQuestionMiss(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true, "c1|img_2": true},
		answer:    capabilities.VisionAnswer{Answer: "x"},
	}
	c := NewCaching(inner)

	_, _ = c.Inspect(context.Background(), "c1", "img_1", "q one")
	_, _ = c.Inspect(context.Background(), "c1", "img_1", "q two") // different question
	_, _ = c.Inspect(context.Background(), "c1", "img_2", "q one") // different image
	if inner.calls != 3 {
		t.Fatalf("distinct (image,question) pairs must each call once, got %d", inner.calls)
	}
}

func TestCache_ErrorsNotCached(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true},
		err:       errors.New("boom"),
	}
	c := NewCaching(inner)

	if _, err := c.Inspect(context.Background(), "c1", "img_1", "q?"); err == nil {
		t.Fatal("expected error")
	}
	// Fix the underlying service; the second call must retry (error wasn't cached).
	inner.err = nil
	inner.answer = capabilities.VisionAnswer{Answer: "ok now"}
	ans, err := c.Inspect(context.Background(), "c1", "img_1", "q?")
	if err != nil || ans.Answer != "ok now" {
		t.Fatalf("retry after error failed: %v / %q", err, ans.Answer)
	}
	if inner.calls != 2 {
		t.Fatalf("errors must not be cached; expected 2 calls, got %d", inner.calls)
	}
}

func TestCache_ClearForcesRecall(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true},
		answer:    capabilities.VisionAnswer{Answer: "x"},
	}
	c := NewCaching(inner)

	_, _ = c.Inspect(context.Background(), "c1", "img_1", "q?")
	c.Clear("c1")
	_, _ = c.Inspect(context.Background(), "c1", "img_1", "q?")
	if inner.calls != 2 {
		t.Fatalf("cleared cache must re-call, got %d", inner.calls)
	}
}

func TestCache_PerConversationIsolation(t *testing.T) {
	inner := &countingVision{
		available: true,
		present:   map[string]bool{"c1|img_1": true, "c2|img_1": true},
		answer:    capabilities.VisionAnswer{Answer: "x"},
	}
	c := NewCaching(inner)

	_, _ = c.Inspect(context.Background(), "c1", "img_1", "q?")
	_, _ = c.Inspect(context.Background(), "c2", "img_1", "q?") // same id, different conversation
	if inner.calls != 2 {
		t.Fatalf("conversations must not share cache entries, got %d", inner.calls)
	}
}

func TestCache_AvailableAndLookupPassThrough(t *testing.T) {
	inner := &countingVision{available: true, present: map[string]bool{"c1|img_1": true}}
	c := NewCaching(inner)
	if !c.Available() {
		t.Fatal("Available should pass through true")
	}
	if !c.Lookup("c1", "img_1") {
		t.Fatal("Lookup should pass through hit")
	}
	if c.Lookup("c1", "img_gone") {
		t.Fatal("Lookup should pass through miss")
	}

	inner.available = false
	if c.Available() {
		t.Fatal("Available should reflect the wrapped service turning unavailable")
	}
}

func TestCache_NilInner(t *testing.T) {
	c := NewCaching(nil)
	if c.Available() || c.Lookup("c1", "img_1") {
		t.Fatal("nil inner must report unavailable and no presence")
	}
	if _, err := c.Inspect(context.Background(), "c1", "img_1", "q?"); err == nil {
		t.Fatal("nil inner Inspect must error")
	}
}
