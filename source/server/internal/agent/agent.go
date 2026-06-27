package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/locus"
)

// ProgressFunc defines a callback for progress updates.
type ProgressFunc func(message string)

// Coordinator defines the interface for the iterative generation loop.
type Coordinator interface {
	Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error)
}

// AgentOption configures optional Agent dependencies.
type AgentOption func(*Agent)

// WithConversationStore attaches a ConversationStore for multi-turn history.
func WithConversationStore(cs *ConversationStore) AgentOption {
	return func(a *Agent) {
		a.conversation = cs
	}
}

// WithPersistentStore attaches a SQLite-backed persistent conversation store.
// When set, the agent dual-writes every turn (in-memory for fast history
// during the active session, persistent for cross-restart /history & /resume).
// If nil, persistence is silently skipped.
func WithPersistentStore(ps conversation.Store) AgentOption {
	return func(a *Agent) {
		a.persistent = ps
	}
}

// WithContextMeter attaches a per-conversation token-usage registry. When set,
// the agent updates each conversation's counter on every turn (using upstream
// token counts when available, falling back to tokenizer-based estimates).
// The GetContextUsage RPC reads from this registry. If nil, the CLI status
// bar shows zeros.
func WithContextMeter(reg *contextmeter.Registry, modelForMax string) AgentOption {
	return func(a *Agent) {
		a.meter = reg
		a.meterModel = modelForMax
	}
}

// ContextLoader is the interface for loading project context. Implementations
// look up .cercano/context.md (or equivalent) under a project directory.
// Kept narrow so the agent doesn't pull the full internal/context package
// surface (and so we can stub it in tests).
type ContextLoader interface {
	PrependContext(projectDir, prompt string) string
}

// WithContextLoader attaches a project-context loader. When the request
// carries a work_dir, the agent prepends the project's .cercano/context.md
// to the prompt so the model has project awareness on every turn.
func WithContextLoader(l ContextLoader) AgentOption {
	return func(a *Agent) { a.contextLoader = l }
}

// Agent is the top-level orchestrator for AI requests.
type Agent struct {
	router        Router
	coordinator   Coordinator
	conversation  *ConversationStore
	persistent    conversation.Store
	meter         *contextmeter.Registry
	meterModel    string // active local model name, used as Max() baseline
	contextLoader ContextLoader
	recap         RecapScheduler
	compaction    CompactionScheduler
	locusMode     func() string // live getter for the configured Locus Mode
}

// RecapScheduler requests a (debounced) recap regeneration for a conversation.
// Implemented by *recap.Generator; kept as an interface so agent doesn't import
// the recap package.
type RecapScheduler interface {
	Schedule(conversationID string)
}

// WithRecapScheduler attaches the living-recap generator. After each persisted
// assistant turn the agent calls Schedule to refresh the conversation recap.
func WithRecapScheduler(rs RecapScheduler) AgentOption {
	return func(a *Agent) { a.recap = rs }
}

// ScheduleRecap requests a debounced recap regeneration for a conversation.
// Nil-safe — a no-op when no scheduler is attached. Exported so callers that
// persist turns outside the agent (e.g. the tool-loop streaming path on the
// server) can refresh the recap too.
func (a *Agent) ScheduleRecap(conversationID string) {
	if a.recap != nil {
		a.recap.Schedule(conversationID)
	}
}

// CompactionScheduler requests background context compaction for a conversation
// and can run it synchronously. Implemented by *compactiongen.Generator; an
// interface so agent doesn't import the generator package.
type CompactionScheduler interface {
	Schedule(conversationID string)
	CompactNow(ctx context.Context, conversationID string) error
	IsCompacting(conversationID string) bool
}

// WithCompactionScheduler attaches the background compaction generator.
func WithCompactionScheduler(cs CompactionScheduler) AgentOption {
	return func(a *Agent) { a.compaction = cs }
}

// SetLocusModeGetter wires a live getter for the active Locus Mode so co-proc
// routing reflects runtime UpdateConfig changes. Nil getter → DefaultMode.
func (a *Agent) SetLocusModeGetter(f func() string) { a.locusMode = f }

