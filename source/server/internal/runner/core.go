package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/failurelog"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/requestassembly"
	"cercano/source/server/internal/routinglog"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
)

// Core implements TurnRunner. It holds no process-global mutable state, so
// many Cores may run concurrently (embedded mode) or one per worker process
// (Phase 5) — identical code path either way.
type Core struct{ d Deps }

type targetHistory interface {
	AssembleHistoryForTarget(ctx context.Context, convID string, target requestassembly.Target) requestassembly.Result
}

// New constructs a Core from the injected service dependencies.
func New(d Deps) *Core { return &Core{d: d} }

func (c *Core) logRoute(event string, fields routinglog.Event) {
	if c == nil || c.d.RoutingLog == nil {
		return
	}
	c.d.RoutingLog.Log(event, fields)
}

func (c *Core) logFailure(event string, fields failurelog.Event) {
	if c == nil || c.d.FailureLog == nil {
		return
	}
	c.d.FailureLog.Log(event, fields)
}

func mainFailureFields(req Request, scope, provider, model string, isCloud bool, err error) failurelog.Event {
	fields := failurelog.Event{
		"scope":           scope,
		"conversation_id": req.ConversationID,
		"provider":        provider,
		"model":           model,
		"is_cloud":        isCloud,
		"error_class":     errClassString(err),
		"message":         failurelog.SanitizeMessage(errorString(err)),
	}
	if ep := llm.ProviderOf(err); ep != "" {
		fields["error_provider"] = ep
	}
	return fields
}

func addAssemblyFailureFields(fields failurelog.Event, acct requestassembly.Accounting) {
	fields["initial_tokens"] = acct.InitialTokens
	fields["after_hard_elide"] = acct.AfterHardElide
	fields["after_keep_last"] = acct.AfterKeepLast
	fields["final_tokens"] = acct.FinalTokens
	fields["raw_tokens"] = acct.RawTokens
	fields["dropped_messages"] = acct.DroppedMessages
	fields["context_window"] = acct.Window
	fields["hard_limit"] = acct.HardLimit
}

func addRequestBudgetFailureFields(fields failurelog.Event, result agent.ToolLoopResult) {
	budget := result.LastRequestBudget
	fields["system_tokens"] = budget.SystemTokens
	fields["message_tokens"] = budget.MessageTokens
	fields["tool_schema_tokens"] = budget.ToolTokens
	fields["output_reserve"] = budget.OutputReserve
	fields["estimated_total_request_tokens"] = budget.EstimatedUsed
	if result.InputTokens > 0 {
		fields["provider_reported_input_tokens"] = result.InputTokens
	}
}

func addTokenDiagnosticsFailureFields(fields failurelog.Event, acct requestassembly.Accounting, result agent.ToolLoopResult) failurelog.Event {
	addAssemblyFailureFields(fields, acct)
	addRequestBudgetFailureFields(fields, result)
	return fields
}

func providerName(p inference.Provider) string {
	if p == nil {
		return ""
	}
	return p.Name()
}

