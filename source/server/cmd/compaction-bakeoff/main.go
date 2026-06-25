// Command compaction-bakeoff scores the compaction contenders against a real
// model by routing summarization through a running cercano agent. It is a
// validation tool, not part of the test suite.
//
// Usage: compaction-bakeoff -addr localhost:50051
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/agentclient"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "running cercano agent gRPC address")
	flag.Parse()

	ctx := context.Background()
	client, err := agentclient.Dial(ctx, *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial agent at %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer client.Close()

	summarize := agentSummarizer(client)
	tok := contextmeter.Default()
	// SegmentTokens is sized to the small illustrative corpus (older spans are
	// tens-to-hundreds of tokens) so the older history actually splits into
	// multiple segments — otherwise every fixture is one segment and
	// map-reduce/model degenerates to map-reduce/mechanical, making the B-vs-C
	// comparison inert. A production run over large real conversations would use
	// a realistic budget (e.g. 32000).
	budget := compaction.Budget{VerbatimRecent: 4, SegmentTokens: 40}

	contenders := []compaction.Compactor{
		compaction.RollingCompactor{},
		compaction.MapReduceCompactor{ModelReduce: false},
		compaction.MapReduceCompactor{ModelReduce: true},
	}

	fmt.Printf("%-22s %-18s %8s %10s %8s %6s %6s\n",
		"contender", "fixture", "reduce", "anchors", "dedup", "valid", "calls")
	invalid := false
	for _, c := range contenders {
		for _, f := range compaction.Corpus() {
			m, err := compaction.Score(ctx, c, f, summarize, tok, budget)
			if err != nil {
				fmt.Printf("%-22s %-18s  ERROR: %v\n", c.Name(), f.Name, err)
				continue
			}
			if !m.PairingValid {
				invalid = true
			}
			fmt.Printf("%-22s %-18s %7.0f%% %6d/%-3d %8d %6v %6d\n",
				c.Name(), f.Name, m.Reduction*100,
				m.AnchorsKept, m.AnchorsTotal, m.DedupCollapsed, m.PairingValid, m.ModelCalls)
		}
	}
	if invalid {
		fmt.Fprintln(os.Stderr, "FAIL: at least one send-view was pairing-invalid")
		os.Exit(1)
	}
}

// agentSummarizer builds a SummarizeFunc that sends the summary prompt through
// the agent and parses the streamed response.
func agentSummarizer(client *agentclient.Client) compaction.SummarizeFunc {
	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		prompt := compaction.BuildSummaryPrompt(msgs)
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		ch, err := client.StreamChat(cctx, "", prompt, "")
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		var out strings.Builder
		for m := range ch {
			switch m.Type {
			case agentclient.TypeToken:
				out.WriteString(m.Token)
			case agentclient.TypeDone:
				if m.Final != "" {
					out.Reset()
					out.WriteString(m.Final)
				}
			case agentclient.TypeError:
				return compaction.StructuredSummary{}, m.Err
			}
		}
		return compaction.ParseSummary(out.String()), nil
	}
}
