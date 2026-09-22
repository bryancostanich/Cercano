package loopcompact

import (
	"context"
	"fmt"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/dispatchhistory"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/modelwindow"
	"cercano/source/server/pkg/config"
)

// Compaction budget policy — ONE policy shared by the host front door and the
// worker. These are the exact values cmd/cercano/main.go used inline; they moved
// here so worker-mode dispatches compact against the same budget derivation
// instead of a parallel worker policy. NOT worker-tunable.
const (
	// CompactedBudgetDefaultPct is the default fraction of the chat model's
	// context window bounding the compacted backlog.
	CompactedBudgetDefaultPct = 0.30
	// CompactedBudgetFloorTokens keeps a tiny-window local model from getting a
	// uselessly small budget.
	CompactedBudgetFloorTokens = 16000
)

// WiringDeps carries the seams both front doors (host cmd/cercano/main.go and
// internal/worker) supply so the two assemble the SAME summarizer routing, the
// SAME compactor config, and the SAME per-dispatch factory. There is no worker
// variant of any policy here: only the provider/accessor seams differ, because
// the worker reaches its open/cloud providers through host-streamed proxies
// while the host holds them directly.
type WiringDeps struct {
	// Cfg is the effective config (host: live config; worker: host snapshot).
	Cfg config.Config
	// ChatModel is the everyday open chat model — the budget denominator.
	// Host resolves it via openmodels; the worker uses the host-resolved
	// override snapshotted into its config.
	ChatModel string
	// Candidates returns the same immutable routing graph used by task dispatch.
	// Host resolves it live; workers use the host-snapshotted graph.
	Candidates func() inference.Tiers
	// OpenRuntimeContext resolves llama-server serving capacity for a model.
	// nil, or a non-llama_server runtime, means static windows apply.
	OpenRuntimeContext func(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error)
	// Log receives metadata-only diagnostics (never content). nil disables.
	Log func(format string, args ...any)
}

func (d WiringDeps) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log(format, args...)
	}
}

// BuildSummarizer honors the Compaction task's destination and quality through
// the existing router. Profile backup and locality rules belong to that graph,
// not a special local-first/cloud fallback hidden inside compaction.
func BuildSummarizer(deps WiringDeps) Summarize {
	if deps.Candidates == nil {
		return nil
	}
	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		ctx, cancel := compaction.WithExecutionBudget(ctx)
		defer cancel()
		if err := ctx.Err(); err != nil {
			return compaction.StructuredSummary{}, err
		}
		tiers := deps.Candidates()
		assignment := deps.Cfg.TaskAssignment(config.TaskCompaction)
		if tiers.TaskFor != nil {
			assignment = tiers.TaskFor(config.TaskCompaction)
		}
		tier := assignment.Quality.CapabilityTier()
		mode, err := locus.ParseMode(deps.Cfg.LocusMode)
		if tiers.Mode != "" {
			mode = tiers.Mode
			err = nil
		}
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		localModel := ""
		if tiers.ModelFor != nil {
			localModel = tiers.ModelFor(inference.Selection{}, tier)
		}
		// The legacy model override applies only to an explicitly selected Local
		// task. It must never hijack Secondary or a redirected cloud destination.
		if assignment.Destination == config.DestinationLocal && deps.Cfg.Compaction.SummarizerModel != "" {
			localModel = deps.Cfg.Compaction.SummarizerModel
		}
		if tiers.OpenReady != nil && !tiers.OpenReady(localModel) {
			tiers.Open = nil
		}
		selected, err := inference.SelectDestination(mode, assignment.Destination, tiers)
		if err != nil {
			return compaction.StructuredSummary{}, err
		}
		model := localModel
		if selected.IsCloud {
			model = ""
			if tiers.ModelFor != nil {
				model = tiers.ModelFor(selected, tier)
			}
		}
		provider := inference.WithTaskRoute(selected.Provider, config.TaskCompaction, assignment, selected.PolicyDestination, model)
		run := agent.InferenceTurnRunner(provider, model)
		window := contextmeter.ModelMax(model)
		route := "cloud"
		if !selected.IsCloud {
			route = "local"
			window = modelwindow.LocalRuntimeWindow(deps.Cfg, model)
			if deps.Cfg.OpenRuntime == "llama_server" && deps.OpenRuntimeContext != nil {
				capacity, err := deps.OpenRuntimeContext(ctx, model, true)
				if err != nil {
					return compaction.StructuredSummary{}, err
				}
				window = capacity.Window
				ctx = llm.WithRuntimeContext(ctx, capacity)
			}
		}
		greedy := engine.Greedy()
		convID, iteration := scopeFrom(ctx)
		requestID := fmt.Sprintf("compaction-%s-%d-%d", convID, iteration, time.Now().UnixNano())
		tr := dispatchhistory.From(ctx)
		callIndex := 0
		call := func(ctx context.Context, prompt string, maxTokens int) (compaction.StructuredSummary, error) {
			if err := ctx.Err(); err != nil {
				return compaction.StructuredSummary{}, err
			}
			callIndex++
			callID := fmt.Sprintf("%s:%d", requestID, callIndex)
			deps.logf("[compaction] summarizer request: request_id=%s destination=%s quality=%s route=%s", callID, selected.PolicyDestination, assignment.Quality, route)
			tr.SummarizerRequest(dispatchhistory.SummarizerRequestEvent{Route: route, RequestID: callID, Model: model, Tier: string(tier), MaxTokens: maxTokens, Temperature: greedy.Temperature, Prompt: prompt, ConversationID: convID, Iteration: iteration})
			resp, err := run.Process(ctx, &agent.Request{Input: prompt, Temperature: greedy.Temperature, Tier: string(tier), ModelOverride: model, MaxTokens: maxTokens, RequestID: callID, ConversationID: convID})
			if err != nil {
				tr.SummarizerResponse(dispatchhistory.SummarizerResponseEvent{Route: route, RequestID: callID, Model: model, Err: classifyFailure(err), ConversationID: convID, Iteration: iteration})
				return compaction.StructuredSummary{}, err
			}
			servedModel := resp.RoutingMetadata.ModelName
			if servedModel == "" {
				servedModel = model
			}
			tr.SummarizerResponse(dispatchhistory.SummarizerResponseEvent{Route: route, RequestID: callID, Model: servedModel, Output: resp.Output, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, ConversationID: convID, Iteration: iteration})
			deps.logf("[compaction] summarizer usage: request_id=%s route=%s input_tokens_reported=%d output_tokens_reported=%d", callID, route, resp.InputTokens, resp.OutputTokens)
			summary := compaction.ParseSummary(resp.Output)
			if summary.IsEmpty() {
				deps.logf("[compaction] summarizer output parsed EMPTY: output_chars=%d", len(resp.Output))
			}
			return summary, nil
		}
		// Keep local capacity-aware chunking. Cloud requests retain their existing
		// prompt construction; provider-profile failover runs within this context.
		if selected.IsCloud {
			return call(ctx, compaction.BuildSummaryPrompt(msgs), compaction.DefaultSummaryOutputReserve)
		}
		summary, _, err := compaction.SummarizeBudgetedLocal(ctx, msgs, window, compaction.DefaultSummaryOutputReserve, call)
		return summary, err
	}
}

