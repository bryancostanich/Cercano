package watchdog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestJustifyToolRecordsOverride(t *testing.T) {
	w := New(Config{Mode: ModeChallenge},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)

	// First Gate call should challenge.
	d := w.Gate(context.Background(), "conv", editAction())
	if d.Action != "challenge" {
		t.Fatalf("expected challenge, got %+v", d)
	}

	// Obtain the justify tool and check its name.
	tool := w.JustifyTool("conv")
	if tool.Name() != "justify" {
		t.Fatalf("Name() = %q, want %q", tool.Name(), "justify")
	}

	// Execute the justify tool with a reason.
	args, _ := json.Marshal(map[string]string{"reason": "obvious typo"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Text != "watchdog override recorded: obvious typo" {
		t.Fatalf("unexpected result text: %q", res.Text)
	}

	// Next Gate call on the same action must now allow.
	d2 := w.Gate(context.Background(), "conv", editAction())
	if d2.Action != "allow" {
		t.Fatalf("after justify, expected allow, got %+v", d2)
	}
}

func TestJustifyNoActiveChallenge(t *testing.T) {
	w := New(Config{Mode: ModeChallenge},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)

	// Call justify with NO prior Gate/challenge.
	tool := w.JustifyTool("conv")
	args, _ := json.Marshal(map[string]string{"reason": "x"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Text == "" || res.Text == "watchdog override recorded: x" {
		t.Fatalf("expected no-active message, got %q", res.Text)
	}
	if len(res.Text) < 3 || res.Text[:9] != "no active" {
		t.Fatalf("expected text to start with 'no active', got %q", res.Text)
	}

	// A subsequent Gate on a violating action must still challenge (nothing was spuriously justified).
	d := w.Gate(context.Background(), "conv", editAction())
	if d.Action != "challenge" {
		t.Fatalf("expected challenge after no-op justify, got %+v", d)
	}
}