func errClassString(err error) string {
	if err == nil {
		return ""
	}
	return string(llm.ClassOf(err))
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func failedProviderName(routeProvider inference.Provider, err error) string {
	if name := llm.ProviderOf(err); name != "" {
		return name
	}
	return providerName(routeProvider)
}

func (c *Core) contextWindowFor(isCloud bool, model string) int {
	window, _ := c.knownContextWindowFor(isCloud, model)
	return window
}

func (c *Core) knownContextWindowFor(isCloud bool, model string) (int, bool) {
	if c.d.Config != nil && !isCloud {
		window := localContextWindow(c.d.Config.Get(), model)
		return window, window > 0
	}
	return contextmeter.KnownModelMax(model)
}

func (c *Core) assembleAttemptHistory(ctx context.Context, req Request, attempt string, provider inference.Provider, model string, isCloud bool, tightContext bool) ([]llm.Message, requestassembly.Accounting) {
	if c.d.Persist == nil || req.ConversationID == "" {
		return nil, requestassembly.Accounting{}
	}
	target := requestassembly.Target{
		RouteLabel:    attempt,
		Provider:      providerName(provider),
		Model:         model,
		ContextWindow: c.contextWindowFor(isCloud, model),
		TightContext:  tightContext,
	}
	if h, ok := c.d.Persist.(targetHistory); ok {
		assembled := h.AssembleHistoryForTarget(ctx, req.ConversationID, target)
		acct := assembled.Accounting
		c.logRoute("context.assembled", routinglog.Event{
			"conversation_id":   req.ConversationID,
			"attempt":           attempt,
			"provider":          target.Provider,
			"model":             target.Model,
			"is_cloud":          isCloud,
			"context_window":    acct.Window,
			"hard_limit":        acct.HardLimit,
			"raw_tokens":        acct.RawTokens,
			"initial_tokens":    acct.InitialTokens,
			"after_hard_elide":  acct.AfterHardElide,
			"after_keep_last":   acct.AfterKeepLast,
			"final_tokens":      acct.FinalTokens,
			"dropped_messages":  acct.DroppedMessages,
			"scheduled_compact": acct.Scheduled,
			"truncated":         acct.Truncated,
			"tight_context":     tightContext,
		})
		return assembled.Messages, acct
	}
	return c.d.Persist.AssembleHistory(ctx, req.ConversationID), requestassembly.Accounting{}
}

// localContextWindow reports the context window the local runtime will
// actually serve for model, which is NOT always the configured value: a
// catalog model may pin its own --ctx-size in ExtraArgs, and because per-model
// flags are appended last, llama-server honors that one instead of the config's.
// Budgeting against the config number alone produced false preflight
// context_overflow rejections for requests the running server would have
// accepted (config said 16384 while the process served 32768).
//
// model is the resolved local model ID; empty falls back to the config value.
func localContextWindow(cfg config.Config, model string) int {
	switch cfg.OpenRuntime {
	case "mistralrs":
		return cfg.MistralRS.MaxSeqLen
	case "llama_server":
		return llamaServerWindow(cfg.LlamaServer.ContextSize, cfg.LlamaServer.ContextSizeSet, model)
	default:
		if cfg.LlamaServer.ContextSize > 0 {
			return llamaServerWindow(cfg.LlamaServer.ContextSize, cfg.LlamaServer.ContextSizeSet, model)
		}
		return cfg.MistralRS.MaxSeqLen
	}
}

// llamaServerWindow applies the catalog's per-model --ctx-size override to the
// configured context size. The model ID carries provider/catalog prefixes in
// routing form (e.g. "llama_server:catalog:glm-4.5-air-q4_k_m"); the catalog is
// keyed by the bare ID, so match on the final segment.
func llamaServerWindow(configured int, configExplicit bool, model string) int {
	if configExplicit && configured > 0 {
		return configured
	}
	if model == "" {
		return configured
	}
	bare := model
	if i := strings.LastIndex(bare, ":"); i >= 0 {
		bare = bare[i+1:]
	}
	total := sysram.Total()
	if total < 0 {
		total = 0
	}
	if n := llamaserver.ModelContextOverride(bare, uint64(total)); n > 0 {
		return n
	}
	return configured
}

func addCloudProfileFields(fields routinglog.Event, prefix string, cfg config.Config, name string) {
	if name == "" {
		return
	}
	for _, p := range cfg.CloudProfiles {
		if p.Name != name {
			continue
		}
		fields[prefix+"_profile"] = p.Name
		fields[prefix+"_provider"] = p.Provider
		fields[prefix+"_flavor"] = p.Flavor
		fields[prefix+"_route"] = p.Route
		fields[prefix+"_backend"] = p.Backend
		fields[prefix+"_base_url_set"] = p.BaseURL != ""
		fields[prefix+"_stored_model"] = p.Model
		fields[prefix+"_model_pinned"] = p.ModelPinned
		return
	}
	fields[prefix+"_profile"] = name
	fields[prefix+"_missing"] = true
}

// RunTurn executes one conversation turn end-to-end:
//  1. Resolve provider from locus config.
//  2. Assemble conversation history.
//  3. Persist the user turn up front (crash resilience).
//  4. Build watchdog gate/turn-end + gated tool registry.
//  5. Run agent.RunToolLoop.
//  6. Cross-tier fallback on error.
//  7. Post-turn bookkeeping (recap, compaction, context usage).
//
// Events are emitted via sink; W/X permission gates go via requester;
// assistant/tool-result turns are persisted via persist (host-fenced).
func (c *Core) RunTurn(
	ctx context.Context,
	req Request,
	sink EventSink,
	requester PermissionRequester,
	persist PersistFunc,
) (Result, error) {
	// Thread the conversation ID as the session ID so downstream (e.g. the
	// stream-anomaly log) can attribute this turn to its conversation.
	ctx = llm.WithSessionID(ctx, req.ConversationID)
	startFields := routinglog.Event{
		"conversation_id":       req.ConversationID,
		"effective_cloud_model": c.d.Providers.MainModel(true),
		"effective_open_model":  c.d.Providers.MainModel(false),
	}
	if c.d.Config != nil {
		cfgSnap := c.d.Config.Get()
		startFields["locus_mode"] = cfgSnap.LocusMode
		startFields["active_cloud_profile"] = cfgSnap.ActiveCloudProfile
		startFields["backup_cloud_profile"] = cfgSnap.BackupCloudProfile
		startFields["open_runtime"] = cfgSnap.OpenRuntime
		startFields["mistralrs_enabled"] = cfgSnap.MistralRS.Enabled
		startFields["mistralrs_max_seq_len"] = cfgSnap.MistralRS.MaxSeqLen
		startFields["llama_server_context_size"] = cfgSnap.LlamaServer.ContextSize
		addCloudProfileFields(startFields, "active", cfgSnap, cfgSnap.ActiveCloudProfile)
		addCloudProfileFields(startFields, "backup", cfgSnap, cfgSnap.BackupCloudProfile)
	}
	c.logRoute("turn.start", startFields)

	// 1. Resolve the provider per the active Locus Mode.
	provider, isCloud, fellBack, err := c.d.Providers.Main()
	if err != nil {
		c.logRoute("turn.select_error", routinglog.Event{
			"conversation_id": req.ConversationID,
			"error":           err.Error(),
		})
		c.logFailure("main.provider_error", failurelog.Event{
			"scope":           "main",
			"conversation_id": req.ConversationID,
			"error_class":     errClassString(err),
			"message":         failurelog.SanitizeMessage(errorString(err)),
		})
		// *_only mode with its required tier unavailable — return a synthetic
		// result so the host can send a terminal FinalResponse.
		return Result{FinalText: "Locus: " + err.Error()}, nil
	}
	selectedModel := c.d.Providers.MainModel(isCloud)
	c.logRoute("turn.selected", routinglog.Event{
		"conversation_id": req.ConversationID,
		"provider":        providerName(provider),
		"model":           selectedModel,
		"is_cloud":        isCloud,
		"locus_fell_back": fellBack,
	})
	if fellBack {
		sink.Emit(Event{
			Kind: EventProgress,
			Text: fmt.Sprintf("⚠ preferred tier unavailable — falling back to %s (%s)",
				provider.Name(), selectedModel),
		})
	}

	// Announce the route so the client shows the correct engine badge.
	sink.Emit(Event{
		Kind:    EventRouteSelected,
		Model:   selectedModel,
		IsCloud: isCloud,
	})

	// 2. Assemble conversation history before crash-resilient user persistence.
	// The tool loop receives the current user input separately; if assembly runs
	// after PersistTurn below, the current user turn is duplicated in the model
	// request. Prepare the primary history now and prepare a potential cross-tier
	// fallback history for the concrete fallback target as well.
	convHistory, attemptAccounting := c.assembleAttemptHistory(ctx, req, "primary", provider, selectedModel, isCloud, false)
	fallbackHistory := []llm.Message(nil)
	fallbackAccounting := requestassembly.Accounting{}
	fallbackPrepared := false
	mode := locus.Mode("cloud_primary")
	if c.d.Config != nil {
		mode, _ = locus.ParseMode(c.d.Config.Get().LocusMode)
	}
	res := mode.Main()
	fbProv := c.d.Providers.Cloud()
	fbCloud := true
	if res.Fallback == locus.TierLocal {
		fbProv, fbCloud = c.d.Providers.Open(), false
	}
	fallbackModel := c.d.Providers.MainModel(fbCloud)
	if fbProv != nil {
		fallbackPrepared = true
		fallbackHistory, fallbackAccounting = c.assembleAttemptHistory(ctx, req, "cross_tier_fallback", fbProv, fallbackModel, fbCloud, !fbCloud)
	}

	// 3. Crash-resilient persistence: persist the USER turn up front (before
	// any LLM call) so a crash/kill/restart cannot lose the prompt. The
	// assistant/tool-result turns are persisted incrementally via persist
	// (OnTurnComplete). On the rare cross-tier fallback retry, already-persisted
	// assistant turns may be re-persisted — non-destructive.
	// Persistence is enabled whenever the host supplied a persist sink and this
	// is a real conversation. In-process the sink writes to the local store; in
	// worker mode it forwards over the stream to the host, which owns the store.
	// (Previously this was gated on c.d.Agent != nil, which is nil in the worker
	// child process — silently disabling ALL persistence for worker-executed
	// turns. See docs/bugs/2026-07-09-worker-turn-persistence.md.)
	persistEnabled := persist != nil && req.ConversationID != ""
	if persistEnabled {
		// In-process the runner owns the store and must create the conversation
		// row before the first write. In worker mode the child has no local
		// store (c.d.Agent == nil); the host ensures the row up front, so skip.
		if c.d.Agent != nil && c.d.Agent.PersistentStore() != nil {
			if err := c.d.Agent.PersistentStore().EnsureConversation(
				ctx, req.ConversationID, req.WorkDir, c.d.Providers.MainModel(isCloud),
			); err != nil {
				fmt.Fprintf(os.Stderr, "[tool-loop] EnsureConversation(%s) failed: %v\n", req.ConversationID, err)
				persistEnabled = false
				// Surface the failure so the user knows the turn won't appear in /resume
				// (see docs/bugs/2026-07-04-user-message-tear.md).
				sink.Emit(Event{
					Kind: EventProgress,
					Text: "⚠ conversation persistence unavailable this turn — it will not appear in /resume",
				})
			}
		}
		// Persist the user turn before calling the model (crash resilience).
		if persistEnabled {
			persist(agent.UserMessage(req.Input, req.Images))
		}
	}

	// onTurn is called by the tool loop with each assistant/tool-result message.
	// The PersistFunc passed in by the host already fences on turn generation,
	// so we forward directly. nil persist → onTurn nil (loop treats as no-op).
	var onTurn func(m llm.Message)
	if persistEnabled && persist != nil {
		onTurn = func(m llm.Message) { persist(m) }
	}

	onTextDelta := func(t string) {
		sink.Emit(Event{Kind: EventToken, Text: t})
	}

	// 4. Build watchdog gate/turn-end and the per-turn gated registry.
	// Watchdog is default-OFF (d.Watchdog == nil ⇒ unchanged behavior).
	gateRegistry := c.d.Tools.Registry()
	var wdGate agent.WatchdogGate
	var wdTurnEnd agent.WatchdogTurnEnd
	var wd *watchdog.Watchdog
	if c.d.Watchdog != nil {
		wd = c.d.Watchdog() // live read at turn time (see Deps.Watchdog)
	}
	if wd != nil {
		convID := req.ConversationID
		wdGate = func(ctx context.Context, toolName string, args json.RawMessage, transcript []llm.Message) agent.WatchdogDecision {
			d := wd.Gate(ctx, convID, watchdog.Action{Kind: "tool_call", ToolName: toolName, ToolArgs: args, Transcript: transcript})
			return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge, Revise: d.Revise}
		}
		wdTurnEnd = func(ctx context.Context, finalText string, transcript []llm.Message) agent.WatchdogDecision {
			d := wd.Gate(ctx, convID, watchdog.Action{Kind: "turn_end", Text: finalText, Transcript: transcript})
			return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge, Revise: d.Revise}
		}
		reg := agenttools.NewRegistry()
		for _, t := range c.d.Tools.Registry().All() {
			_ = reg.Register(t)
		}
		_ = reg.Register(wd.JustifyTool(convID))
		gateRegistry = reg

		// Echo forwarding: set echo on the shared watchdog so its protocol
		// interventions route to THIS turn's sink. Safe because turns within a
		// conversation are serialized (beginTurn supersedes the prior one).
		echoOn := c.d.Config.Get().Watchdog.Echo
		if echoOn {
			wd.SetEcho(func(thread, text string) {
				sink.Emit(Event{Kind: EventWatchdog, WatchdogKind: "echo", Summary: text, Thread: thread})
			})
		}
	}

	// 5. Read the active capability profile (read-only planning fence / autonomous
	// mode signal). Live accessor: read at turn time so a mid-session mode switch
	// takes effect on the next turn. Nil accessor = unrestricted (zero Profile).
	var profile agent.Profile
	if c.d.Profiles != nil {
		profile = c.d.Profiles(req.ConversationID)
	}

	// 6. Internal adapter: agent.LoopEvent → runner.Event, forwarded to sink.
	loopSink := makeLoopSink(sink, c.d.FailureLog, req.ConversationID)

	// 7. Build the permission store for the loop.
	var permStore *agent.PermissionStore
	if c.d.Perms != nil {
		permStore = c.d.Perms.Store()
	}

	// Run the tool loop on the primary provider.
	c.logRoute("loop.start", routinglog.Event{
		"conversation_id": req.ConversationID,
		"attempt":         "primary",
		"provider":        providerName(provider),
		"route_provider":  providerName(provider),
		"model":           selectedModel,
		"is_cloud":        isCloud,
	})
	result, loopErr := c.runLoop(ctx, req, provider, selectedModel, isCloud,
		loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile, false)
	c.logRoute("loop.result", routinglog.Event{
		"conversation_id": req.ConversationID,
		"attempt":         "primary",
		"provider":        providerName(provider),
		"route_provider":  providerName(provider),
		"error_provider":  llm.ProviderOf(loopErr),
		"model":           selectedModel,
		"is_cloud":        isCloud,
		"error_class":     errClassString(loopErr),
		"error":           errorString(loopErr),
		"input_tokens":    result.InputTokens,
		"output_tokens":   result.OutputTokens,
	})

	// 6.5. Same-provider turn retry: a transient loop error — server overload
	// (busy), a transport reset (network), or an unclassified failure that may
	// simply be transient (unknown) — is often a mid-stream one, where the
	// resilience engine deliberately cannot re-serve (content already flowed).
	// At the turn level a full re-run IS safe: the failed iteration's partial
	// output was never persisted, and the re-run supersedes it — the same
	// contract the cross-tier fallback below has always relied on. One narrated
	// attempt on the same provider before any tier change; llm.Retryable owns
	// the class policy so this site and the resilience engine stay in sync.
	if loopErr != nil && ctx.Err() == nil && !errors.Is(loopErr, context.Canceled) && llm.Retryable(llm.ClassOf(loopErr)) {
		failedProvider := failedProviderName(provider, loopErr)
		notice := retryNotice(failedProvider, llm.ClassOf(loopErr))
		fmt.Fprintf(os.Stderr, "[resilience] turn retry: %s route failed at %s (%v)\n", provider.Name(), failedProvider, loopErr)
		c.logRoute("loop.retry", routinglog.Event{
			"conversation_id": req.ConversationID,
			"attempt":         "same_provider",
			"provider":        providerName(provider),
			"route_provider":  providerName(provider),
			"error_provider":  failedProvider,
			"model":           selectedModel,
			"is_cloud":        isCloud,
			"previous_error":  errorString(loopErr),
			"error_class":     errClassString(loopErr),
		})
		sink.Emit(Event{Kind: EventProgress, Text: notice})
		result, loopErr = c.runLoop(ctx, req, provider, selectedModel, isCloud,
			loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile, false)
		c.logRoute("loop.result", routinglog.Event{
			"conversation_id": req.ConversationID,
			"attempt":         "same_provider_retry",
			"provider":        providerName(provider),
			"route_provider":  providerName(provider),
			"error_provider":  llm.ProviderOf(loopErr),
			"model":           selectedModel,
			"is_cloud":        isCloud,
			"error_class":     errClassString(loopErr),
			"error":           errorString(loopErr),
			"input_tokens":    result.InputTokens,
			"output_tokens":   result.OutputTokens,
		})
	}

	// 7. Cross-tier fallback: on error, attempt the other tier if locus allows.
	var fallbackNotice string
	if loopErr != nil && ctx.Err() == nil && !errors.Is(loopErr, context.Canceled) {
		failedProvider := failedProviderName(provider, loopErr)
		fromWindow, _ := c.knownContextWindowFor(isCloud, selectedModel)
		fallbackWindow, fallbackWindowKnown := c.knownContextWindowFor(fbCloud, fallbackModel)
		c.logRoute("fallback.consider", routinglog.Event{
			"conversation_id":         req.ConversationID,
			"mode":                    string(mode),
			"cross_allowed":           res.CrossAllowed,
			"already_fell_back":       fellBack,
			"from_provider":           providerName(provider),
			"from_route_provider":     providerName(provider),
			"from_error_provider":     llm.ProviderOf(loopErr),
			"from_effective_provider": failedProvider,
			"from_model":              selectedModel,
			"from_is_cloud":           isCloud,
			"fallback_tier":           res.Fallback.String(),
			"fallback_provider":       providerName(fbProv),
			"fallback_model":          fallbackModel,
			"fallback_is_cloud":       fbCloud,
			"from_context_window":     fromWindow,
			"fallback_context_window": fallbackWindow,
			"fallback_window_known":   fallbackWindowKnown,
			"trigger_error_class":     errClassString(loopErr),
			"trigger_error":           errorString(loopErr),
		})
		if !fellBack && res.CrossAllowed && fbProv != nil && fallbackPrepared && llm.FailoverableToWindow(llm.ClassOf(loopErr), loopErr, fromWindow, fallbackWindow, fallbackWindowKnown) {
			// The local fallback generally has a much smaller context window than
			// the cloud provider. Keep its tool catalog compact for every
			// cross-tier fallback, including transient cloud failures whose error
			// text does not identify context overflow.
			tightContextFallback := !fbCloud
			fallbackNotice = fmt.Sprintf("⚠ %s failed (%v) — retrying on %s", failedProvider, loopErr, fbProv.Name())
			sink.Emit(Event{Kind: EventProgress, Text: fallbackNotice})
			sink.Emit(Event{
				Kind:    EventRouteSelected,
				Model:   fallbackModel,
				IsCloud: fbCloud,
			})
			provider = fbProv
			isCloud = fbCloud
			selectedModel = fallbackModel
			c.logRoute("loop.start", routinglog.Event{
				"conversation_id":        req.ConversationID,
				"attempt":                "cross_tier_fallback",
				"provider":               providerName(fbProv),
				"route_provider":         providerName(fbProv),
				"model":                  fallbackModel,
				"is_cloud":               fbCloud,
				"tight_context_fallback": tightContextFallback,
			})
			convHistory = fallbackHistory
			attemptAccounting = fallbackAccounting
			result, loopErr = c.runLoop(ctx, req, fbProv, fallbackModel, fbCloud,
				loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile, tightContextFallback)
			c.logRoute("loop.result", routinglog.Event{
				"conversation_id": req.ConversationID,
				"attempt":         "cross_tier_fallback",
				"provider":        providerName(fbProv),
				"route_provider":  providerName(fbProv),
				"error_provider":  llm.ProviderOf(loopErr),
				"model":           fallbackModel,
				"is_cloud":        fbCloud,
				"error_class":     errClassString(loopErr),
				"error":           errorString(loopErr),
				"input_tokens":    result.InputTokens,
				"output_tokens":   result.OutputTokens,
			})
		}
	}
	if loopErr != nil {
		fields := mainFailureFields(req, "main", providerName(provider), selectedModel, isCloud, loopErr)
		c.logFailure("main.tool_loop_failed", addTokenDiagnosticsFailureFields(fields, attemptAccounting, result))
		return Result{}, fmt.Errorf("tool loop error: %w", loopErr)
	}

	// Post-turn bookkeeping: recap, compaction, token accounting. Recap and
	// compaction are host-owned (they operate on the persisted history via the
	// Agent). In worker mode the child has no Agent (c.d.Agent == nil), so it
	// must skip them here or ScheduleRecap nil-derefs — the earlier persistEnabled
	// gate used to imply Agent != nil, but no longer does now that worker turns
	// persist. RecordContextUsage below is nil-receiver-safe.
	if persistEnabled && c.d.Agent != nil {
		c.d.Agent.ScheduleRecap(req.ConversationID)
		c.d.Agent.ScheduleCompaction(req.ConversationID)
	}
	c.d.Agent.RecordContextUsage(req.ConversationID, selectedModel,
		result.InputTokens, result.OutputTokens)

	return Result{
		FinalText:    result.FinalText,
		Model:        provider.Name(),
		IsCloud:      isCloud,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		Notice:       fallbackNotice,
	}, nil
}

