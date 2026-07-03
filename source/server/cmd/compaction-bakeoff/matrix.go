package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// frame pairs a contender with the prompt it was designed for. Deterministic
// contenders have a nil prompt and receive a nil SummarizeFunc.
type frame struct {
	compactor compaction.Compactor
	prompt    func([]llm.Message) string
}

// frames returns the experiment matrix from the external survey
// (docs/research/compaction/external-survey.md): A baseline, B adaptive,
// C elision-first, D extractive, E retrieval-backed.
func frames() []frame {
	return []frame{
		{compaction.RollingCompactor{}, compaction.BuildSummaryPrompt},
		{compaction.AdaptiveCompactor{}, compaction.BuildAdaptivePrompt},
		{compaction.ElisionFirstCompactor{}, nil},
		{compaction.ExtractiveCompactor{}, compaction.BuildExtractivePrompt},
		{compaction.RetrievalCompactor{}, compaction.BuildSummaryPrompt},
	}
}

// matrixConfig holds the -conv mode's knobs.
type matrixConfig struct {
	DBPath     string
	ConvID     string
	AroundTurn string // optional turn id; empty means the whole conversation
	Before     int    // turns of context before AroundTurn
	After      int    // turns of context after AroundTurn
	Model      string
	OllamaURL  string
	SegTokens  int
	Verbatim   int
	Anchors    []string
	CSVPath    string
	PerFrame   time.Duration
}

// runMatrix scores every frame over one stored conversation window and prints
// a table; with CSVPath set it also appends machine-readable rows.
func runMatrix(ctx context.Context, cfg matrixConfig) error {
	msgs, err := loadConvWindow(ctx, cfg)
	if err != nil {
		return err
	}
	tok := contextmeter.Default()
	rawTok := compaction.TotalTokens(tok, msgs)
	rawFlat := flattenSendView(msgs)
	fmt.Printf("conv=%s window=%d messages ~%d tokens seg=%d verbatim=%d anchors=%d model=%s\n\n",
		cfg.ConvID, len(msgs), rawTok, cfg.SegTokens, cfg.Verbatim, len(cfg.Anchors), cfg.Model)

	base, err := url.Parse(cfg.OllamaURL)
	if err != nil {
		return fmt.Errorf("bad ollama url: %w", err)
	}

	var rows [][]string
	fmt.Printf("%-20s %8s %10s %10s %8s %6s %8s\n",
		"frame", "reduce", "anchors", "grounded", "calls", "valid", "elapsed")
	for _, f := range frames() {
		var summarize compaction.SummarizeFunc
		calls := 0
		if f.prompt != nil {
			inner := ollamaSummarizer(base, cfg.Model, f.prompt)
			summarize = func(cx context.Context, m []llm.Message) (compaction.StructuredSummary, error) {
				calls++
				return inner(cx, m)
			}
		}

		fctx, cancel := context.WithTimeout(ctx, cfg.PerFrame)
		start := time.Now()
		res, err := f.compactor.Compact(fctx, msgs, summarize,
			compaction.Budget{VerbatimRecent: cfg.Verbatim, SegmentTokens: cfg.SegTokens})
		elapsed := time.Since(start)
		cancel()
		name := f.compactor.Name()
		if err != nil {
			fmt.Printf("%-20s ERROR after %s: %v\n", name, elapsed.Round(time.Second), err)
			rows = append(rows, []string{name, cfg.ConvID, "ERROR", err.Error()})
			continue
		}

		sent := compaction.TotalTokens(tok, res.SendView)
		reduction := 0.0
		if rawTok > 0 {
			reduction = 1 - float64(sent)/float64(rawTok)
		}
		flat := flattenSendView(res.SendView)
		kept := 0
		for _, a := range cfg.Anchors {
			if strings.Contains(flat, a) {
				kept++
			}
		}
		grounded, bullets := 0, 0
		for _, s := range res.Summaries {
			g, n := compaction.GroundedBullets(s, rawFlat)
			grounded += g
			bullets += n
		}
		groundedCol := "-"
		if bullets > 0 {
			groundedCol = fmt.Sprintf("%d/%d", grounded, bullets)
		}
		valid := llm.IsValidPairing(res.SendView)
		fmt.Printf("%-20s %7.0f%% %7d/%-2d %10s %8d %6v %8s\n",
			name, reduction*100, kept, len(cfg.Anchors), groundedCol, calls, valid,
			elapsed.Round(time.Second))
		rows = append(rows, []string{
			name, cfg.ConvID,
			fmt.Sprint(rawTok), fmt.Sprint(sent), fmt.Sprintf("%.3f", reduction),
			fmt.Sprint(kept), fmt.Sprint(len(cfg.Anchors)),
			fmt.Sprint(grounded), fmt.Sprint(bullets),
			fmt.Sprint(calls), fmt.Sprint(valid),
			fmt.Sprintf("%.1f", elapsed.Seconds()),
		})
	}

	if cfg.CSVPath != "" {
		if err := appendCSV(cfg.CSVPath, rows); err != nil {
			return fmt.Errorf("write csv: %w", err)
		}
		fmt.Printf("\nrows appended to %s\n", cfg.CSVPath)
	}
	return nil
}

// loadConvWindow loads the conversation's LLM history, optionally sliced to a
// window around one turn (same addressing proposal-repro uses).
func loadConvWindow(ctx context.Context, cfg matrixConfig) ([]llm.Message, error) {
	store, err := conversation.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.DBPath, err)
	}
	turns, err := store.GetTurns(ctx, cfg.ConvID)
	if err != nil {
		return nil, fmt.Errorf("getturns: %w", err)
	}
	if len(turns) == 0 {
		return nil, fmt.Errorf("conversation %s has no turns", cfg.ConvID)
	}
	if cfg.AroundTurn != "" {
		target := -1
		for i, t := range turns {
			if t.ID == cfg.AroundTurn {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, fmt.Errorf("turn %s not found in %s (%d turns)", cfg.AroundTurn, cfg.ConvID, len(turns))
		}
		lo := max(0, target-cfg.Before)
		hi := min(len(turns), target+cfg.After+1)
		turns = turns[lo:hi]
	}
	return agent.BuildLLMHistory(turns), nil
}

// ollamaSummarizer builds a SummarizeFunc that renders the frame's prompt and
// calls Ollama directly — no running agent needed.
func ollamaSummarizer(base *url.URL, model string, build func([]llm.Message) string) compaction.SummarizeFunc {
	client := ollamaapi.NewClient(base, http.DefaultClient)
	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		stream := false
		var out strings.Builder
		req := &ollamaapi.GenerateRequest{Model: model, Prompt: build(msgs), Stream: &stream}
		err := client.Generate(ctx, req, func(r ollamaapi.GenerateResponse) error {
			out.WriteString(r.Response)
			return nil
		})
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		return compaction.ParseSummary(out.String()), nil
	}
}

// appendCSV appends rows, writing the header first when the file is new.
func appendCSV(path string, rows [][]string) error {
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if os.IsNotExist(statErr) {
		if err := w.Write([]string{
			"frame", "conv", "raw_tokens", "sent_tokens", "reduction",
			"anchors_kept", "anchors_total", "grounded", "bullets",
			"model_calls", "pairing_valid", "elapsed_s",
		}); err != nil {
			return err
		}
	}
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}
