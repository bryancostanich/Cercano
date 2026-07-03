// Command compaction-bakeoff scores the compaction contenders against a real
// model by routing summarization through a running cercano agent. It is a
// validation tool, not part of the test suite.
//
// Synthetic corpus:  compaction-bakeoff -addr localhost:50052
//
//	Real session:      compaction-bakeoff -addr localhost:50052 \
//	                       -transcript ~/.claude/projects/<proj>/<id>.jsonl \
//	                       -maxtokens 150000 -anchors "goal phrase,key file.go"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/agentclient"
)

func contenders() []compaction.Compactor {
	return []compaction.Compactor{
		compaction.RollingCompactor{},
		compaction.MapReduceCompactor{},
	}
}

func main() {
	addr := flag.String("addr", "localhost:50052", "running cercano agent gRPC address")
	transcript := flag.String("transcript", "", "path to a Claude Code JSONL session to use as a real fixture (instead of the synthetic corpus)")
	maxTokens := flag.Int("maxtokens", 150000, "for -transcript: slice the session to ~this many tokens from the start")
	segTokens := flag.Int("segtokens", 8000, "for -transcript: per-segment token budget (keep under the local model's context window)")
	verbatim := flag.Int("verbatim", 6, "for -transcript: number of trailing messages kept verbatim")
	anchors := flag.String("anchors", "", "for -transcript/-conv: comma-separated must-keep substrings to score retention")
	statsDir := flag.String("statsdir", "", "analyze token/turn/tool distributions across all .jsonl transcripts under this dir (no model needed); prints stats and exits")
	conv := flag.String("conv", "", "matrix mode: score every frame over this stored conversation id (talks to Ollama directly, no agent needed)")
	dbPath := flag.String("db", os.ExpandEnv("$HOME/.config/cercano/conversations.db"), "for -conv: conversations database")
	aroundTurn := flag.String("aroundturn", "", "for -conv: slice a window around this turn id (default: whole conversation)")
	before := flag.Int("before", 40, "for -conv with -aroundturn: turns of context before the target")
	after := flag.Int("after", 10, "for -conv with -aroundturn: turns of context after the target")
	model := flag.String("model", "qwen3-coder-next:latest", "for -conv: Ollama model tag")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "for -conv: Ollama base URL")
	csvPath := flag.String("csv", "", "for -conv: append machine-readable result rows to this CSV")
	perFrame := flag.Duration("frametimeout", 30*time.Minute, "for -conv: per-frame timeout")
	repeat := flag.Int("repeat", 1, "for -conv: matrix repetitions (LLM frames are sampled; one run is one sample)")
	dumpDir := flag.String("dumpdir", "", "for -conv: save every prompt/response pair to this directory")
	flag.Parse()

	// Stats mode is deterministic and needs no agent — handle before dialing.
	if *statsDir != "" {
		runStats(*statsDir)
		return
	}

	// Matrix mode talks to Ollama directly — also no agent.
	if *conv != "" {
		var must []string
		for _, a := range strings.Split(*anchors, ",") {
			if a = strings.TrimSpace(a); a != "" {
				must = append(must, a)
			}
		}
		err := runMatrix(context.Background(), matrixConfig{
			DBPath:     *dbPath,
			ConvID:     *conv,
			AroundTurn: *aroundTurn,
			Before:     *before,
			After:      *after,
			Model:      *model,
			OllamaURL:  *ollamaURL,
			SegTokens:  *segTokens,
			Verbatim:   *verbatim,
			Anchors:    must,
			CSVPath:    *csvPath,
			PerFrame:   *perFrame,
			Repeat:     *repeat,
			DumpDir:    *dumpDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "matrix: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx := context.Background()
	client, err := agentclient.Dial(ctx, *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial agent at %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer client.Close()

	summarize := agentSummarizer(client)
	tok := contextmeter.Default()

	if *transcript != "" {
		runTranscript(ctx, summarize, tok, *transcript, *maxTokens, *segTokens, *verbatim, *anchors)
		return
	}
	runCorpus(ctx, summarize, tok)
}

// runCorpus scores every contender over the synthetic fixture corpus.
func runCorpus(ctx context.Context, summarize compaction.SummarizeFunc, tok contextmeter.Tokenizer) {
	// SegmentTokens is sized to the small illustrative corpus (older spans are
	// tens-to-hundreds of tokens) so the older history actually splits into
	// multiple segments — otherwise every fixture is one segment and
	// map-reduce/model degenerates to map-reduce/mechanical, making the B-vs-C
	// comparison inert. The -transcript path uses a realistic budget instead.
	budget := compaction.Budget{VerbatimRecent: 4, SegmentTokens: 40}

	fmt.Printf("%-22s %-18s %8s %10s %8s %6s %6s\n",
		"contender", "fixture", "reduce", "anchors", "dedup", "valid", "calls")
	invalid := false
	for _, c := range contenders() {
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

// runTranscript runs every contender over one real Claude Code session and
// prints metrics plus each contender's final summary side by side, so the
// quality difference (did compounding loss erode the thread?) is legible.
func runTranscript(ctx context.Context, summarize compaction.SummarizeFunc, tok contextmeter.Tokenizer, path string, maxTokens, segTokens, verbatim int, anchorsCSV string) {
	msgs, err := LoadTranscript(path, maxTokens, tok)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load transcript: %v\n", err)
		os.Exit(1)
	}
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "transcript produced no convertible messages")
		os.Exit(1)
	}
	var anchors []string
	for _, a := range strings.Split(anchorsCSV, ",") {
		if a = strings.TrimSpace(a); a != "" {
			anchors = append(anchors, a)
		}
	}

	budget := compaction.Budget{VerbatimRecent: verbatim, SegmentTokens: segTokens}
	rawTok := compaction.TotalTokens(tok, msgs)
	fmt.Printf("transcript %s: %d messages, ~%d tokens (sliced to <=%d), seg=%d verbatim=%d, %d anchors\n\n",
		filepath.Base(path), len(msgs), rawTok, maxTokens, segTokens, verbatim, len(anchors))

	for _, c := range contenders() {
		calls := 0
		counted := func(cx context.Context, m []llm.Message) (compaction.StructuredSummary, error) {
			calls++
			return summarize(cx, m)
		}
		res, err := c.Compact(ctx, msgs, counted, budget)
		if err != nil {
			fmt.Printf("== %s ==\n  ERROR: %v\n\n", c.Name(), err)
			continue
		}
		sentTok := compaction.TotalTokens(tok, res.SendView)
		reduction := 0.0
		if rawTok > 0 {
			reduction = 1 - float64(sentTok)/float64(rawTok)
		}
		flat := flattenSendView(res.SendView)
		kept := 0
		for _, a := range anchors {
			if strings.Contains(flat, a) {
				kept++
			}
		}
		fmt.Printf("== %s ==\n", c.Name())
		fmt.Printf("  reduction=%.0f%%  calls=%d  pairingValid=%v", reduction*100, calls, llm.IsValidPairing(res.SendView))
		if len(anchors) > 0 {
			fmt.Printf("  anchors=%d/%d", kept, len(anchors))
		}
		fmt.Print("\n  --- final summary ---\n")
		if len(res.Summaries) > 0 {
			for _, line := range strings.Split(res.Summaries[0].RenderBlock().Text, "\n") {
				fmt.Println("  " + line)
			}
		}
		fmt.Println()
	}
}

func flattenSendView(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
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
