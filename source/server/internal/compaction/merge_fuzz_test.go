package compaction

import (
	"strings"
	"testing"
)

// This file holds the property/fuzz tests (Option C) for the merge primitive
// that underlies all consolidation. Where the longitudinal test (Option A)
// proves the whole orchestrator stays bounded on ONE real conversation, these
// tests hammer MergeSummaries across thousands of randomized shapes to catch
// the dedup-drift class generally.
//
// MergeSummaries has no cap of its own — it is bounded in production only
// because Advance wraps it with pruneToFit. So the property we can assert about
// the primitive today is INVARIANCE and MONOTONE-SAFETY, not a hard token cap:
//   - determinism: same input → same output.
//   - idempotence: merging a merged result with itself adds nothing.
//   - no-fabrication: every output entry came from some input.
//   - Goal/State semantics: first-non-empty Goal, last-non-empty State.
//   - exact-duplicate collapse: identical strings never duplicate.
//
// A separate aspirational fuzz target documents the ideal that reworded
// near-duplicates should also collapse — which today's exact-string dedup does
// not do. It is skipped until semantic/normalized dedup or a per-section cap
// lands.

// realDecisionSeeds are authentic decision/proposal-shaped lines lifted from the
// real "CERCANO - AGENT BEHAVIORS" conversation, so the fuzzer starts from the
// phrasings the system actually produces rather than synthetic strings.
var realDecisionSeeds = []string{
	"Adopt deterministic pruning over LLM re-summarization for the ledger.",
	"The watchdog is config-gated (default off) and steering is assembled server-side.",
	"Native tool calling supersedes embedding-based dispatch.",
	"Permission model is three-mode: Strict, Permissive, Bypass.",
	"Replace the fixed compaction ceiling with a window-relative budget.",
	"Sessions are stored as independent rows linked via precursor_id.",
	"Tiered retention keeps recent segments verbatim and compresses ancient ones.",
	"The handoff artifact includes the structured summary plus the last N turns.",
}

// FuzzMergeSummaries checks the invariants MergeSummaries must hold for ANY
// sequence of decision strings. The fuzzed bytes are split into individual
// decision lines, each becoming a one-decision segment summary.
func FuzzMergeSummaries(f *testing.F) {
	// Seed with real phrasings and some adversarial shapes.
	f.Add(strings.Join(realDecisionSeeds, "\n"))
	f.Add("")
	f.Add("a\na\na\na") // exact duplicates
	f.Add(strings.Repeat("x\n", 500))

	f.Fuzz(func(t *testing.T, blob string) {
		lines := splitNonEmpty(blob)

		parts := make([]StructuredSummary, 0, len(lines))
		for i, ln := range lines {
			s := StructuredSummary{Decisions: []string{ln}}
			// Give the first and last segments a Goal/State so the semantics
			// invariants have something to check.
			if i == 0 {
				s.Goal = "G-first"
			}
			if i == len(lines)-1 {
				s.State = "S-last"
			}
			parts = append(parts, s)
		}

		merged := MergeSummaries(parts)

		// INVARIANT — no fabrication: every merged decision came from an input.
		inputSet := map[string]bool{}
		for _, ln := range lines {
			inputSet[ln] = true
		}
		for _, d := range merged.Decisions {
			if !inputSet[d] {
				t.Fatalf("fabricated decision not present in any input: %q", d)
			}
		}

		// INVARIANT — exact-duplicate collapse: output has no repeated string.
		seen := map[string]bool{}
		for _, d := range merged.Decisions {
			if seen[d] {
				t.Fatalf("exact duplicate survived merge: %q", d)
			}
			seen[d] = true
		}

		// INVARIANT — output count never exceeds the count of DISTINCT inputs.
		if len(merged.Decisions) > len(inputSet) {
			t.Fatalf("merged has %d decisions but only %d distinct inputs",
				len(merged.Decisions), len(inputSet))
		}

		// INVARIANT — Goal/State semantics.
		if len(lines) > 0 && merged.Goal != "G-first" {
			t.Fatalf("Goal = %q, want first-non-empty G-first", merged.Goal)
		}
		if len(lines) > 0 && merged.State != "S-last" {
			t.Fatalf("State = %q, want last-non-empty S-last", merged.State)
		}

		// INVARIANT — determinism: a second merge of the same parts is identical.
		merged2 := MergeSummaries(parts)
		if !equalStrings(merged.Decisions, merged2.Decisions) {
			t.Fatal("non-deterministic merge")
		}

		// INVARIANT — idempotence: merging the result with itself adds nothing.
		twice := MergeSummaries([]StructuredSummary{merged, merged})
		if len(twice.Decisions) != len(merged.Decisions) {
			t.Fatalf("not idempotent: %d decisions after re-merge, was %d",
				len(twice.Decisions), len(merged.Decisions))
		}
	})
}

// FuzzMergeSummaries_RewordedCollapse_Aspirational documents the ideal that
// near-duplicate rewordings of the same decision should collapse. Today's
// exact-string dedup keeps them all, so this is skipped until a
// semantic/normalized dedup (or a per-section cap) lands. Un-skip to see it
// fail against current code.
func FuzzMergeSummaries_RewordedCollapse_Aspirational(f *testing.F) {
	f.Add("adopt deterministic pruning", "Adopt deterministic pruning.")
	f.Add("prefer a mutex here", "We should prefer a mutex here")

	f.Fuzz(func(t *testing.T, a, b string) {
		t.Skip("aspirational: reworded near-duplicates are not collapsed yet")

		// The intent: two phrasings that differ only in case/punctuation/filler
		// should merge to a single canonical decision.
		merged := MergeSummaries([]StructuredSummary{
			{Decisions: []string{a}},
			{Decisions: []string{b}},
		})
		if normalize(a) == normalize(b) && len(merged.Decisions) > 1 {
			t.Fatalf("reworded duplicates not collapsed: %q vs %q", a, b)
		}
	})
}

func splitNonEmpty(blob string) []string {
	var out []string
	for _, ln := range strings.Split(blob, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// normalize is a reference notion of "same decision" used only by the
// aspirational target: lowercase, strip surrounding punctuation/whitespace.
func normalize(s string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(s)), ".!? ")
}
