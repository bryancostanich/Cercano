package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// rolloverPreviewSnippet takes the first line, trims, and ellipsizes long text.
func TestRolloverPreviewSnippet(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \n  ", ""},
		{"single line", "Goal: ship the widget", "Goal: ship the widget"},
		{"first line only", "Goal: ship it\nState: compiling\nmore", "Goal: ship it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rolloverPreviewSnippet(c.in); got != c.want {
				t.Errorf("rolloverPreviewSnippet(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRolloverPreviewSnippet_Ellipsizes(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := rolloverPreviewSnippet(long)
	if len([]rune(got)) > 120 {
		t.Errorf("snippet should cap at 120 runes, got %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated snippet should end with an ellipsis, got %q", got)
	}
}

// The main-agent driver maps a TypeRolloverOffered StreamMsg to a
// rolloverOfferedMsg carrying every field the confirm flow needs.
func TestStreamMsgToEvent_RolloverOffered(t *testing.T) {
	ev := streamMsgToEvent(agentclient.StreamMsg{
		Type:           agentclient.TypeRolloverOffered,
		OfferID:        "off-1",
		ConvID:         "conv-a",
		RolloverReason: "grown to 180k tokens",
		HandoffPreview: "Goal: x\nState: y",
	})
	ro, ok := ev.(rolloverOfferedMsg)
	if !ok {
		t.Fatalf("expected rolloverOfferedMsg, got %T", ev)
	}
	if ro.offerID != "off-1" || ro.convID != "conv-a" {
		t.Errorf("ids not mapped: %+v", ro)
	}
	if ro.reason != "grown to 180k tokens" {
		t.Errorf("reason not mapped: %q", ro.reason)
	}
	if ro.preview != "Goal: x\nState: y" {
		t.Errorf("preview not mapped: %q", ro.preview)
	}
}
