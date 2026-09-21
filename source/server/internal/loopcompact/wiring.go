package loopcompact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/dispatchtrace"
	"cercano/source/server/internal/engine"
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
	// CloudFallbackTimeout bounds the compaction cloud fallback on its own
	// clock (detached from the pass deadline; see BuildSummarizer).
	CloudFallbackTimeout = 2 * time.Minute
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
	// OpenTierModel resolves the effective open model for a tier
	// (summarizer-lane model resolution).
	OpenTierModel func(config.Tier) string
	// OpenRunner returns the local/open turn runner for the fast_light_text
	// summarizer lane. nil-returning disables the local lane.
	OpenRunner func() agent.TurnRunner
	// CloudRunner returns the active cloud turn runner for fallback. May be
	// nil-returning (local-only deployments).
	CloudRunner func() agent.TurnRunner
	// CloudModelForTier resolves the cloud vendor's model for a tier
	// (economy for fast_light_text). nil = no override.
	CloudModelForTier func(config.Tier) string
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

// BuildSummarizer assembles the compaction summarizer with the SAME routing the
// host main loop uses: greedy decoding, budgeted chunking against the local
// runtime window, fast_light_text tier, and a detached cloud fallback when the
// local lane fails (unless the segment was deferred under an open locus).
// Returns nil when no provider can summarize (neither lane available), so
// callers wire unconditionally and simply get no compaction.
func BuildSummarizer(deps WiringDeps) Summarize {
	if deps.OpenRunner == nil && deps.CloudRunner == nil {
		return nil
	}
	cfg := deps.Cfg
	// Summarizer model precedence: explicit compaction.summarizer_model → the
	// fast_light_text tier's open side (an empty result leaves ModelOverride
	// unset and the runner's everyday default applies — the fallback of last
	// resort, matching the host).
	summarizerModel := cfg.Compaction.SummarizerModel
	if summarizerModel == "" && deps.OpenTierModel != nil {
		summarizerModel = deps.OpenTierModel(config.TierFastLightText)
	}

	parseLogged := func(output, via string) compaction.StructuredSummary {
		s := compaction.ParseSummary(output)
		if s.IsEmpty() {
			// Metadata only: the raw head previously logged here could echo
			// conversation content; sizes and section presence answer the
			// "why was it empty" question without leaking any of it.
			deps.logf("[compaction] summarizer (%s) output parsed EMPTY: output_chars=%d sections_present=none",
				via, len(output))
		}
		return s
	}

	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		// Greedy decoding is a correctness requirement, not a tuning choice: the
		// frames-matrix bakeoff (compaction-bakeoff-findings.md) showed
		// default-temperature summarization is a coin flip; temperature 0
		// reproduces exactly and keeps every proposal anchor.
		greedy := engine.Greedy()
		localSummaryWindow := modelwindow.LocalRuntimeWindow(cfg, summarizerModel)
		if cfg.OpenRuntime == "llama_server" && deps.OpenRuntimeContext != nil {
			capacity, err := deps.OpenRuntimeContext(ctx, summarizerModel, true)
			if err != nil {
				return compaction.StructuredSummary{}, err
			}
			localSummaryWindow = capacity.Window
			ctx = llm.WithRuntimeContext(ctx, capacity)
		}
		convID, iteration := scopeFrom(ctx)
		requestID := fmt.Sprintf("compaction-%s-%d-%d", convID, iteration, time.Now().UnixNano())
		// Scoped dispatch trace (nil unless the dispatch front door opted in
		// for this dispatch): records the exact summarizer prompts and raw
		// responses, correlated by conversation and iteration.
		tr := dispatchtrace.From(ctx)
		summaryCall := 0
		summary, stats, err := compaction.SummarizeBudgetedLocal(ctx, msgs, localSummaryWindow, compaction.DefaultSummaryOutputReserve, func(ctx context.Context, prompt string, maxTokens int) (compaction.StructuredSummary, error) {
			summaryCall++
			callID := fmt.Sprintf("%s:%d", requestID, summaryCall)
			budget := compaction.EstimateSummaryBudget(prompt, maxTokens, localSummaryWindow)
			deps.logf("[compaction] local summarizer request: request_id=%s route=local prompt_tokens=%d output_reserve=%d limit=%d budget=%d fits=%t",
				requestID, budget.PromptTokens, compaction.DefaultSummaryOutputReserve, budget.Limit, budget.Budget, budget.Fits)
			tr.SummarizerRequest(dispatchtrace.SummarizerRequestEvent{
				Route: "local", RequestID: callID, Model: summarizerModel,
				Tier: string(config.TierFastLightText), MaxTokens: maxTokens, Temperature: greedy.Temperature,
				Prompt: prompt, ConversationID: convID, Iteration: iteration,
			})
			req := &agent.Request{Input: prompt, Temperature: greedy.Temperature, Tier: string(config.TierFastLightText), MaxTokens: maxTokens, RequestID: callID, ConversationID: convID}
			if summarizerModel != "" {
				req.ModelOverride = summarizerModel
			}
			var open agent.TurnRunner
			if deps.OpenRunner != nil {
				open = deps.OpenRunner()
			}
			if open == nil {
				return compaction.StructuredSummary{}, fmt.Errorf("no local provider configured for compaction summarization")
			}
			resp, err := open.Process(ctx, req)
			if err != nil {
				tr.SummarizerResponse(dispatchtrace.SummarizerResponseEvent{
					Route: "local", RequestID: callID, Model: summarizerModel,
					Err: classifyFailure(err), ConversationID: convID, Iteration: iteration,
				})
				return compaction.StructuredSummary{}, err
			}
			tr.SummarizerResponse(dispatchtrace.SummarizerResponseEvent{
				Route: "local", RequestID: callID, Model: summarizerModel,
				Output: resp.Output, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
				ConversationID: convID, Iteration: iteration,
			})
			// Provider usage vs estimate, per request: the seam reports no usage,
			// so the budget attributes the chunk's ESTIMATED input; the actual
			// provider-reported counters land here for comparison. Metadata only.
			deps.logf("[compaction] summarizer usage: request_id=%s route=local input_tokens_estimated=%d input_tokens_reported=%d output_tokens_reported=%d",
				requestID, budget.PromptTokens, resp.InputTokens, resp.OutputTokens)
			return parseLogged(resp.Output, "local"), nil
		})
		if err == nil {
			deps.logf("[compaction] local summarizer complete: request_id=%s chunks=%d merged=%t prompt_tokens=%v output_reserve=%d limit=%d",
				requestID, stats.Chunks, stats.Merged, stats.PromptTokens, compaction.DefaultSummaryOutputReserve, localSummaryWindow)
			return summary, nil
		}

		// Local summarizer unavailable (runtime down, model downloading, or a
		// size refusal). Fall back to the active cloud provider so compaction
		// keeps working — with the same locus and deferral rules as the host.
		var deferral *compaction.DeferralError
		if errors.As(err, &deferral) && !cloudIsPrimaryLocus(cfg) {
			deps.logf("[compaction] local summarizer deferred (%s) — not falling back to cloud under locus_mode=%q", classifyFailure(err), cfg.LocusMode)
			return compaction.StructuredSummary{}, err
		}
		var cloud agent.TurnRunner
		if deps.CloudRunner != nil {
			cloud = deps.CloudRunner()
		}
		if cloud != nil {
			cloudReq := &agent.Request{Input: compaction.BuildSummaryPrompt(msgs), Temperature: greedy.Temperature, Tier: string(config.TierFastLightText), MaxTokens: compaction.DefaultSummaryOutputReserve, RequestID: requestID + ":cloud", ConversationID: convID}
			// Summarization is fast_light_text work — resolve the vendor's
			// economy model instead of burning the premium chat model on it.
			if deps.CloudModelForTier != nil {
				if m := deps.CloudModelForTier(config.TierFastLightText); m != "" {
					cloudReq.ModelOverride = m
				}
			}
			deps.logf("[compaction] local summarizer failed (%s) — falling back to cloud", classifyFailure(err))
			tr.SummarizerRequest(dispatchtrace.SummarizerRequestEvent{
				Route: "cloud", RequestID: cloudReq.RequestID, Model: cloudReq.ModelOverride,
				Tier: string(config.TierFastLightText), MaxTokens: cloudReq.MaxTokens, Temperature: cloudReq.Temperature,
				Prompt: cloudReq.Input, ConversationID: convID, Iteration: iteration,
			})
			// Detach from the pass deadline but keep cancellation: the pass
			// deadline is exactly what this call is meant to outlive, while
			// shutdown must still be able to stop it.
			cloudCtx, cancelCloud := context.WithTimeout(context.WithoutCancel(ctx), CloudFallbackTimeout)
			stopPropagate := context.AfterFunc(ctx, func() {
				if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
					cancelCloud()
				}
			})
			cresp, cerr := cloud.Process(cloudCtx, cloudReq)
			stopPropagate()
			cancelCloud()
			if cerr == nil {
				tr.SummarizerResponse(dispatchtrace.SummarizerResponseEvent{
					Route: "cloud", RequestID: cloudReq.RequestID, Model: cloudReq.ModelOverride,
					Output: cresp.Output, InputTokens: cresp.InputTokens, OutputTokens: cresp.OutputTokens,
					ConversationID: convID, Iteration: iteration,
				})
				deps.logf("[compaction] summarizer usage: request_id=%s route=cloud input_tokens_reported=%d output_tokens_reported=%d", cloudReq.RequestID, cresp.InputTokens, cresp.OutputTokens)
				return parseLogged(cresp.Output, "cloud fallback"), nil
			}
			// Both failures classified, never echoed raw — provider errors can
			// carry content.
			tr.SummarizerResponse(dispatchtrace.SummarizerResponseEvent{
				Route: "cloud", RequestID: cloudReq.RequestID, Model: cloudReq.ModelOverride,
				Err: classifyFailure(cerr), ConversationID: convID, Iteration: iteration,
			})
			deps.logf("[compaction] cloud fallback FAILED: reason=%s", classifyFailure(cerr))
			return compaction.StructuredSummary{}, fmt.Errorf("local summarizer: %w; cloud fallback: %v", err, cerr)
		}
		return compaction.StructuredSummary{}, err
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

// cloudIsPrimaryLocus reports whether the configured locus puts the cloud in
// front for compaction summarization work — the gate for spending cloud tokens
// on a segment the LOCAL summarizer deferred on size. An unparseable mode
// falls back to the package default rather than silently enabling cloud spend.
func cloudIsPrimaryLocus(cfg config.Config) bool {
	mode, err := locus.ParseMode(cfg.LocusMode)
	if err != nil {
		mode = locus.DefaultMode
	}
	return mode.Coproc().Preferred == locus.TierCloud
}