// runLoop is the single-provider tool-loop invocation, factored so the
// cross-tier fallback can reuse it without duplicating the RunToolLoop call.
func (c *Core) runLoop(
	ctx context.Context,
	req Request,
	provider inference.Provider,
	model string,
	isCloud bool,
	loopSink func(agent.LoopEvent),
	requester PermissionRequester,
	convHistory []llm.Message,
	onTextDelta func(string),
	onTurn func(m llm.Message),
	wdGate agent.WatchdogGate,
	wdTurnEnd agent.WatchdogTurnEnd,
	gateRegistry *agenttools.Registry,
	permStore *agent.PermissionStore,
	profile agent.Profile,
	tightContextFallback bool,
) (agent.ToolLoopResult, error) {
	maxIterations := 0
	contextWindow := 0
	if c.d.Config != nil {
		cfgSnap := c.d.Config.Get()
		maxIterations = cfgSnap.ToolLoop.MaxIterations
		contextWindow = c.contextWindowFor(isCloud, model)
	}

	return agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:             provider,
		Registry:             gateRegistry,
		Permissions:          permStore,
		Profile:              profile,
		UserInput:            req.Input,
		Images:               req.Images,
		Model:                model,
		System:               BuildSystemPrompt(c.d, req.WorkDir, profile),
		WorkDir:              req.WorkDir,
		ConversationID:       req.ConversationID,
		VisionStore:          c.d.VisionStore,
		EventSink:            loopSink,
		PermissionRequester:  requester,
		ConvHistory:          convHistory,
		OnTextDelta:          onTextDelta,
		OnTurnComplete:       onTurn,
		MaxIterations:        maxIterations,
		ContextWindow:        contextWindow,
		TightContextFallback: tightContextFallback,
		WatchdogGate:         wdGate,
		WatchdogTurnEnd:      wdTurnEnd,
	})
}