// BuildConfig derives the compactor config from the app config — the same
// derivation the host front door performs inline (thresholds verbatim from
// cfg.Compaction; the compacted backlog budget a fraction of the everyday chat
// model's window, floored). Worker callers get the identical policy because
// there is no other construction site.
func BuildConfig(deps WiringDeps) compactor.Config {
	cfg := deps.Cfg
	budgetPct := cfg.Compaction.CompactedBudgetPct
	if budgetPct <= 0 {
		budgetPct = CompactedBudgetDefaultPct
	}
	budgetWindow := modelwindow.LocalRuntimeWindow(cfg, deps.ChatModel)
	if cfg.OpenRuntime == "llama_server" && deps.OpenRuntimeContext != nil {
		capacityCtx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		defer cancel()
		if capacity, err := deps.OpenRuntimeContext(capacityCtx, deps.ChatModel, false); err == nil {
			budgetWindow = capacity.Window
		}
	}
	if budgetWindow <= 0 && cfg.OpenRuntime != "llama_server" {
		budgetWindow = contextmeter.ModelMax(deps.ChatModel)
	}
	budgetTokens := int(float64(budgetWindow) * budgetPct)
	if budgetTokens < CompactedBudgetFloorTokens {
		budgetTokens = CompactedBudgetFloorTokens
	}
	return compactor.Config{
		ActivationFloorTokens:   cfg.Compaction.ActivationFloorTokens,
		SegmentTokens:           cfg.Compaction.SegmentTokens,
		VerbatimRecent:          cfg.Compaction.VerbatimRecent,
		CompactedBudgetTokens:   budgetTokens,
		TieredRetentionSegments: cfg.Compaction.TieredRetentionSegments,
	}
}

// NewFactory builds the per-dispatch compactor factory from the shared wiring.
// It returns nil when compaction is disabled in config or no summarizer can be
// assembled, so callers wire unconditionally. Each call of the returned factory
// produces a FRESH compactor: frozen-segment state is per-dispatch and must
// never leak between concurrent sub-agents.
func NewFactory(deps WiringDeps) func() agent.LoopCompactor {
	if !deps.Cfg.Compaction.Enabled {
		return nil
	}
	summarize := BuildSummarizer(deps)
	if summarize == nil {
		return nil
	}
	compCfg := BuildConfig(deps)
	logPass := func(ev PassEvent) { deps.logf("%s", FormatPassEvent(ev)) }
	deps.logf("[loop-compaction] configured: enabled=true activation_floor_tokens=%d segment_tokens=%d verbatim_recent=%d compacted_budget_tokens=%d",
		compCfg.ActivationFloorTokens, compCfg.SegmentTokens, compCfg.VerbatimRecent, compCfg.CompactedBudgetTokens)
	return func() agent.LoopCompactor {
		c := New(Options{
			Config:    compCfg,
			Summarize: summarize,
			Tokenizer: contextmeter.Default(),
			OnPass:    logPass,
		})
		if c == nil {
			return nil
		}
		return c
	}
}
