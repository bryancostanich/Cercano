package watchdog

import (
	"context"
	"testing"
)

func editAction() Action {
	return Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"x"}`)}
}

// fakeCheck is declared in check_test.go (same package).

func TestGateChallengeThenEscalate(t *testing.T) {
	w := New(Config{Mode: ModeChallenge, EscalateAfter: 2},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)
	d1 := w.Gate(context.Background(), "conv", editAction())
	if d1.Action != "challenge" || d1.Challenge != "c" {
		t.Fatalf("first: %+v", d1)
	}
	d2 := w.Gate(context.Background(), "conv", editAction()) // same action repeats → hits threshold
	if d2.Action != "escalate" {
		t.Fatalf("second (repeat) should escalate, got %+v", d2)
	}
}

func TestGateJustifyAllows(t *testing.T) {
	w := New(Config{Mode: ModeChallenge},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)
	a := editAction()
	if w.Gate(context.Background(), "conv", a).Action != "challenge" {
		t.Fatal("expected challenge")
	}
	w.recordJustify("conv", keyFor("debug-loop", a))
	if w.Gate(context.Background(), "conv", a).Action != "allow" {
		t.Fatal("after justify, the same action must be allowed")
	}
}

func TestGateStrictBlocks(t *testing.T) {
	w := New(Config{Mode: ModeStrict},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)
	if w.Gate(context.Background(), "conv", editAction()).Action != "block" {
		t.Fatal("strict mode must block")
	}
}

func TestGateFailsOpenOnCheckError(t *testing.T) {
	w := New(Config{Mode: ModeChallenge}, []Check{errCheck{}}, nil)
	if w.Gate(context.Background(), "conv", editAction()).Action != "allow" {
		t.Fatal("a check error must fail open (allow)")
	}
}

type errCheck struct{}

func (errCheck) Name() string        { return "err" }
func (errCheck) Applies(Action) bool { return true }
func (errCheck) Evaluate(_ context.Context, _ Action, _ OneShotFunc) (Verdict, error) {
	return Verdict{}, context.DeadlineExceeded
}

func TestGateNoViolationAllows(t *testing.T) {
	w := New(Config{Mode: ModeStrict},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: false}}},
		nil,
	)
	if d := w.Gate(context.Background(), "conv", editAction()); d.Action != "allow" {
		t.Fatalf("no violation must allow, got %+v", d)
	}
}

func TestGateEscalateAfterDefault(t *testing.T) {
	// EscalateAfter 0 → default 2: challenge on first, escalate on second
	w := New(Config{Mode: ModeChallenge, EscalateAfter: 0},
		[]Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}},
		nil,
	)
	d1 := w.Gate(context.Background(), "conv", editAction())
	if d1.Action != "challenge" {
		t.Fatalf("first (default EscalateAfter=2): expected challenge, got %+v", d1)
	}
	d2 := w.Gate(context.Background(), "conv", editAction())
	if d2.Action != "escalate" {
		t.Fatalf("second (default EscalateAfter=2): expected escalate, got %+v", d2)
	}
}

func TestKeyForStable(t *testing.T) {
	a := editAction()
	k1 := keyFor("debug-loop", a)
	k2 := keyFor("debug-loop", a)
	if k1 != k2 {
		t.Fatalf("keyFor not stable: %q vs %q", k1, k2)
	}
	k3 := keyFor("other-protocol", a)
	if k1 == k3 {
		t.Fatal("different protocol must produce different key")
	}
}