// retryNotice phrases the one-time same-provider turn retry for the user in
// terms of what actually failed: overload, a dropped connection, or an opaque
// hiccup. All three resolve to "trying once more" — the retry is single-shot
// and the cross-tier fallback still follows if it fails again.
func retryNotice(provider string, class llm.ErrorClass) string {
	switch class {
	case llm.ErrNetwork:
		return fmt.Sprintf("⚠ %s connection dropped — trying once more", provider)
	case llm.ErrBusy:
		return fmt.Sprintf("⚠ %s server busy — trying once more", provider)
	default:
		return fmt.Sprintf("⚠ %s hiccup — trying once more", provider)
	}
}

// makeLoopSink builds the agent.LoopEvent → runner.Event adapter.
// It is the single point that maps the tool-loop's internal event vocabulary
// to the runner's proto-free Event type. The host's protoSink then maps
// runner.Event → stream.Send(proto...).
func makeLoopSink(sink EventSink, failures *failurelog.Writer, conversationID string) func(agent.LoopEvent) {
	return func(ev agent.LoopEvent) {
		switch ev.Kind {
		case agent.LoopToolUseStart:
			sink.Emit(Event{
				Kind:      EventToolUseStart,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
			})

		case agent.LoopToolUseStop:
			// Summarize args: Edit/Write carry whole file bodies; truncate so
			// the log and wire stay small. The CLI fetches full args via GetToolCall.
			argsSummary := summarizeArgs(ev.ArgsJSON)
			fmt.Fprintf(os.Stderr, "[tool-loop] call %s args=%s\n", ev.ToolName, argsSummary)
			sink.Emit(Event{
				Kind:        EventToolUseStop,
				ToolUseID:   ev.ToolUseID,
				ToolName:    ev.ToolName,
				ArgsSummary: argsSummary,
			})

		case agent.LoopToolExecStart:
			sink.Emit(Event{Kind: EventProgress, Text: toolProgress(ev.ToolName)})
			sink.Emit(Event{
				Kind:      EventToolExecStart,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
			})

		case agent.LoopToolExecComplete:
			fmt.Fprintf(os.Stderr, "[tool-loop]   -> %s (err=%v) %s\n", ev.Summary, ev.IsError, ev.Detail)
			if ev.IsError && failures != nil {
				failures.Log("main.tool_error", failurelog.Event{
					"scope":           "main",
					"conversation_id": conversationID,
					"tool_name":       ev.ToolName,
					"error_class":     "tool_error",
					"message":         "tool returned an error",
				})
			}
			sink.Emit(Event{
				Kind:      EventToolExecComplete,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
				Summary:   ev.Summary,
				Detail:    ev.Detail,
				StartLine: ev.StartLine,
				IsError:   ev.IsError,
			})

		case agent.LoopProgress:
			if ev.TaskChangeKind != "" {
				sink.Emit(Event{
					Kind:           EventTaskChange,
					TaskChangeKind: ev.TaskChangeKind,
					TaskSnapshot:   taskProgressSnapshotToRunner(ev.TaskSnapshot),
				})
				break
			}
			if ev.SubAgentID != "" {
				sink.Emit(Event{
					Kind:      EventSubAgent,
					Text:      ev.Summary,
					ToolUseID: ev.ToolUseID,
					ToolName:  ev.ToolName,
					// Sub-agent tool_use_stop carries raw args in ArgsJSON; summarize
					// it here (as the main path does) so the child tab shows the tool
					// call instead of a stuck "loading..." placeholder.
					ArgsSummary:      summarizeArgs(ev.ArgsJSON),
					Detail:           ev.Detail,
					Summary:          ev.Summary,
					StartLine:        ev.StartLine,
					IsError:          ev.IsError,
					SubAgentID:       ev.SubAgentID,
					SubAgentParentID: ev.SubAgentParentID,
					SubAgentTitle:    ev.SubAgentTitle,
					SubAgentKind:     ev.SubAgentKind,
					GrantedTools:     append([]string(nil), ev.GrantedTools...),
					IgnoredTools:     append([]string(nil), ev.IgnoredTools...),
				})
				break
			}
			sink.Emit(Event{Kind: EventProgress, Text: ev.Summary, ToolUseID: ev.ToolUseID, ToolName: ev.ToolName})

		case agent.LoopNotice:
			// Resilience-engine narration ("anthropic quota reached — switching
			// to openai"). Logged so backup-served/retried turns are visible in
			// the server log, and forwarded on the progress channel so the CLI
			// shows it as the live status line. Auth failures carry a marker so
			// capable clients can raise an actionable re-auth prompt.
			fmt.Fprintf(os.Stderr, "[resilience] %s\n", ev.Summary)
			text := "⚠ " + ev.Summary
			if strings.Contains(strings.ToLower(ev.Summary), "anthropic auth failed") {
				text = "cercano:reauth-required provider=anthropic profile=claude | " + text
			}
			sink.Emit(Event{Kind: EventProgress, Text: text})

		case agent.LoopWatchdogChallenge:
			sink.Emit(Event{
				Kind:         EventWatchdog,
				WatchdogKind: "challenge",
				Detail:       ev.Detail,
				Summary:      ev.Summary,
			})

		case agent.LoopWatchdogBlock:
			sink.Emit(Event{
				Kind:         EventWatchdog,
				WatchdogKind: "block",
				Detail:       ev.Detail,
				Summary:      ev.Summary,
			})

		case agent.LoopWatchdogEcho:
			sink.Emit(Event{
				Kind:         EventWatchdog,
				WatchdogKind: "echo",
				Summary:      ev.Summary,
				Thread:       ev.ToolName, // LoopEvent reuses ToolName for the echo thread
			})

		case agent.LoopWatchdogEscalate:
			// Escalate: old sink dropped it (no proto send). Runner emits the
			// event so future consumers can act on it, but the host's protoSink
			// must drop it (send nothing) to preserve the old on-wire behavior.
			sink.Emit(Event{
				Kind:         EventWatchdog,
				WatchdogKind: "escalate",
				Detail:       ev.Detail,
				Summary:      ev.Summary,
			})

		case agent.LoopPermissionRequired:
			// LoopPermissionRequired is NOT forwarded via sink — it goes via
			// requester (request/response, not fire-and-forget). The loop calls
			// PermissionRequester directly; we don't emit an event here.
		}
	}
}

