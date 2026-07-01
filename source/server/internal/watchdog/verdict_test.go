package watchdog

import "testing"

func TestParseVerdict(t *testing.T) {
	v := parseVerdict("debug-loop", "VIOLATION: yes\nCHALLENGE: You're editing to fix a bug with no debug evidence.")
	if !v.Violation || v.Protocol != "debug-loop" || v.Challenge == "" {
		t.Fatalf("expected a violation with a challenge, got %+v", v)
	}
	if parseVerdict("debug-loop", "VIOLATION: no").Violation {
		t.Fatal("VIOLATION: no must be no violation")
	}
	// Fail-open on garbage: no clear violation → no challenge.
	if parseVerdict("debug-loop", "the model rambled without a verdict").Violation {
		t.Fatal("ambiguous output must default to no violation")
	}
}
