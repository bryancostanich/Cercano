// Package requestassembly builds the provider-facing conversation send view for
// a concrete model attempt and reports deterministic token accounting for that
// view.
package requestassembly

import (
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
	RouteLabel    string
	Provider      string
	Model         string
	Tier          string
	ContextWindow int
	TightContext  bool
}

// Accounting describes how the raw persisted turns became the final send view.
// Counts use ProviderTotalTokens because this is provider-facing accounting, not
// raw storage pressure.
type Accounting struct {
	RawTokens       int
	Window          int
	HardLimit       int
	InitialTokens   int
	AfterHardElide  int
	AfterKeepLast   int
	FinalTokens     int
	DroppedMessages int
	Scheduled       bool
	Truncated       bool
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
	if target.ContextWindow > 0 {
		return target.ContextWindow
	}
	return contextmeter.ModelMax(target.Model)
}

// Assemble builds the provider-facing send view for target.
func Assemble(turns []conversation.Turn, state conversation.Compaction, cfg config.CompactionConfig, floor int64, target Target, tok contextmeter.Tokenizer) Result {
	if tok == nil {
		tok = contextmeter.Default()
	}
	acct := Accounting{RawTokens: EstimateRawTokens(turns), Window: WindowFor(target)}
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
	return Result{Messages: view, Accounting: acct}
}