func toolProgress(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return "running tool"
	}
	return "running " + name
}

func taskProgressSnapshotToRunner(in agenttools.TaskProgressSnapshot) TaskSnapshot {
	out := TaskSnapshot{
		ID:       in.ID,
		Title:    in.Title,
		Status:   in.Status,
		Notes:    in.Notes,
		ParentID: in.ParentID,
		Children: make([]TaskSnapshot, 0, len(in.Children)),
	}
	for _, child := range in.Children {
		out.Children = append(out.Children, taskProgressSnapshotToRunner(child))
	}
	return out
}

// ── buildSystemPrompt and its helpers ────────────────────────────────────────
//
// These are copies of the same-named helpers in internal/server/server.go.
// They live here so the runner stays proto-free — it cannot import internal/server.
// The server's own buildSystemPrompt remains for runAgenticDispatch and toolSvc.

// BuildSystemPrompt gathers live environment grounding for workDir and renders
// the tool-loop system prompt. Exported so the worker can reuse it for
// sub-agent (Agentic dispatch) system prompts instead of keeping a third copy.
//
// profile is the session's active capability profile for this turn. When it
// fences anything off (e.g. the read-only planning profile), a state-signal
// block is appended so the model KNOWS its current posture — otherwise a model
// that has already entered planning mode has no in-context signal saying so, and
// re-reaches for suggest_plan or is surprised when a write is fenced (FU-1b).
// The zero Profile restricts nothing and adds no block, so the common path is
// unchanged.
func BuildSystemPrompt(d Deps, workDir string, profile agent.Profile) string {
	env := loopEnv{
		WorkDir:  workDir,
		Platform: runtime.GOOS,
		Date:     time.Now().Format("2006-01-02"),
	}
	if workDir != "" {
		env.GitRepo, env.GitBranch = runnerGitInfo(workDir)
	}
	steering := protocols.SteeringBlock(protocols.ForDomain(protocols.DomainCore))
	if sig := profileStateSignal(profile); sig != "" {
		steering = steering + "\n\n" + sig
	}
	projectCtx := ""
	if d.Persist != nil {
		projectCtx = d.Persist.LoadProjectContext(workDir)
	}
	return buildToolLoopSystem(env, steering, runnerDirectorySnapshot(workDir, 80), projectCtx)
}

