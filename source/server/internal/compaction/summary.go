package compaction

import (
	"fmt"
	"sort"
	"strings"

	"cercano/source/server/internal/llm"
)

// MergeSummaries reconciles segment summaries into one. Goal is the first
// non-empty; Decisions and OpenThreads concatenate with exact-duplicate removal
// (order preserved); Files union with later values overriding earlier ones for
// the same path; State is the last non-empty. Deterministic.
func MergeSummaries(sums []StructuredSummary) StructuredSummary {
	out := StructuredSummary{Files: map[string]string{}}
	for _, s := range sums {
		if out.Goal == "" && s.Goal != "" {
			out.Goal = s.Goal
		}
		out.Decisions = appendUnique(out.Decisions, s.Decisions)
		out.Proposals = appendUnique(out.Proposals, s.Proposals)
		out.OpenThreads = appendUnique(out.OpenThreads, s.OpenThreads)
		for path, state := range s.Files {
			out.Files[path] = state
		}
		if s.State != "" {
			out.State = s.State
		}
	}
	return out
}

func appendUnique(dst, src []string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[dedupKey(v)] = true
	}
	for _, v := range src {
		k := dedupKey(v)
		if !seen[k] {
			dst = append(dst, v)
			seen[k] = true
		}
	}
	return dst
}

// dedupKey maps a bullet to a canonical form for duplicate detection: lowercase,
// surrounding punctuation/whitespace trimmed, and internal whitespace collapsed
// to single spaces. Two bullets differing only in case, trailing punctuation, or
// spacing share a key and are treated as the same entry. The *original* first-
// seen string is retained for display — only the comparison is normalized — so
// no content is fabricated or mangled, and the mapping is pure and deterministic.
// This is intentionally lexical, not semantic: collapsing genuinely-different
// phrasings of the same idea would require embedding similarity, which is non-
// deterministic and out of scope for this pure merge primitive.
func dedupKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, ".!?,;: \t")
	return strings.Join(strings.Fields(s), " ")
}

// RenderBlock renders the summary into a single text block used as a send-view
// preamble. Section order is fixed for determinism.
func (s StructuredSummary) RenderBlock() llm.Block {
	var b strings.Builder
	b.WriteString("[conversation summary]\n")
	if s.Goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", s.Goal)
	}
	if len(s.Decisions) > 0 {
		b.WriteString("Decisions:\n")
		for _, d := range s.Decisions {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}
	if len(s.Proposals) > 0 {
		b.WriteString("Proposals (awaiting decision):\n")
		for _, p := range s.Proposals {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	if len(s.Files) > 0 {
		b.WriteString("Files:\n")
		for _, path := range sortedKeys(s.Files) {
			fmt.Fprintf(&b, "  - %s: %s\n", path, s.Files[path])
		}
	}
	if len(s.OpenThreads) > 0 {
		b.WriteString("Open threads:\n")
		for _, o := range s.OpenThreads {
			fmt.Fprintf(&b, "  - %s\n", o)
		}
	}
	if s.State != "" {
		fmt.Fprintf(&b, "Current state: %s\n", s.State)
	}
	return llm.Block{Type: llm.BlockText, Text: strings.TrimRight(b.String(), "\n")}
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
