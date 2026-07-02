package main

import (
	"context"
	"fmt"
	"os"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func main() {
	path := os.ExpandEnv("$HOME/.config/cercano/conversations.db")
	store, err := conversation.Open(path)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	convID := "80109e871fba4e18"
	if len(os.Args) > 1 {
		convID = os.Args[1]
	}
	turns, err := store.GetTurns(context.Background(), convID)
	if err != nil {
		fmt.Println("getturns:", err)
		return
	}
	msgs := agent.BuildLLMHistory(turns)
	tok := contextmeter.Default()
	rawTok := compaction.TotalTokens(tok, msgs)

	// Count tool_result blocks so the K choices are meaningful.
	nToolResults := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				nToolResults++
			}
		}
	}

	fmt.Printf("conv=%s\n", convID)
	fmt.Printf("turns=%d msgs=%d tool_result_blocks=%d\n", len(turns), len(msgs), nToolResults)
	fmt.Printf("raw tokens (tiktoken)=%d\n\n", rawTok)

	fmt.Printf("%-32s  %10s  %10s  %8s  %10s\n", "policy", "tokens", "saved", "saved%", "stubbed")
	fmt.Printf("%-32s  %10s  %10s  %8s  %10s\n",
		"--------------------------------", "----------", "----------", "--------", "----------")

	// Baseline: no elision.
	fmt.Printf("%-32s  %10d  %10d  %7.1f%%  %10d\n", "none (baseline)", rawTok, 0, 0.0, 0)

	// Current impl: byte-identical dedup.
	elided, collapsed := compaction.ElideSupersededToolResults(msgs)
	et := compaction.TotalTokens(tok, elided)
	fmt.Printf("%-32s  %10d  %10d  %7.1f%%  %10d\n",
		"current: byte-identical dedup", et, rawTok-et, 100.0*float64(rawTok-et)/float64(rawTok), collapsed)

	// Wider: keep last K tool_result blocks in full, stub older. Uses the
	// exported compaction.KeepLastNToolResults so the probe reflects what
	// the running server actually does when lossy_tool_elision is on.
	for _, k := range []int{50, 30, compaction.DefaultLossyElisionKeepLast, 10, 5, 0} {
		wide, stubs := compaction.KeepLastNToolResults(msgs, k)
		wt := compaction.TotalTokens(tok, wide)
		label := fmt.Sprintf("keep-last-N=%d", k)
		if k == compaction.DefaultLossyElisionKeepLast {
			label += " (default)"
		}
		fmt.Printf("%-32s  %10d  %10d  %7.1f%%  %10d\n",
			label, wt, rawTok-wt, 100.0*float64(rawTok-wt)/float64(rawTok), stubs)
	}
}
