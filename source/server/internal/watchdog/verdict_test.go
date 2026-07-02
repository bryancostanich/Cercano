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

	// Decorated affirmatives count as violations (prefix match).
	if !parseVerdict("debug-loop", "VIOLATION: yes.\nCHALLENGE: c").Violation {
		t.Fatal("'yes.' must count as a violation")
	}
	if !parseVerdict("debug-loop", "VIOLATION: yes (clearly)\nCHALLENGE: c").Violation {
		t.Fatal("'yes (clearly)' must count as a violation")
	}
	if !parseVerdict("debug-loop", "VIOLATION: true.").Violation {
		t.Fatal("'true.' must count as a violation")
	}
	// "no"-family values never count, decorated or not.
	if parseVerdict("debug-loop", "VIOLATION: no, looks fine").Violation {
		t.Fatal("'no, ...' must not count as a violation")
	}
	// The challenge is the model's line, not the fallback.
	v = parseVerdict("debug-loop", "VIOLATION: yes\nCHALLENGE: too much jargon here")
	if v.Challenge != "too much jargon here" {
		t.Fatalf("challenge must be the model's line, got %q", v.Challenge)
	}
}
