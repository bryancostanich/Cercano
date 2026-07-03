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
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
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
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	dbPath := os.ExpandEnv("$HOME/.config/cercano/conversations.db")
	store, err := conversation.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	turns, err := store.GetTurns(ctx, *convID)
	if err != nil || len(turns) == 0 {
		fmt.Fprintf(os.Stderr, "getturns %s: %v (or empty)\n", *convID, err)
		os.Exit(1)
	}

	ollamaBase, err := url.Parse(*ollamaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad ollama url %q: %v\n", *ollamaURL, err)
		os.Exit(1)
	}
	ollama := ollamaapi.NewClient(ollamaBase, http.DefaultClient)

	msgs := agent.BuildLLMHistory(turns)
	tok := contextmeter.Default()
	rawTok := compaction.TotalTokens(tok, msgs)

	fmt.Printf("conv=%s model=%s ollama=%s\n", *convID, *model, *ollamaURL)
	fmt.Printf("turns=%d msgs=%d raw_tokens=%d\n", len(turns), len(msgs), rawTok)
	fmt.Printf("thresholds: activation_floor=%d segment_tokens=%d verbatim_recent=%d\n\n",
		*floor, *segTokens, *verbatim)

	summarizerCalls := 0
	summarize := func(ctx context.Context, m []llm.Message) (compaction.StructuredSummary, error) {
		summarizerCalls++
		prompt := compaction.BuildSummaryPrompt(m)
		var out strings.Builder
		stream := false
		req := &ollamaapi.GenerateRequest{Model: *model, Prompt: prompt, Stream: &stream}
		err := ollama.Generate(ctx, req, func(r ollamaapi.GenerateResponse) error {
			out.WriteString(r.Response)
			return nil
		})
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		return compaction.ParseSummary(out.String()), nil
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

	fmt.Println("running compactor.Advance in a loop until more=false...")
	start := time.Now()
	var newState = state
	var changed, more bool
	pass := 0
	for {
		pass++
		passStart := time.Now()
		next, chg, m, err := compactor.Advance(ctx, turns, newState, summarize, cfg, tok)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nAdvance pass %d failed after %s: %v\n", pass, time.Since(start), err)
			os.Exit(1)
		}
		fmt.Printf("pass=%d elapsed=%s summarizer_calls_total=%d changed=%v more=%v\n",
			pass, time.Since(passStart), summarizerCalls, chg, m)
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
	fmt.Printf("\ntotal_elapsed=%s summarizer_calls=%d passes=%d\n\n", elapsed, summarizerCalls, pass)
	if !changed {
		fmt.Println("compactor.Advance did not run — inputs may be below activation floor. Try -floor 500 or a longer conversation.")
		return
	}

	fmt.Println("--- consolidated summary (what the compaction layer stored) ---")
	var consolidated compaction.StructuredSummary
	if newState.ConsolidatedJSON != "" {
		// Round-trip through the RenderBlock so we see it the way the model on
		// the receiving end would see it.
		_ = consolidated
	}
	// Re-parse for direct inspection.
	fmt.Println(newState.ConsolidatedJSON)
	fmt.Println()

	// Anchor / fabrication analysis over the JSON representation of the summary
	// (which contains every section's text).
	summary := strings.ToLower(newState.ConsolidatedJSON)
	fmt.Println("--- grounding analysis ---")
	fmt.Printf("%-8s  %-40s  %s\n", "kind", "substring", "found?")
	fmt.Printf("%-8s  %-40s  %s\n", "--------", "----------------------------------------", "------")
	for _, a := range splitCSV(*anchors) {
		mark := "MISS"
		if strings.Contains(summary, strings.ToLower(a)) {
			mark = "hit"
		}
		fmt.Printf("%-8s  %-40s  %s\n", "anchor", a, mark)
	}
	for _, t := range splitCSV(*tells) {
		mark := "clean"
		if strings.Contains(summary, strings.ToLower(t)) {
			mark = "FABRICATED"
		}
		fmt.Printf("%-8s  %-40s  %s\n", "tell", t, mark)
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