// profileStateSignal renders the in-context notice of the active capability
// profile, or "" when the profile restricts nothing. It is keyed off the
// profile's Name so a future mode (brainstorm, execute, …) can add its own line
// without touching the prompt builder.
func profileStateSignal(p agent.Profile) string {
	if !p.Restricts() {
		return ""
	}
	switch p.Name {
	case "plan":
		return "<planning-mode>\nYou are currently IN PLANNING MODE (a read-only exploration fence is active). You may read the codebase and author the effort's spec.md and plan.md, but write/exec tools on other files are unavailable until the plan is approved. Do NOT call suggest_plan again — you are already planning; proceed to investigate and author the spec. When the plan is ready, call request_plan_approval to hand off to execution; to abandon planning, call plan_exit.\n</planning-mode>"
	case "autonomous":
		return "<autonomous-mode>\nYou are currently IN AUTONOMOUS MODE. Follow the autonomous-run protocol. Work against the approved run brief: pursue the goal, satisfy the done_when items, honor constraints, and pay attention to review_points. Keep visible progress: emit concise user-visible progress beacons before meaningful phases, long or noisy tool batches, verification, checkpointing, and major slice transitions, then continue working in the same turn. For meaningful in-scope forks, use the design-decision protocol, call capture_decision with the real options/trade-offs/hack flags/counterarguments/reversibility, then continue without asking. Stop mid-run only for high-risk boundary cases: effectively irreversible choices, scope expansion, security/permission/data-loss semantics, destructive operations, push/merge/migration/user-data changes, or when you cannot identify a clean preferred option. A checkpoint boundary is not a pause boundary: after checkpointing a solved unit, continue to the next unsatisfied done_when item or necessary implementation slice instead of ending with a status report. When the brief is satisfied, call request_autonomous_exit to begin final decision review. Walk the user through captured decisions one by one; if they accept, call complete_autonomous_review to mark the run completed and leave autonomous mode.\n</autonomous-mode>"
	default:
		return fmt.Sprintf("<active-profile>\nYou are currently in the %q capability profile, which fences off some tools. Tools outside the profile are unavailable this turn.\n</active-profile>", p.Name)
	}
}

