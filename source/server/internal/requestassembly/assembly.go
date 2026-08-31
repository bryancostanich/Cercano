// Package requestassembly builds the provider-facing conversation send view for
// a concrete model attempt and reports deterministic token accounting for that
// view.
package requestassembly

import (
	"encoding/json"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// Target describes the concrete provider/model attempt the send view is being
// assembled for. ContextWindow overrides the model-name lookup when the caller
// knows the runtime window that will actually be served.
type Target struct {
	RouteLabel         string
	Provider           string
	Model              string
	Tier               string
	ContextWindow      int
	ContextWindowKnown bool
	TightContext       bool
}

// Accounting describes how the raw persisted turns became the final send view.
// Counts use ProviderTotalTokens because this is provider-facing accounting, not
// raw storage pressure.
type Accounting struct {
	RawTokens              int
	Window                 int
	WindowKnown            bool
	HardLimit              int
	InitialTokens          int
	AfterHardElide         int
	AfterKeepLast          int
	FinalTokens            int
	MessageTokens          int
	SystemTokens           int
	ToolSchemaTokens       int
	OutputReserveTokens    int
	EstimatedRequestTokens int
	DroppedMessages        int
	Scheduled              bool
	Truncated              bool
}

// Result is the assembled send view plus its accounting.
type Result struct {
	Messages   []llm.Message
	Accounting Accounting
}

// EstimateRawTokens is a fast len/4 token estimate over the turns' text. It is
// intentionally cheap for frequent UI polling and raw/savings display.
func EstimateRawTokens(turns []conversation.Turn) int {
	n := 0
	for _, t := range turns {
		if len(t.BlocksJSON) > len(t.Content) {
			n += len(t.BlocksJSON)
		} else {
			n += len(t.Content)
		}
	}
	return (n + 3) / 4
}

// WindowFor returns target.ContextWindow when set, otherwise the conventional
// context window for target.Model.
func WindowFor(target Target) int {
	window, _ := WindowForTarget(target)
	return window
}

// WindowForTarget returns the context window for target plus whether that value
// came from known metadata or an explicit concrete target window.
func WindowForTarget(target Target) (int, bool) {
	if target.ContextWindow > 0 {
		return target.ContextWindow, target.ContextWindowKnown
	}
	mw := contextmeter.ModelWindowFor(target.Model)
	return mw.Tokens, mw.Known
}

// RequestEstimateInput carries provider-agnostic request components available
// before a provider call.
type RequestEstimateInput struct {
	Messages      []llm.Message
	System        string
	Tools         []llm.Tool
	OutputReserve int
}

// EstimateFullRequest fills full-request accounting fields on acct using the
// same tokenizer family as send-view assembly. Counts are estimates: provider
// tokenizers and wire formats can differ.
func EstimateFullRequest(acct Accounting, in RequestEstimateInput, tok contextmeter.Tokenizer) Accounting {
	if tok == nil {
		tok = contextmeter.Default()
	}
	acct.MessageTokens = acct.FinalTokens
	if acct.MessageTokens == 0 && len(in.Messages) > 0 {
		acct.MessageTokens = compaction.ProviderTotalTokens(tok, in.Messages)
	}
	acct.SystemTokens = tok.Count(in.System)
	acct.ToolSchemaTokens = EstimateToolSchemaTokens(tok, in.Tools)
	acct.OutputReserveTokens = in.OutputReserve
	acct.EstimatedRequestTokens = acct.MessageTokens + acct.SystemTokens + acct.ToolSchemaTokens + acct.OutputReserveTokens
	return acct
}

// EstimateToolSchemaTokens estimates the token cost of the advertised native
// tool schemas without logging or exposing the schema bodies themselves.
func EstimateToolSchemaTokens(tok contextmeter.Tokenizer, tools []llm.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	if tok == nil {
		tok = contextmeter.Default()
	}
	b, err := json.Marshal(tools)
	if err != nil {
		total := 0
		for _, tool := range tools {
			total += tok.Count(tool.Name) + tok.Count(tool.Description) + tok.Count(string(tool.Schema))
		}
		return total
	}
	return tok.Count(string(b))
}

// Assemble builds the provider-facing send view for target.
func Assemble(turns []conversation.Turn, state conversation.Compaction, cfg config.CompactionConfig, floor int64, target Target, tok contextmeter.Tokenizer) Result {
	if tok == nil {
		tok = contextmeter.Default()
	}
	window, windowKnown := WindowForTarget(target)
	acct := Accounting{RawTokens: EstimateRawTokens(turns), Window: window, WindowKnown: windowKnown}
	if floor > 0 {
		turns, _ = compactor.StubToolResultsThrough(turns, floor)
	}
	view, _ := compactor.BuildSendView(turns, state)
	acct.InitialTokens = compaction.ProviderTotalTokens(tok, view)

	if cfg.Enabled && cfg.HardOverridePct > 0 && acct.Window > 0 {
		acct.HardLimit = int(float64(acct.Window) * cfg.HardOverridePct)
		if acct.HardLimit > 0 && acct.InitialTokens > acct.HardLimit {
			acct.Scheduled = true
			view, _ = compaction.ElideSupersededToolResults(view)
			acct.AfterHardElide = compaction.ProviderTotalTokens(tok, view)
			if acct.AfterHardElide > acct.HardLimit {
				view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
				acct.AfterKeepLast = compaction.ProviderTotalTokens(tok, view)
			}
			current := compaction.ProviderTotalTokens(tok, view)
			if current > acct.HardLimit {
				preserve := 0
				if state.ConsolidatedJSON != "" {
					preserve = 1
				}
				var dropped int
				view, dropped = compaction.TruncateOldestToFit(view, tok, acct.HardLimit, preserve)
				acct.DroppedMessages = dropped
				acct.Truncated = dropped > 0
			}
		}
	}

	if cfg.ElideToolResults {
		view, _ = compaction.ElideSupersededToolResults(view)
	}
	if cfg.LossyToolElision {
		view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
	}
	view = llm.RepairPairing(view)
	acct.FinalTokens = compaction.ProviderTotalTokens(tok, view)
	acct.MessageTokens = acct.FinalTokens
	acct.EstimatedRequestTokens = acct.MessageTokens
	return Result{Messages: view, Accounting: acct}
}
