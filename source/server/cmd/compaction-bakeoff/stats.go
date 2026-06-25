package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cercano/source/server/internal/llm"
)

// fastTok is a cheap len/4 token estimate. Accurate enough for distribution
// shape, and avoids tiktoken's cost over hundreds of large transcripts.
type fastTok struct{}

func (fastTok) Count(s string) int { return (len(s) + 3) / 4 }

// runStats scans every .jsonl transcript under dir and prints the token / turn /
// tool-result distributions used to pick data-driven compaction defaults. It
// needs no model and no agent.
func runStats(dir string) {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no .jsonl files under %s\n", dir)
		os.Exit(1)
	}

	tok := fastTok{}
	var sessTokens, sessTurns, perTurnTokens, avgPerTurn []int
	var totalAll, toolAll int64
	skipped := 0

	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			skipped++
			continue
		}
		msgs := parseTranscript(file, 0, tok)
		file.Close()
		if len(msgs) == 0 {
			skipped++
			continue
		}
		st := 0
		for _, m := range msgs {
			mt := 0
			for _, b := range m.Blocks {
				mt += tok.Count(b.Text) + tok.Count(b.Content) + tok.Count(string(b.ToolInput))
				if b.Type == llm.BlockToolResult {
					toolAll += int64(tok.Count(b.Content))
				}
			}
			perTurnTokens = append(perTurnTokens, mt)
			st += mt
		}
		sessTokens = append(sessTokens, st)
		sessTurns = append(sessTurns, len(msgs))
		avgPerTurn = append(avgPerTurn, st/max1(len(msgs)))
		totalAll += int64(st)
	}

	sort.Ints(sessTokens)
	sort.Ints(sessTurns)
	sort.Ints(perTurnTokens)
	sort.Ints(avgPerTurn)

	fmt.Printf("corpus: %d transcripts converted (%d skipped/empty), %d total turns\n\n",
		len(sessTokens), skipped, len(perTurnTokens))

	fmt.Println("session size (tokens):")
	printPcts(sessTokens)
	fmt.Println("\nturns per session:")
	printPcts(sessTurns)
	fmt.Println("\ntokens per turn:")
	printPcts(perTurnTokens)
	fmt.Println("\navg tokens/turn per session (growth rate):")
	printPcts(avgPerTurn)

	if totalAll > 0 {
		fmt.Printf("\ntool_result tokens: %.0f%% of all tokens\n", 100*float64(toolAll)/float64(totalAll))
	}
}

func printPcts(sorted []int) {
	fmt.Printf("  min=%d  p25=%d  p50=%d  p75=%d  p90=%d  p99=%d  max=%d\n",
		pct(sorted, 0), pct(sorted, 0.25), pct(sorted, 0.50),
		pct(sorted, 0.75), pct(sorted, 0.90), pct(sorted, 0.99), pct(sorted, 1.0))
}

func pct(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
