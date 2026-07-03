// Command ctx-usage-probe runs the exact GetContextUsage math on a given
// conversation and prints which branch of the switch it takes, plus the
// sent/raw values. Useful when the CLI's meter shows a number that doesn't
// match what elision + compaction should produce — this tells us if the
// server logic is fine (and the running binary is stale / caching) or if
// the switch is falling through the wrong branch.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/config"
)

func main() {
	convID := flag.String("conv", "80109e871fba4e18", "conversation id")
	flag.Parse()

	dbPath := os.ExpandEnv("$HOME/.config/cercano/conversations.db")
	store, err := conversation.Open(dbPath)
	if err != nil {
		fmt.Println("open:", err)
		return
	}

	cfg, err := config.Load(os.ExpandEnv("$HOME/.config/cercano/config.yaml"))
	if err != nil {
		fmt.Println("config load:", err)
		return
	}
	elide := cfg.Compaction.ElideToolResults
	lossy := cfg.Compaction.LossyToolElision
	fmt.Printf("config: elide=%v lossy=%v enabled=%v\n\n", elide, lossy, cfg.Compaction.Enabled)

	turns, err := store.GetTurns(context.Background(), *convID)
	if err != nil {
		fmt.Println("getturns:", err)
		return
	}
	fmt.Printf("turns loaded: %d\n", len(turns))

	state, _ := store.GetCompaction(context.Background(), *convID)
	fmt.Printf("state.ConsolidatedJSON: %d bytes\n", len(state.ConsolidatedJSON))
	fmt.Printf("state.FrozenThrough: %d\n\n", state.FrozenThrough)

	// Match the exact estimateRawTokens logic from server.go.
	rawBytes := 0
	for _, t := range turns {
		if len(t.BlocksJSON) > len(t.Content) {
			rawBytes += len(t.BlocksJSON)
		} else {
			rawBytes += len(t.Content)
		}
	}
	raw := (rawBytes + 3) / 4
	fmt.Printf("estimateRawTokens raw: %d\n\n", raw)

	// Now the switch.
	tok := contextmeter.Default()
	var sent int
	var branch string
	switch {
	case state.ConsolidatedJSON != "":
		branch = "case ConsolidatedJSON (compaction has run)"
		view, verr := compactor.BuildSendView(turns, state)
		fmt.Printf("BuildSendView err: %v, messages: %d\n", verr, len(view))
		if elide {
			var n int
			view, n = compaction.ElideSupersededToolResults(view)
			fmt.Printf("after ElideSupersededToolResults: %d messages, %d stubbed\n", len(view), n)
		}
		if lossy {
			var n int
			view, n = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
			fmt.Printf("after KeepLastNToolResults(%d): %d messages, %d stubbed\n",
				compaction.DefaultLossyElisionKeepLast, len(view), n)
		}
		sent = compaction.TotalTokens(tok, view)
	case elide || lossy:
		branch = "case elide||lossy (no compaction row)"
		view := agent.BuildLLMHistory(turns)
		fmt.Printf("BuildLLMHistory messages: %d\n", len(view))
		if elide {
			view, _ = compaction.ElideSupersededToolResults(view)
		}
		if lossy {
			view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
		}
		sent = compaction.TotalTokens(tok, view)
	default:
		branch = "default (fast path — sent = raw)"
		sent = raw
	}
	fmt.Printf("\nbranch: %s\n", branch)
	fmt.Printf("sent: %d\n", sent)
	fmt.Printf("raw : %d\n", raw)
	fmt.Printf("ratio: %.1f%%\n", 100.0*float64(sent)/float64(raw))
}