// loopEnv is the environment grounding rendered into the tool-loop system
// prompt's <env> block.
type loopEnv struct {
	WorkDir   string
	Platform  string
	Date      string
	GitRepo   bool
	GitBranch string
}

// buildToolLoopSystem assembles the tool-loop system prompt: an <env> block
// (cwd, platform, date, git), a <directory> snapshot of the cwd's immediate
// children, and the project's .cercano/context.md when present.
func buildToolLoopSystem(env loopEnv, steering, dirSnapshot, projectContext string) string {
	var b strings.Builder
	b.WriteString("You are Cercano, an agentic coding assistant operating in a terminal.\n\n")
	b.WriteString("A note on tool naming: depending on your cloud route, some tools in your schema may appear under a host prefix like `mcp__oc__Read` instead of plain `Read`. That prefix is a wire-level routing artifact from the provider (e.g. an OpenCode/Meridian adapter) — it does not mean you are running inside a different host. You are Cercano either way. Call tools using whatever name is in your schema. But when you pass tool names as data — for example, in the `tools` argument of `dispatch` or `workflow` — always use the plain registered names (Read, Write, Edit, Bash, Glob, Grep, LS, git_info, git_status, etc.) without any host prefix.\n\n")
	b.WriteString("Never end your turn on a promise. Your turn ends the moment you send a reply with no tool calls — anything you say you are \"about to\" do (\"let me check…\", \"running it now…\") will never happen unless you do it in this same turn, with tool calls, before replying. Either do the work now, or state plainly that you are not doing it and why. Never claim you checked, ran, or verified something unless a tool call in this turn actually did it.\n\n")
	b.WriteString("Keep the user oriented during tool-using work. Before the first tool call in a non-trivial turn, say what you are going to do in one short sentence. For long runs or phase changes, emit concise progress beacons before the next meaningful tool batch, verification step, or checkpoint. These beacons are not promises and not pause points: write the update, then do the work in the same turn.\n\n")
	if strings.TrimSpace(steering) != "" {
		b.WriteString(steering)
		b.WriteString("\n\n")
	}
	b.WriteString("<env>\n")
	if env.WorkDir != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", env.WorkDir)
	}
	if env.Platform != "" {
		fmt.Fprintf(&b, "Platform: %s\n", env.Platform)
	}
	if env.Date != "" {
		fmt.Fprintf(&b, "Today's date: %s\n", env.Date)
	}
	if env.GitRepo {
		if env.GitBranch != "" {
			fmt.Fprintf(&b, "Is a git repository: yes (branch %s)\n", env.GitBranch)
		} else {
			b.WriteString("Is a git repository: yes\n")
		}
	} else {
		b.WriteString("Is a git repository: no\n")
	}
	b.WriteString("</env>\n\n")
	b.WriteString("Resolve relative file paths against the working directory; don't search the filesystem to locate the project.\n")
	if strings.TrimSpace(dirSnapshot) != "" {
		b.WriteString("\n<directory>\n")
		b.WriteString(strings.TrimRight(dirSnapshot, "\n"))
		b.WriteString("\n</directory>\n")
	}
	if strings.TrimSpace(projectContext) != "" {
		b.WriteString("\n<project-context>\n")
		b.WriteString(strings.TrimRight(projectContext, "\n"))
		b.WriteString("\n</project-context>\n")
	}
	return b.String()
}

