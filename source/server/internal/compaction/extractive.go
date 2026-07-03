package compaction

import (
	"context"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// ExtractiveCompactor is the quote-only contender: the model may select what
// matters but must copy it verbatim, never rephrase. Its output is
// mechanically verifiable — every bullet must be a substring of the source
// transcript (checked by GroundedBullets). No production system surveyed uses
// pure extraction; this frame tests whether the verifiability is worth the
// weaker compression.
type ExtractiveCompactor struct{}

func (ExtractiveCompactor) Name() string { return "extractive" }

func (ExtractiveCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)
	older, recent := splitRecent(elided, b.VerbatimRecent)

	var sum StructuredSummary
	if len(older) > 0 {
		tok := contextmeter.Default()
		for _, seg := range SegmentByTokens(older, tok, segTokens(b)) {
			input := append(renderSummaryMessages(sum), seg.Messages...)
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		}
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}

// BuildExtractivePrompt asks for verbatim quotes only, in the shared section
// format so ParseSummary applies unchanged. Selection is the model's job;
// wording is not.
func BuildExtractivePrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString("Extract the load-bearing content from the conversation below.\n")
	b.WriteString("\n")
	b.WriteString("You may SELECT what matters, but you may not REWRITE it:\n")
	b.WriteString("- Every bullet must be an exact, character-for-character quote copied from the conversation (you may trim leading/trailing whitespace, nothing else).\n")
	b.WriteString("- Do not paraphrase, merge, or complete sentences. If a passage is too long, quote its most load-bearing contiguous span.\n")
	b.WriteString("- Quote identifiers, signatures, config keys, YAML, and code exactly as written.\n")
	b.WriteString("- Write each quote as plain text: no surrounding quotation marks, no escaping — keep real newlines and quote characters exactly as they appear in the conversation.\n")
	b.WriteString("- Omit any section with nothing worth quoting. Never invent a quote.\n")
	b.WriteString("\n")
	b.WriteString("Use only these section labels:\n\n")
	b.WriteString("GOAL: <short verbatim quote naming the objective>\n")
	b.WriteString("DECISIONS:\n- <verbatim quote of a confirmed decision>\n")
	b.WriteString("PROPOSALS:\n- <verbatim quote of a proposal awaiting a verdict>\n")
	b.WriteString("FILES:\n- <path>: <verbatim quote about its state>\n")
	b.WriteString("OPEN:\n- <verbatim quote of an unresolved question or next step>\n")
	b.WriteString("STATE: <short verbatim quote of the current state>\n\n")
	b.WriteString("--- conversation ---\n")
	writeTranscript(&b, messages)
	return b.String()
}

// GroundedBullets counts how many of the summary's bullets appear verbatim in
// source (whitespace-normalized). The pair (grounded, total) is the harness's
// mechanical hallucination metric: for the extractive frame ungrounded
// bullets are defects by construction; for paraphrase frames the grounded
// fraction is a (looser) drift signal.
//
// Bullets are unwrapped before matching: models quote-wrap extracted spans
// (- "...") and JSON-escape newlines and inner quotes inside them, neither of
// which exists in the source text. Without unwrapping, genuinely verbatim
// quotes score 0 — observed on every frame in matrix run 2.
func GroundedBullets(s StructuredSummary, source string) (grounded, total int) {
	src := normalizeSpace(source)
	check := func(item string) {
		total++
		if item != "" && strings.Contains(src, normalizeSpace(unwrapQuote(item))) {
			grounded++
		}
	}
	for _, d := range s.Decisions {
		check(d)
	}
	for _, p := range s.Proposals {
		check(p)
	}
	for _, o := range s.OpenThreads {
		check(o)
	}
	for _, state := range s.Files {
		check(state)
	}
	return grounded, total
}

// normalizeSpace collapses all whitespace runs to single spaces so line wraps
// and indentation differences don't defeat substring matching.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// unwrapQuote strips a surrounding quote pair and undoes JSON-style escapes
// (\n, \t, \") that models introduce when quoting multi-line spans.
func unwrapQuote(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []struct{ open, close string }{{`"`, `"`}, {"“", "”"}, {"'", "'"}} {
		if len(s) >= 2 && strings.HasPrefix(s, q.open) && strings.HasSuffix(s, q.close) {
			s = strings.TrimSuffix(strings.TrimPrefix(s, q.open), q.close)
			break
		}
	}
	r := strings.NewReplacer(`\n`, " ", `\t`, " ", `\"`, `"`)
	return r.Replace(s)
}
