// Command proposal-repro tests the summarizer fidelity fix on a specific
// turn window inside a real conversation, without running the full
// compaction pipeline. It slices ±windowBefore..+windowAfter turns around
// a target turn ID, feeds them through BuildSummaryPrompt via Ollama, and
// reports whether the anchor substrings appear in the resulting summary.
//
// This is the fast acceptance check for the PROPOSALS-slot + fidelity-rules
// change in summarizer.go — the full-conversation compactor.Advance run
// takes hours on qwen3-coder-next:latest, so we don't want it in the
// feedback loop. If the tiers-proposal keywords survive here, the prompt
// fix works for that specific proposal.
//
// Typical use:
//
//	# Test whether the tiers-proposal keywords survive.
//	go run ./cmd/proposal-repro -conv 80109e871fba4e18 \
//	  -turn adfada03679f33c3a13fa50a \
//	  -before 3 -after 3 \
//	  -anchors 'most_capable,fast_light,models.Resolve,default_provider,tier'
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
	"cercano/source/server/internal/conversation"
)

func main() {
	convID := flag.String("conv", "80109e871fba4e18", "conversation id")
	turnID := flag.String("turn", "adfada03679f33c3a13fa50a", "target turn id (the proposal turn)")
	before := flag.Int("before", 3, "turns of context before the target")
	after := flag.Int("after", 3, "turns of context after the target")
	model := flag.String("model", "qwen3-coder-next:latest", "Ollama model tag")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "Ollama base URL")
	anchors := flag.String("anchors",
		"most_capable,fast_light,models.Resolve,default_provider,tier,workhorse,frontier,open-weight",
		"comma-separated substrings that SHOULD appear in the summary")
	timeout := flag.Duration("timeout", 5*time.Minute, "run timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	dbPath := os.ExpandEnv("$HOME/.config/cercano/conversations.db")
	store, err := conversation.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	allTurns, err := store.GetTurns(ctx, *convID)
	if err != nil || len(allTurns) == 0 {
		fmt.Fprintf(os.Stderr, "getturns: %v (n=%d)\n", err, len(allTurns))
		os.Exit(1)
	}

	targetIdx := -1
	for i, t := range allTurns {
		if t.ID == *turnID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		fmt.Fprintf(os.Stderr, "turn %s not found in conversation %s (has %d turns)\n",
			*turnID, *convID, len(allTurns))
		os.Exit(1)
	}

	start := targetIdx - *before
	if start < 0 {
		start = 0
	}
	end := targetIdx + *after + 1
	if end > len(allTurns) {
		end = len(allTurns)
	}
	window := allTurns[start:end]
	fmt.Printf("conv=%s target=%s targetIdx=%d window=[%d..%d) size=%d\n",
		*convID, *turnID, targetIdx, start, end, len(window))

	msgs := agent.BuildLLMHistory(window)
	prompt := compaction.BuildSummaryPrompt(msgs)
	fmt.Printf("prompt_bytes=%d messages=%d\n\n", len(prompt), len(msgs))

	ollamaBase, err := url.Parse(*ollamaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad ollama url: %v\n", err)
		os.Exit(1)
	}
	ollama := ollamaapi.NewClient(ollamaBase, http.DefaultClient)

	fmt.Println("--- calling summarizer ---")
	callStart := time.Now()
	var out strings.Builder
	stream := false
	req := &ollamaapi.GenerateRequest{Model: *model, Prompt: prompt, Stream: &stream}
	err = ollama.Generate(ctx, req, func(r ollamaapi.GenerateResponse) error {
		out.WriteString(r.Response)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate failed after %s: %v\n", time.Since(callStart), err)
		os.Exit(1)
	}
	fmt.Printf("elapsed=%s response_bytes=%d\n\n", time.Since(callStart), out.Len())

	raw := out.String()
	fmt.Println("--- raw model output ---")
	fmt.Println(raw)
	fmt.Println()

	fmt.Println("--- parsed summary ---")
	sum := compaction.ParseSummary(raw)
	fmt.Printf("Goal: %s\n", sum.Goal)
	fmt.Printf("Decisions (%d):\n", len(sum.Decisions))
	for _, d := range sum.Decisions {
		fmt.Printf("  - %s\n", d)
	}
	fmt.Printf("Proposals (%d):\n", len(sum.Proposals))
	for _, p := range sum.Proposals {
		fmt.Printf("  - %s\n", p)
	}
	fmt.Printf("Files (%d)\n", len(sum.Files))
	fmt.Printf("OpenThreads (%d):\n", len(sum.OpenThreads))
	for _, o := range sum.OpenThreads {
		fmt.Printf("  - %s\n", o)
	}
	fmt.Printf("State: %s\n\n", sum.State)

	fmt.Println("--- grounding analysis ---")
	lc := strings.ToLower(raw)
	fmt.Printf("%-8s  %-30s  %s\n", "kind", "substring", "found?")
	for _, a := range splitCSV(*anchors) {
		mark := "MISS"
		if strings.Contains(lc, strings.ToLower(a)) {
			mark = "hit"
		}
		fmt.Printf("%-8s  %-30s  %s\n", "anchor", a, mark)
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
