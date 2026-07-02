package watchdog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestEchoOnViolation verifies that Gate emits a "watchdog"-thread echo
// containing the challenge string when a violation is detected.
func TestEchoOnViolation(t *testing.T) {
	check := fakeCheck{
		applies: true,
		verdict: Verdict{Violation: true, Protocol: "test-proto", Challenge: "forbidden-action-xyz"},
	}
	w := New(Config{Mode: ModeChallenge}, []Check{check}, nil)

	type emission struct {
		thread string
		text   string
	}
	var got []emission
	w.SetEcho(func(thread, text string) {
		got = append(got, emission{thread, text})
	})

	_ = w.Gate(context.Background(), "conv1", Action{Kind: "tool_call", ToolName: "bash"})

	if len(got) != 1 {
		t.Fatalf("want 1 emission, got %d: %v", len(got), got)
	}
	if got[0].thread != "watchdog" {
		t.Errorf("want thread=watchdog, got %q", got[0].thread)
	}
	if !strings.Contains(got[0].text, "forbidden-action-xyz") {
		t.Errorf("want text to contain challenge %q, got %q", "forbidden-action-xyz", got[0].text)
	}
}

// TestEchoOnJustify verifies that JustifyTool.Execute emits a "main"-thread
// echo containing "justify" and the reason string.
func TestEchoOnJustify(t *testing.T) {
	check := fakeCheck{
		applies: true,
		verdict: Verdict{Violation: true, Protocol: "test-proto", Challenge: "something suspicious"},
	}
	w := New(Config{Mode: ModeChallenge}, []Check{check}, nil)

	type emission struct {
		thread string
		text   string
	}
	var got []emission
	w.SetEcho(func(thread, text string) {
		got = append(got, emission{thread, text})
	})

	// Trigger a challenge so lastChallengedKey is set.
	_ = w.Gate(context.Background(), "conv2", Action{Kind: "tool_call", ToolName: "bash"})

	// Clear captured emissions so we only see the justify emission.
	got = nil

	args, _ := json.Marshal(map[string]string{"reason": "typo here"})
	_, err := w.JustifyTool("conv2").Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("want 1 emission after justify, got %d: %v", len(got), got)
	}
	if got[0].thread != "main" {
		t.Errorf("want thread=main, got %q", got[0].thread)
	}
	if !strings.Contains(got[0].text, "typo here") {
		t.Errorf("want text to contain reason %q, got %q", "typo here", got[0].text)
	}
	if !strings.Contains(got[0].text, "justify") {
		t.Errorf("want text to contain \"justify\", got %q", got[0].text)
	}
}

func TestEchoOnBlock(t *testing.T) {
	w := New(Config{Mode: ModeStrict}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "blocked-c"}}}, nil)
	var got []string
	w.SetEcho(func(thread, text string) { got = append(got, thread+"|"+text) })
	if d := w.Gate(context.Background(), "conv", editAction()); d.Action != "block" {
		t.Fatalf("expected block, got %+v", d)
	}
	if len(got) != 1 || !strings.Contains(got[0], "watchdog|") || !strings.Contains(got[0], "blocked-c") {
		t.Fatalf("block must emit one watchdog-thread line with the challenge: %v", got)
	}
}

func TestEchoOnEscalate(t *testing.T) {
	w := New(Config{Mode: ModeChallenge, EscalateAfter: 2}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "esc-c"}}}, nil)
	var got []string
	w.SetEcho(func(thread, text string) { got = append(got, thread+"|"+text) })
	w.Gate(context.Background(), "conv", editAction()) // challenge (1st emit)
	got = nil
	if d := w.Gate(context.Background(), "conv", editAction()); d.Action != "escalate" {
		t.Fatalf("expected escalate, got %+v", d)
	}
	if len(got) != 1 || !strings.Contains(got[0], "watchdog|") || !strings.Contains(got[0], "esc-c") {
		t.Fatalf("escalate must emit one watchdog-thread line: %v", got)
	}
}

// TestEchoNilSafeWhenUnset verifies that Gate does not panic when SetEcho was
// never called, and returns the expected decision.
func TestEchoNilSafeWhenUnset(t *testing.T) {
	check := fakeCheck{
		applies: true,
		verdict: Verdict{Violation: true, Protocol: "test-proto", Challenge: "something"},
	}
	w := New(Config{Mode: ModeChallenge}, []Check{check}, nil)
	// No SetEcho call.

	d := w.Gate(context.Background(), "conv3", Action{Kind: "tool_call", ToolName: "bash"})
	if d.Action != "challenge" {
		t.Errorf("want action=challenge, got %q", d.Action)
	}
}
