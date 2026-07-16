// Command compaction-repro exercises the compaction summarizer against a real
// conversation from ~/.config/cercano/conversations.db, using Ollama directly
// (no running Cercano agent required). It prints the produced summary plus a
// grounding analysis — which of a user-supplied set of anchor substrings
// actually appear in the summary, and which known fabrication tells (that we
// know did NOT happen in the conversation) leak through.
//
// This is the reproducer for the fabrication bug in compaction: the current
// prompt + local model was writing plausible-sounding summaries that had
// nothing to do with the actual conversation. Run this with a suspected model,
// eyeball the output, and diff against a run with a better one.
//
// Typical use:
//
//	# Repro the fabrication on qwen3-coder (the default local model).
//	go run ./cmd/compaction-repro -conv 80109e871fba4e18 -model qwen3-coder
//
//	# Try phi4 instead (needs `ollama pull phi4`).
//	go run ./cmd/compaction-repro -conv 80109e871fba4e18 -model phi4
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	ollamallm "cercano/source/server/internal/llm/ollama"
)

func main() {
	convID := flag.String("conv", "80109e871fba4e18", "conversation id from ~/.config/cercano/conversations.db")
	model := flag.String("model", "qwen3-coder", "Ollama model tag to use for the summarizer")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "Ollama base URL")
	floor := flag.Int("floor", 1000, "activation floor tokens (lowered so short conversations still trigger)")
	segTokens := flag.Int("seg", 4000, "segment tokens")
	verbatim := flag.Int("verbatim", 6, "verbatim recent turns kept out of the summary")
	anchors := flag.String("anchors", "elide,compaction,tool_result,phi4,summarizer,dedup",
		"comma-separated substrings that SHOULD appear in the summary (the conversation is really about these)")
	tells := flag.String("tells", "race condition,tests pass,rename-before-ensure,merge conflict",
		"comma-separated fabrication tells that SHOULD NOT appear (weren't discussed here)")
	timeout := flag.Duration("timeout", 5*time.Minute, "total run timeout")
	aroundTurn := flag.String("aroundturn", "", "slice a window around this turn id (default: whole conversation)")
	before := flag.Int("before", 40, "with -aroundturn: turns of context before the target")
	after := flag.Int("after", 10, "with -aroundturn: turns of context after the target")
	dbFlag := flag.String("db", os.ExpandEnv("$HOME/.config/cercano/conversations.db"), "conversations database to read (any sliced copy works — nothing is written)")
	jsonOut := flag.Bool("json", false, "emit one machine-readable JSON result object on stdout (human output moves to stderr)")
	flag.Parse()

	// hout carries all human-oriented output. In -json mode it moves to
	// stderr so stdout is exactly one parseable JSON object.
	hout := io.Writer(os.Stdout)
	if *jsonOut {
		hout = os.Stderr
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, err := conversation.Open(*dbFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *dbFlag, err)
		os.Exit(1)
	}
	turns, err := store.GetTurns(ctx, *convID)
	if err != nil || len(turns) == 0 {
		fmt.Fprintf(os.Stderr, "getturns %s: %v (or empty)\n", *convID, err)
		os.Exit(1)
	}
	if *aroundTurn != "" {
		target := -1
		for i, t := range turns {
			if t.ID == *aroundTurn {
				target = i
				break
			}
		}
		if target < 0 {
			fmt.Fprintf(os.Stderr, "turn %s not found in %s (%d turns)\n", *aroundTurn, *convID, len(turns))
			os.Exit(1)
		}
		lo := max(0, target-*before)
		hi := min(len(turns), target+*after+1)
		turns = turns[lo:hi]
	}

	// Summarize through the production turn-runner stack (inference.Provider →
	// agent.TurnRunner → agent.Request), not a raw Ollama completion call, so
	// this tool reproduces exactly what the agent does — including the
	// greedy-decoding pin below.
	provider := agent.InferenceTurnRunner(ollamallm.NewClient(ollamallm.Config{
		BaseURL: *ollamaURL,
		Model:   *model,
	}), *model)

	msgs := agent.BuildLLMHistory(turns)
	tok := contextmeter.Default()
	rawTok := compaction.TotalTokens(tok, msgs)

	fmt.Fprintf(hout, "conv=%s model=%s ollama=%s\n", *convID, *model, *ollamaURL)
	fmt.Fprintf(hout, "turns=%d msgs=%d raw_tokens=%d\n", len(turns), len(msgs), rawTok)
	fmt.Fprintf(hout, "thresholds: activation_floor=%d segment_tokens=%d verbatim_recent=%d\n\n",
		*floor, *segTokens, *verbatim)

	summarizerCalls := 0
	summarize := func(ctx context.Context, m []llm.Message) (compaction.StructuredSummary, error) {
		summarizerCalls++
		// Mirrors compactSummarize in cmd/cercano/main.go: greedy decoding is
		// pinned so summaries are reproducible run to run.
		greedy := engine.Greedy()
		req := &agent.Request{Input: compaction.BuildSummaryPrompt(m), Temperature: greedy.Temperature}
		resp, err := provider.Process(ctx, req)
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		return compaction.ParseSummary(resp.Output), nil
	}

	cfg := compactor.Config{
		ActivationFloorTokens: *floor,
		SegmentTokens:         *segTokens,
		VerbatimRecent:        *verbatim,
	}
	state, err := store.GetCompaction(ctx, *convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetCompaction: %v\n", err)
		os.Exit(1)
	}
	state.ConversationID = *convID
	// Force a fresh compaction pass by clearing prior state — we want to test
	// what the summarizer actually produces on this input right now.
	state.FrozenThrough = 0
	state.SegmentSummariesJSON = ""
	state.ConsolidatedJSON = ""

	fmt.Fprintln(hout, "running compactor.Advance in a loop until more=false...")
	start := time.Now()
	var newState = state
	var changed, more bool
	passCount := 0
	for {
		passCount++
		passStart := time.Now()
		next, chg, m, err := compactor.Advance(ctx, turns, newState, summarize, cfg, tok)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nAdvance pass %d failed after %s: %v\n", passCount, time.Since(start), err)
			os.Exit(1)
		}
		fmt.Fprintf(hout, "pass=%d elapsed=%s summarizer_calls_total=%d changed=%v more=%v\n",
			passCount, time.Since(passStart), summarizerCalls, chg, m)
		newState = next
		if chg {
			changed = true
		}
		more = m
		if !more || !chg {
			break
		}
	}
	elapsed := time.Since(start)
	fmt.Fprintf(hout, "\ntotal_elapsed=%s summarizer_calls=%d passes=%d\n\n", elapsed, summarizerCalls, passCount)
	if !changed {
		fmt.Fprintln(hout, "compactor.Advance did not run — inputs may be below activation floor. Try -floor 500 or a longer conversation.")
		return
	}

	fmt.Fprintln(hout, "--- consolidated summary (what the compaction layer stored) ---")
	var consolidated compaction.StructuredSummary
	if newState.ConsolidatedJSON != "" {
		// Round-trip through the RenderBlock so we see it the way the model on
		// the receiving end would see it.
		_ = consolidated
	}
	// Re-parse for direct inspection.
	fmt.Fprintln(hout, newState.ConsolidatedJSON)
	fmt.Fprintln(hout)

	// Anchor / fabrication analysis over the JSON representation of the summary
	// (which contains every section's text).
	summary := strings.ToLower(newState.ConsolidatedJSON)
	fmt.Fprintln(hout, "--- grounding analysis ---")
	fmt.Fprintf(hout, "%-8s  %-40s  %s\n", "kind", "substring", "found?")
	fmt.Fprintf(hout, "%-8s  %-40s  %s\n", "--------", "----------------------------------------", "------")
	type anchorResult struct {
		Name string `json:"name"`
		Hit  bool   `json:"hit"`
	}
	type tellResult struct {
		Name       string `json:"name"`
		Fabricated bool   `json:"fabricated"`
		InSource   bool   `json:"in_source"`
	}
	var anchorResults []anchorResult
	var tellResults []tellResult
	pass := true
	for _, a := range splitCSV(*anchors) {
		hit := strings.Contains(summary, strings.ToLower(a))
		mark := "MISS"
		if hit {
			mark = "hit"
		} else {
			pass = false
		}
		anchorResults = append(anchorResults, anchorResult{Name: a, Hit: hit})
		fmt.Fprintf(hout, "%-8s  %-40s  %s\n", "anchor", a, mark)
	}
	// A fabrication tell is only valid if the phrase does NOT occur in the
	// window's source turns: a summary that quotes genuine source text must
	// never fail the audition for it. Tells found in the source are reported
	// but excluded from pass/fail scoring.
	var srcText strings.Builder
	for _, t := range turns {
		srcText.WriteString(strings.ToLower(t.Content))
		srcText.WriteString("\n")
		srcText.WriteString(strings.ToLower(t.BlocksJSON))
		srcText.WriteString("\n")
	}
	source := srcText.String()
	for _, t := range splitCSV(*tells) {
		inSource := strings.Contains(source, strings.ToLower(t))
		fab := strings.Contains(summary, strings.ToLower(t))
		mark := "clean"
		switch {
		case inSource && fab:
			mark = "in-source (quoted from window; not counted)"
			fab = false
		case inSource:
			mark = "in-source (invalid tell for this window)"
		case fab:
			mark = "FABRICATED"
			pass = false
		}
		tellResults = append(tellResults, tellResult{Name: t, Fabricated: fab, InSource: inSource})
		fmt.Fprintf(hout, "%-8s  %-40s  %s\n", "tell", t, mark)
	}
	if *jsonOut {
		result := struct {
			Model           string         `json:"model"`
			ConversationID  string         `json:"conversation_id"`
			Passes          int            `json:"passes"`
			SummarizerCalls int            `json:"summarizer_calls"`
			ElapsedSeconds  float64        `json:"elapsed_seconds"`
			Anchors         []anchorResult `json:"anchors"`
			Tells           []tellResult   `json:"tells"`
			Pass            bool           `json:"pass"`
			Summary         string         `json:"consolidated_summary_json"`
		}{
			Model: *model, ConversationID: *convID,
			Passes: passCount, SummarizerCalls: summarizerCalls,
			ElapsedSeconds: elapsed.Seconds(),
			Anchors:        anchorResults, Tells: tellResults,
			Pass: pass, Summary: newState.ConsolidatedJSON,
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
			os.Exit(1)
		}
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
