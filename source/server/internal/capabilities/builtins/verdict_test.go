package builtins

import "testing"

func TestParseVerdict_Safe(t *testing.T) {
	v := parseVerdict("VERDICT: SAFE\nREASONING: looks good")
	if v.Risky {
		t.Errorf("expected Risky=false for SAFE verdict, got true")
	}
	if v.Reasoning != "looks good" {
		t.Errorf("Reasoning = %q, want %q", v.Reasoning, "looks good")
	}
}

func TestParseVerdict_Risky(t *testing.T) {
	v := parseVerdict("VERDICT: RISKY\nREASONING: x")
	if !v.Risky {
		t.Errorf("expected Risky=true for RISKY verdict, got false")
	}
	if v.Reasoning != "x" {
		t.Errorf("Reasoning = %q, want %q", v.Reasoning, "x")
	}
}

func TestParseVerdict_Refuted(t *testing.T) {
	v := parseVerdict("REFUTED: the claim is false")
	if !v.Risky {
		t.Errorf("expected Risky=true for REFUTED text, got false")
	}
}

func TestParseVerdict_EmptyIsRisky(t *testing.T) {
	v := parseVerdict("")
	if !v.Risky {
		t.Errorf("expected Risky=true for empty text (fail-safe), got false")
	}
}

func TestParseVerdict_GarbageIsRisky(t *testing.T) {
	v := parseVerdict("I have no opinion on this matter.")
	if !v.Risky {
		t.Errorf("expected Risky=true for garbage/no-verdict text, got false")
	}
}

func TestParseVerdict_ReasoningFallback(t *testing.T) {
	// No REASONING: line — should fall back to the full trimmed text.
	v := parseVerdict("VERDICT: SAFE")
	if v.Risky {
		t.Errorf("expected Risky=false, got true")
	}
	if v.Reasoning != "VERDICT: SAFE" {
		t.Errorf("Reasoning = %q, want %q", v.Reasoning, "VERDICT: SAFE")
	}
}