// runnerDirectorySnapshot lists the immediate children of dir — directories
// first (trailing slash), then files — skipping dot-entries, capped at max
// with a "(N more)" note.
func runnerDirectorySnapshot(dir string, max int) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	all := append(dirs, files...)
	more := 0
	if len(all) > max {
		more = len(all) - max
		all = all[:max]
	}
	out := strings.Join(all, "\n")
	if more > 0 {
		out += fmt.Sprintf("\n… (%d more)", more)
	}
	return out
}

// runnerGitInfo reports whether dir is inside a git work tree and its current
// branch.
func runnerGitInfo(dir string) (bool, string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(out))
}

// ── summarizeArgs and helper ──────────────────────────────────────────────────
//
// Proto-free copy of the same function from internal/server/toolargs.go.
// Moved here so the runner can compute ArgsSummary without importing server.

const maxArgFieldRunes = 200

// summarizeArgs returns a compact, still-valid-JSON rendering of a tool call's
// arguments with any oversized top-level string value truncated.
func summarizeArgs(argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return truncateRunes(argsJSON, maxArgFieldRunes*4)
	}
	changed := false
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if t := truncateRunes(s, maxArgFieldRunes); t != s {
			m[k] = t
			changed = true
		}
	}
	if !changed {
		return argsJSON
	}
	b, err := json.Marshal(m)
	if err != nil {
		return truncateRunes(argsJSON, maxArgFieldRunes*4)
	}
	return string(b)
}

// truncateRunes caps s to max runes, appending an ellipsis when it cuts.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