func (a *Agent) currentLocusMode() string {
	if a.locusMode == nil {
		return string(locus.DefaultMode)
	}
	return a.locusMode()
}

// ScheduleCompaction requests a debounced compaction pass. Nil-safe.
func (a *Agent) ScheduleCompaction(conversationID string) {
	if a.compaction != nil {
		a.compaction.Schedule(conversationID)
	}
}

// CompactNow runs a synchronous compaction pass. Nil-safe (returns nil).
func (a *Agent) CompactNow(ctx context.Context, conversationID string) error {
	if a.compaction != nil {
		return a.compaction.CompactNow(ctx, conversationID)
	}
	return nil
}

// IsCompacting reports whether a compaction pass is currently running. Nil-safe.
func (a *Agent) IsCompacting(conversationID string) bool {
	if a.compaction != nil {
		return a.compaction.IsCompacting(conversationID)
	}
	return false
}

// NewAgent creates a new Agent orchestrator.
func NewAgent(r Router, c Coordinator, opts ...AgentOption) *Agent {
	a := &Agent{
		router:      r,
		coordinator: c,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// loadHistory loads conversation history and returns (augmentedInput, originalInput).
// If no store is configured or no history exists, augmentedInput == originalInput.
func (a *Agent) loadHistory(ctx context.Context, req *Request) (augmented, original string) {
	original = req.Input
	augmented = original

	// Prepend project context (.cercano/context.md under req.WorkDir) so the
	// model has project awareness on every turn. Done before history so the
	// history reflects what the model was actually asked.
	if a.contextLoader != nil && req.WorkDir != "" {
		augmented = a.contextLoader.PrependContext(req.WorkDir, augmented)
	}

	if a.conversation == nil || req.ConversationID == "" {
		return
	}

	history, err := a.conversation.LoadHistory(ctx, req.ConversationID)
	if err != nil || history == "" {
		return
	}

	augmented = history + "\n" + augmented
	return
}

// storeConversationTurn compacts the response and stores the turn in BOTH
// the in-memory session store (fast intra-session history) AND the persistent
// SQLite store (cross-restart /history & /resume), if configured.
//
// augmentedInput is the FULL prompt sent to the model — including project
// context (.cercano/context.md prepended) and conversation history. It's
// used as the input side of the context-window meter so the meter reflects
// "size of what was sent" rather than just the user's last typed line.
// originalInput is the user's bare text and is what gets persisted to the
// conversation store.
func (a *Agent) storeConversationTurn(ctx context.Context, conversationID, originalInput, augmentedInput string, resp *Response) {
	if conversationID == "" {
		return
	}
	compacted := CompactResponse(resp)
	if a.conversation != nil {
		_ = a.conversation.AppendTurn(ctx, conversationID, originalInput, compacted)
	}
	// Context meter — SNAPSHOT semantics, not cumulative. After each turn,
	// the meter reflects the size of the most recently sent prompt + the
	// response that came back. That's the "context window fill" indicator
	// the CLI status bar is meant to show. Reset first so multi-turn growth
	// reflects actual conversation size, not lifetime spend.
	if a.meter != nil {
		c := a.meter.Get(conversationID, a.meterModel)
		c.Reset()
		if augmentedInput != "" {
			c.Add(augmentedInput)
		} else {
			// Coordinator path may not pass augmentedInput; fall back to the
			// user's text + Ollama-reported input tokens if available.
			if resp.InputTokens > 0 {
				c.AddCount(resp.InputTokens)
			} else {
				c.Add(originalInput)
			}
		}
		if resp.OutputTokens > 0 {
			c.AddCount(resp.OutputTokens)
		} else {
			c.Add(compacted)
		}
	}

	if a.persistent != nil {
		// projectDir comes from the request the agent processed; we don't
		// carry it onto this helper, so EnsureConversation here uses empty
		// project. The next refactor can thread workDir through.
		if err := a.persistent.EnsureConversation(ctx, conversationID, "", ""); err != nil {
			fmt.Fprintf(os.Stderr, "[persistent-store] EnsureConversation(%s) failed: %v\n", conversationID, err)
		}
		if err := a.persistent.Append(ctx, conversation.Turn{
			ConversationID: conversationID,
			Role:           "user",
			Content:        originalInput,
			TokensIn:       resp.InputTokens,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "[persistent-store] Append(user, %s) failed: %v\n", conversationID, err)
		}
		if err := a.persistent.Append(ctx, conversation.Turn{
			ConversationID: conversationID,
			Role:           "assistant",
			Content:        compacted,
			TokensOut:      resp.OutputTokens,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "[persistent-store] Append(assistant, %s) failed: %v\n", conversationID, err)
		}
		a.ScheduleRecap(conversationID)
		a.ScheduleCompaction(conversationID)
	}
}

// ResumeConversation loads persisted turns for the given conversation id and
// hydrates them into the in-memory session store, so that the next call to
// loadHistory sees full context. Returns the persisted turns so the caller
// (CLI) can render them in the scrollback. Also rebuilds the conversation's
// context-meter counter from the persisted token totals (or tokenizer
// estimates) so the CLI's status bar shows accurate accumulated usage right
// after resume. If no persistent store is configured the returned slice is
// empty.
func (a *Agent) ResumeConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error) {
	if a.persistent == nil {
		return nil, nil
	}
	turns, err := a.persistent.GetTurns(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	// Rebuild the meter so the CLI sees accurate context-window usage
	// immediately after /resume.
	if a.meter != nil {
		a.meter.Drop(conversationID)
		c := a.meter.Get(conversationID, a.meterModel)
		for _, t := range turns {
			switch {
			case t.Role == "user" && t.TokensIn > 0:
				c.AddCount(t.TokensIn)
			case t.Role == "assistant" && t.TokensOut > 0:
				c.AddCount(t.TokensOut)
			default:
				c.Add(t.Content)
			}
		}
	}
	if a.conversation == nil {
		return turns, nil
	}
	// Rehydrate the in-memory store by replaying user/assistant pairs.
	var pendingUser string
	for _, t := range turns {
		switch t.Role {
		case "user":
			pendingUser = t.Content
		case "assistant":
			if pendingUser != "" {
				_ = a.conversation.AppendTurn(ctx, conversationID, pendingUser, t.Content)
				pendingUser = ""
			}
		}
	}
	return turns, nil
}

// PersistentStore returns the configured persistent conversation store, or
// nil if none was attached. The new tool-loop path in the server reaches
// through here to dual-write turns the same way storeConversationTurn does.
func (a *Agent) PersistentStore() conversation.Store {
	if a == nil {
		return nil
	}
	return a.persistent
}

// RecordContextUsage snapshots a conversation's context-window meter from
// provider-reported token usage, measured against the given model's window
// (typically the cloud model that served the turn). Snapshot semantics:
// reset then set used = inputTokens + outputTokens. No-op when no meter is
// configured or inputTokens <= 0 (a provider that reports no usage must not
// clobber a prior good reading).
func (a *Agent) RecordContextUsage(convID, model string, inputTokens, outputTokens int) {
	if a == nil || a.meter == nil || convID == "" || inputTokens <= 0 {
		return
	}
	c := a.meter.Get(convID, model)
	c.SetMax(contextmeter.ModelMax(model))
	c.Reset()
	c.AddCount(inputTokens + outputTokens)
}

// GetContextUsage reports the current token usage for a conversation. Used
// is the cumulative tokens spent, max is the model's conventional context
// window. Both zero if no meter is configured or the conversation has no
// turns yet.
func (a *Agent) GetContextUsage(ctx context.Context, conversationID string) (used, max int) {
	if a == nil || a.meter == nil || conversationID == "" {
		return 0, 0
	}
	c := a.meter.Get(conversationID, a.meterModel)
	return c.Used(), c.Max()
}

// ListConversations returns persisted conversation summaries (project filter
// + limit). Empty slice if no persistent store is configured.
func (a *Agent) ListConversations(ctx context.Context, projectDir string, limit int) ([]conversation.Info, error) {
	if a.persistent == nil {
		return nil, nil
	}
	return a.persistent.List(ctx, projectDir, limit)
}

// GetConversation returns a single conversation's Info, including its recap.
func (a *Agent) GetConversation(ctx context.Context, conversationID string) (conversation.Info, error) {
	if a.persistent == nil {
		return conversation.Info{}, fmt.Errorf("no persistent store configured")
	}
	return a.persistent.Get(ctx, conversationID)
}

// DeleteConversation removes a conversation and its turns from the persistent
// store. The in-memory session store is not touched (it'll garbage-collect
// when the agent restarts).
func (a *Agent) DeleteConversation(ctx context.Context, conversationID string) error {
	if a.persistent == nil {
		return nil
	}
	return a.persistent.Delete(ctx, conversationID)
}

// RenameConversation sets a custom title for a persisted conversation. A
// user-chosen title overrides the auto-derived one and is never overwritten
// by subsequent turn appends.
func (a *Agent) RenameConversation(ctx context.Context, conversationID, title string) error {
	if a.persistent == nil {
		return nil
	}
	return a.persistent.Rename(ctx, conversationID, title)
}

// ProcessRequest orchestrates the flow: Route -> Classify -> Execute Strategy.
func (a *Agent) ProcessRequest(ctx context.Context, req *Request) (*Response, error) {
	if req.Coproc {
		return a.processCoproc(ctx, req)
	}

	// Load conversation history
	augmentedInput, originalInput := a.loadHistory(ctx, req)

	// Direct local bypass — skip SmartRouter for co-processor tools
	if req.DirectLocal {
		fmt.Println("Agent: DirectLocal — bypassing SmartRouter, using local provider.")
		local := a.router.GetModelProviders()["LocalModel"]
		augReq := &Request{Input: augmentedInput, DirectLocal: true, ModelOverride: req.ModelOverride}
		res, err := local.Process(ctx, augReq)
		if err != nil {
			return nil, err
		}
		modelName := local.Name()
		if req.ModelOverride != "" {
			modelName = req.ModelOverride
		}
		res.RoutingMetadata = RoutingMetadata{ModelName: modelName, Confidence: 1.0}
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
		return res, nil
	}

	// 1. Classify Intent (uses original input — no history pollution)
	intent, err := a.router.ClassifyIntent(req)
	if err != nil {
		return nil, fmt.Errorf("failed to classify intent: %w", err)
	}

	// 2. Select Provider
	provider, err := a.router.SelectProvider(req, intent)
	if err != nil {
		return nil, fmt.Errorf("failed to select provider: %w", err)
	}

	// Explicit override: If user says "use cloud", force CloudModel
	if strings.Contains(strings.ToLower(req.Input), "use cloud") {
		fmt.Println("Agent: Explicit 'use cloud' detected. Overriding routing to CloudModel.")
		if cloud, ok := a.router.GetModelProviders()["CloudModel"]; ok {
			provider = cloud
		}
	}

	// 3. Execute Strategy (uses augmented input so LLM can resolve references)
	if intent == IntentCoding && req.WorkDir != "" && req.FileName != "" {
		targetFile := req.FileName
		// If user asks for unit tests specifically, ensure we are targeting a _test.go file
		lowerInput := strings.ToLower(req.Input)
		if (strings.Contains(lowerInput, "unit test") || strings.Contains(lowerInput, "unit tests")) && !strings.HasSuffix(targetFile, "_test.go") {
			targetFile = strings.TrimSuffix(targetFile, ".go") + "_test.go"
			fmt.Printf("Agent: Adjusting target filename for unit test generation: %s\n", targetFile)
		}

		fmt.Printf("Agent: Detected Coding intent. Executing Coordinator Loop in %s for %s...\n", req.WorkDir, targetFile)
		res, err := a.coordinator.Coordinate(ctx, augmentedInput, "", req.WorkDir, targetFile, nil)
		if err != nil {
			return nil, fmt.Errorf("agentic loop failed: %w", err)
		}
		res.RoutingMetadata = RoutingMetadata{
			ModelName:  provider.Name(),
			Confidence: 1.0,
		}
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
		return res, nil
	}

	fmt.Printf("Agent: Executing direct call with provider: %s\n", provider.Name())
	// Use augmented input for the provider call
	augReq := &Request{
		Input:          augmentedInput,
		WorkDir:        req.WorkDir,
		FileName:       req.FileName,
		ConversationID: req.ConversationID,
	}
	res, err := provider.Process(ctx, augReq)
	if err != nil {
		// If the failing provider isn't the local one, degrade to local and
		// surface a notice. NEVER silently mock; the user always knows.
		if newRes, ok := a.degradeIfCloudFailure(ctx, provider, augReq, err, nil); ok {
			res = newRes
			err = nil
			provider = a.router.GetModelProviders()["LocalModel"]
		}
	}
	if err != nil {
		return nil, err
	}

	if len(res.FileChanges) > 0 {
		fmt.Printf("Agent: WARNING - Provider %s returned %d file changes for non-coordinator request. Clearing them.\n", provider.Name(), len(res.FileChanges))
		res.FileChanges = nil
	}

	res.RoutingMetadata = RoutingMetadata{
		ModelName:  provider.Name(),
		Confidence: 1.0,
	}
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
	return res, nil
}

// processCoproc serves a one-shot co-processor request on the tier chosen by
// the active Locus Mode. Bidirectional fallback for *_primary; hard error for
// *_only when its tier is unavailable. Sets IsCloud + a Notice on fallback.
func (a *Agent) processCoproc(ctx context.Context, req *Request) (*Response, error) {
	augmentedInput, originalInput := a.loadHistory(ctx, req)

	mode, _ := locus.ParseMode(a.currentLocusMode())
	res := mode.Coproc()
	providers := a.router.GetModelProviders()

	pick := func(t locus.Tier) ModelProvider {
		if t == locus.TierCloud {
			if cp := providers["CloudModel"]; cp != nil && cp.Name() != "NONE" {
				return cp
			}
			return nil
		}
		if lp := providers["LocalModel"]; lp != nil {
			return lp
		}
		return nil
	}

	prov := pick(res.Preferred)
	isCloud := res.Preferred == locus.TierCloud
	fellBack := false
	if prov == nil && res.CrossAllowed {
		prov = pick(res.Fallback)
		isCloud = res.Fallback == locus.TierCloud
		fellBack = true
	}
	if prov == nil {
		return nil, fmt.Errorf("locus mode %q: no %s provider available for co-processor work", mode, res.Preferred)
	}

	out, err := prov.Process(ctx, &Request{Input: augmentedInput, ModelOverride: req.ModelOverride})
	if err != nil {
		return nil, err
	}
	modelName := prov.Name()
	if req.ModelOverride != "" {
		modelName = req.ModelOverride
	}
	out.RoutingMetadata = RoutingMetadata{ModelName: modelName, Confidence: 1.0, IsCloud: isCloud}
	if fellBack {
		out.Notice = fmt.Sprintf("locus: preferred co-processor tier unavailable — ran on %s (%s)", res.Fallback, modelName)
	}
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, out)
	return out, nil
}

// ProcessRequestStream orchestrates the flow with progress updates.
// tokenProgress delivers incremental LLM tokens for chat-path streaming.
func (a *Agent) ProcessRequestStream(ctx context.Context, req *Request, progress ProgressFunc, tokenProgress TokenFunc) (*Response, error) {
	if progress == nil {
		progress = func(string) {}
	}

	// Load conversation history
	augmentedInput, originalInput := a.loadHistory(ctx, req)

	// 1. Classify Intent (uses original input — no history pollution)
	intent, err := a.router.ClassifyIntent(req)
	if err != nil {
		return nil, fmt.Errorf("failed to classify intent: %w", err)
	}
	progress(fmt.Sprintf("Classifying Intent... %s", intent))

	// 2. Select Provider
	provider, err := a.router.SelectProvider(req, intent)
	if err != nil {
		return nil, fmt.Errorf("failed to select provider: %w", err)
	}
	progress(fmt.Sprintf("Selecting Provider... %s", provider.Name()))

	// Explicit override: If user says "use cloud", force CloudModel
	if strings.Contains(strings.ToLower(req.Input), "use cloud") {
		fmt.Println("Agent: Explicit 'use cloud' detected. Overriding routing to CloudModel.")
		if cloud, ok := a.router.GetModelProviders()["CloudModel"]; ok {
			provider = cloud
			progress("Selecting Provider... CloudModel (Override)")
		}
	}

	// Announce the chosen route so the client can show a live engine badge.
	if req.OnRoute != nil {
		cloudP := a.router.GetModelProviders()["CloudModel"]
		req.OnRoute(provider.Name(), cloudP != nil && provider == cloudP)
	}

	// 3. Execute Strategy (uses augmented input so LLM can resolve references)
	if intent == IntentCoding && req.WorkDir != "" && req.FileName != "" {
		targetFile := req.FileName

		// Streaming path: use CoordinateStream when available.
		if sc, ok := a.coordinator.(StreamableCoordinator); ok {
			fmt.Printf("Agent: Detected Coding intent (streaming). Executing CoordinateStream in %s for %s...\n", req.WorkDir, targetFile)
			progress(fmt.Sprintf("Generating and Validating Code (%s)...", provider.Name()))

			events, finalize, err := sc.CoordinateStream(ctx, augmentedInput, "", req.WorkDir, targetFile)
			if err != nil {
				return nil, fmt.Errorf("agentic loop setup failed: %w", err)
			}

			for event, runErr := range events {
				if runErr != nil {
					return nil, fmt.Errorf("agentic loop error: %w", runErr)
				}
				if msg := MapEventToProgress(event); msg != "" {
					progress(msg)
				}
			}

			res, err := finalize()
			if err != nil {
				return nil, fmt.Errorf("agentic loop finalize failed: %w", err)
			}
			res.RoutingMetadata = RoutingMetadata{
				ModelName:  provider.Name(),
				Confidence: 1.0,
				Escalated:  res.RoutingMetadata.Escalated,
			}
			a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
			return res, nil
		}

		// Fallback: non-streamable coordinator.
		fmt.Printf("Agent: Detected Coding intent. Executing Coordinator Loop in %s for %s...\n", req.WorkDir, req.FileName)
		progress(fmt.Sprintf("Generating and Validating Code (%s)...", provider.Name()))
		res, err := a.coordinator.Coordinate(ctx, augmentedInput, "", req.WorkDir, req.FileName, progress)
		if err != nil {
			return nil, fmt.Errorf("agentic loop failed: %w", err)
		}
		res.RoutingMetadata = RoutingMetadata{
			ModelName:  provider.Name(),
			Confidence: 1.0,
			Escalated:  res.RoutingMetadata.Escalated,
		}
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
		return res, nil
	}

	progress(fmt.Sprintf("Generating Response (%s)...", provider.Name()))
	fmt.Printf("Agent: Executing direct call with provider: %s\n", provider.Name())
	// Use augmented input for the provider call
	augReq := &Request{
		Input:          augmentedInput,
		WorkDir:        req.WorkDir,
		FileName:       req.FileName,
		ConversationID: req.ConversationID,
	}

	var res *Response
	if sp, ok := provider.(StreamingModelProvider); ok && tokenProgress != nil {
		res, err = sp.ProcessStream(ctx, augReq, tokenProgress)
	} else {
		res, err = provider.Process(ctx, augReq)
	}
	if err != nil {
		if newRes, ok := a.degradeIfCloudFailure(ctx, provider, augReq, err, progress); ok {
			res = newRes
			err = nil
			provider = a.router.GetModelProviders()["LocalModel"]
		}
	}
	if err != nil {
		return nil, err
	}

	if len(res.FileChanges) > 0 {
		fmt.Printf("Agent: WARNING - Provider %s returned %d file changes for non-coordinator request. Clearing them.\n", provider.Name(), len(res.FileChanges))
		res.FileChanges = nil
	}

	res.RoutingMetadata = RoutingMetadata{
		ModelName:  provider.Name(),
		Confidence: 1.0,
	}
	progress(fmt.Sprintf("Generating Response (%s)... Done.", provider.Name()))
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, res)
	return res, nil
}
