package main

import "testing"

// An explicit --conv id is used verbatim. An empty one mints a fresh random id
// — NOT a pid-derived one, which recycles and would silently continue a
// stranger's conversation (poisoning its Meridian lineage, per the cross-
// session incident).
func TestResolveHeadlessConvID(t *testing.T) {
	if got := resolveHeadlessConvID("my-session"); got != "my-session" {
		t.Errorf("explicit id: got %q, want my-session", got)
	}

	a := resolveHeadlessConvID("")
	b := resolveHeadlessConvID("")
	if a == "" || b == "" {
		t.Fatal("empty --conv must mint a non-empty id")
	}
	if a == b {
		t.Errorf("minted ids must be unique across runs, got %q twice", a)
	}
	for _, id := range []string{a, b} {
		if len(id) < 8 {
			t.Errorf("minted id %q is implausibly short for a random id", id)
		}
		if id == "headless-" || len(id) > 4 && id[:9] == "headless-" {
			t.Errorf("minted id %q is pid-derived; must be random", id)
		}
	}
}
