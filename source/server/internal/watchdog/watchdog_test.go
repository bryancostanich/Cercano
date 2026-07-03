package watchdog

import (
	"context"
	"testing"
	"time"
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

func TestKeyForTurnEndUsesText(t *testing.T) {
	a1 := Action{Kind: "turn_end", Text: "reply one"}
	a2 := Action{Kind: "turn_end", Text: "reply two"}
	if keyFor("plain-english", a1) == keyFor("plain-english", a2) {
		t.Fatal("different turn_end texts must yield different keys")
	}
	if keyFor("plain-english", a1) != keyFor("plain-english", Action{Kind: "turn_end", Text: "reply one"}) {
		t.Fatal("same turn_end text must yield the same key")
	}
	// tool_call identity unchanged (keys on ToolArgs, not Text)
	tc := Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"p":"a"}`), Text: "ignored"}
	if keyFor("debug-loop", tc) != keyFor("debug-loop", Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"p":"a"}`), Text: "different"}) {
		t.Fatal("tool_call key must ignore Text")
	}
}

// slowCheck blocks until its context is canceled, simulating a check whose
// oneShot lane is drowning (cold model load, saturated Ollama). It honors ctx
// like the real dispatch-backed oneShot does.
type slowCheck struct{}

func (slowCheck) Name() string          { return "slow-check" }
func (slowCheck) Applies(a Action) bool { return true }
func (slowCheck) Evaluate(ctx context.Context, _ Action, _ OneShotFunc) (Verdict, error) {
	<-ctx.Done()
	return Verdict{}, ctx.Err()
}

// TestGate_CheckTimeoutFailsOpen pins the stream-lockout fix: a check that
// hangs must be cut off by the gate's own deadline and fail open, so a sick
// local model lane can never hold a turn's stream open behind supervision.
func TestGate_CheckTimeoutFailsOpen(t *testing.T) {
	w := New(Config{Mode: ModeChallenge, CheckTimeout: 30 * time.Millisecond}, []Check{slowCheck{}}, nil)

	start := time.Now()
	d := w.Gate(context.Background(), "conv", Action{Kind: "turn_end", Text: "some final reply"})
	elapsed := time.Since(start)

	if d.Action != "allow" {
		t.Errorf("hung check must fail open, got action %q", d.Action)
	}
	if elapsed > 2*time.Second {
		t.Errorf("gate took %v; the check timeout did not bound it", elapsed)
	}
}

// TestGate_CheckTimeoutDefaulted pins that a zero CheckTimeout gets a sane
// default rather than no deadline at all.
func TestGate_CheckTimeoutDefaulted(t *testing.T) {
	w := New(Config{Mode: ModeChallenge}, nil, nil)
	if w.checkTimeout <= 0 {
		t.Fatalf("checkTimeout = %v, want a positive default", w.checkTimeout)
	}
}
