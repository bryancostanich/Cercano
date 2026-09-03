// Package persistence provides the conversation/persistence service — the
// single home for conversation store access, turn persistence, context assembly,
// retention sweeping, compaction scheduling, and project-context loading.
//
// The Service interface is what the front door (Server) depends on; the
// concrete svc type holds the three owned fields (retentionSweeper,
// compactionGen, contextLoader) and the collaborators needed to implement the
// conversation/context RPC bodies without reaching back into Server.
package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactiongen"
	"cercano/source/server/internal/compactor"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextedit"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/requestassembly"
	"cercano/source/server/internal/retention"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// ConvAgent is the subset of *agent.Agent the persistence service needs.
// Defined here so tests can inject a fake without importing the full agent.
type ConvAgent interface {
	PersistentStore() conversation.Store
	ListConversations(ctx context.Context, projectDir string, limit int) ([]conversation.Info, error)
	GetConversation(ctx context.Context, conversationID string) (conversation.Info, error)
	ResumeConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	DeleteConversation(ctx context.Context, conversationID string) error
	RenameConversation(ctx context.Context, conversationID, title string) error
	IsCompacting(conversationID string) bool
	ScheduleCompaction(conversationID string)
}

// Service is the interface the front door (Server) depends on for conversation
// persistence and context assembly.
type Service interface {
	// PersistTurn writes one turn (any role) to the conversation store.
	// Best-effort: store errors are logged but never surfaced.
	PersistTurn(ctx context.Context, convID string, m llm.Message)

	// AssembleHistory builds the conversation history to send to the model —
	// the compacted view when compaction state exists, else the full history.
	// Unlike the legacy helper it replaced, AssembleHistory gets the store
	// from the service itself (no store parameter).
	AssembleHistory(ctx context.Context, convID string) []llm.Message

	// AssembleHistoryForTarget builds the same provider-facing send view for a
	// concrete provider/model attempt and returns structured token accounting.
	AssembleHistoryForTarget(ctx context.Context, convID string, target requestassembly.Target) requestassembly.Result

	// Store returns the live conversation store, or nil if none is wired.
	Store() conversation.Store

	// LoadProjectContext returns the project's .cercano/context.md content,
	// or "" if no loader is wired or the file doesn't exist. Nil-safe.
	LoadProjectContext(workDir string) string

	// ContextLoader exposes the wired project-context loader so the front door
	// can pass it to InstallCapabilities (capabilities.Services.ProjectCtx).
	ContextLoader() *projectctx.Loader

	// CompactionGen exposes the generator so RegenerateContext can call
	// compactionGen.Regenerate without reaching back into Server.
	CompactionGen() *compactiongen.Generator

	// UpdateRetentionConfig hot-reloads the retention sweeper config.
	// No-op if no sweeper is wired.
	UpdateRetentionConfig(cfg retention.Config)

	// SetCompactionEnabled enables/disables the background compaction generator.
	// No-op if no generator is wired.
	SetCompactionEnabled(enabled bool)

	// SetToolElisionOnly flips tool-elision-only mode on the generator: passes
	// keep their normal triggers but advance the elision floor instead of
	// calling the summarizer. No-op if no generator is wired.
	SetToolElisionOnly(v bool)

	// Setters called by wiring code (cmd/cercano or watchdog_wire).
	SetRetentionSweeper(sw *retention.Sweeper)
	SetCompactionGenerator(g *compactiongen.Generator)
	SetContextLoader(l *projectctx.Loader)

	// RecordTurnContextUsage caches the exact provider-facing request
	// accounting captured while serving a turn, so the context meter survives
	// an agent restart instead of resetting to "unknown". Best-effort: a cache
	// write must never fail a turn.
	RecordTurnContextUsage(ctx context.Context, convID string, u TurnContextUsage)

	// RPC-body implementations (front door delegates to these).
	ListConversations(ctx context.Context, req *proto.ListConversationsRequest) (*proto.ListConversationsResponse, error)
	GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error)
	ResumeConversation(ctx context.Context, req *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error)
	StreamResumeConversation(req *proto.ResumeConversationRequest, stream proto.Agent_StreamResumeConversationServer) error
	StreamResumeConversationViewportFirst(req *proto.ResumeConversationViewportFirstRequest, stream proto.Agent_StreamResumeConversationViewportFirstServer) error
	DeleteConversation(ctx context.Context, req *proto.DeleteConversationRequest) (*proto.DeleteConversationResponse, error)
	RenameConversation(ctx context.Context, req *proto.RenameConversationRequest) (*proto.RenameConversationResponse, error)
	GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error)
	ListSubAgents(ctx context.Context, req *proto.ListSubAgentsRequest) (*proto.ListSubAgentsResponse, error)
	DismissSubAgent(ctx context.Context, req *proto.DismissSubAgentRequest) (*proto.DismissSubAgentResponse, error)
	GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error)
	GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error)
	ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error)
	ElideContext(ctx context.Context, req *proto.ElideContextRequest) (*proto.ElideContextResponse, error)
	RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error
	ProposeContextEdit(ctx context.Context, req *proto.ProposeContextEditRequest) (*proto.ProposeContextEditResponse, error)
	DeleteConversationTurns(ctx context.Context, req *proto.DeleteConversationTurnsRequest) (*proto.DeleteConversationTurnsResponse, error)
	SuggestNextPrompt(ctx context.Context, req *proto.SuggestNextPromptRequest) (*proto.SuggestNextPromptResponse, error)
}

// TurnContextUsage is the provider-facing request accounting captured while
// serving one turn. Unlike a compaction pass, a real turn knows the true system
// prompt and tool-schema costs, so this is the highest-fidelity snapshot the
// meter can cache.
type TurnContextUsage struct {
	MessageTokens          int
	SystemTokens           int
	ToolSchemaTokens       int
	OutputReserveTokens    int
	EstimatedRequestTokens int
	ContextWindow          int
	ContextWindowKnown     bool
}

// Provenance values for a cached context-usage snapshot. "turn" is exact
// provider-facing accounting captured while serving a real turn; "compaction"
// is the post-pass send-view total a compaction pass already computed.
const (
	contextUsageSourceTurn       = "turn"
	contextUsageSourceCompaction = "compaction"
	// contextUsageSourceRawEstimate is the cold-start fallback: raw storage
	// size only, with no compaction/system/tool-schema accounting. Kept
	// distinct so it is never mistaken for a measured provider request.
	contextUsageSourceRawEstimate = "raw_estimate"
)

// svc is the concrete implementation of Service.
type svc struct {
	// convAgent wraps the *agent.Agent for conversation store access and
	// conversation CRUD. The service does NOT own *agent.Agent.
	convAgent ConvAgent

	// cfgSvc is used by AssembleHistory and GetContextUsage to read compaction
	// config at call time (not at construction time).
	cfgSvc cfgsvc.Service

	// primaryModel returns the model name the context meter measures against.
	// Injected as a func so it always reads from the live provider state.
	primaryModel func() string

	// activeCloudModel returns the cloud model from the active profile.
	// Used by AssembleHistory for hard-override limit calculation.
	activeCloudModel func() string

	// engine returns the dispatch engine for SuggestNextPrompt.
	// May return nil (suggest degrades to empty response).
	engine func() *dispatch.Engine

	// openTurnRunner returns the current open turn runner for ProposeContextEdit.
	openTurnRunner func() agent.TurnRunner

	// cloudProvider returns the cloud LLM provider for ProposeContextEdit.
	cloudProvider func() inference.Provider

	// cloudModel returns the active cloud model string for ProposeContextEdit.
	cloudModel func() string

	// Owned fields (moved off Server.struct).
	retentionSweeper *retention.Sweeper
	compactionGen    *compactiongen.Generator
	contextLoader    *projectctx.Loader

	// elisionFloors holds each conversation's /elide-context floor: tool
	// results in turns at or before this unix-seconds timestamp are stubbed at
	// context-assembly time. In-memory by design — stored raw turns are never
	// touched, and the floor resets when the agent restarts.
	elideMu       sync.Mutex
	elisionFloors map[string]int64
}

// New constructs a Service.
//
//   - convAgent: the *agent.Agent (or a test fake) for store access + conversation CRUD.
//   - cfgSvc: config service; AssembleHistory reads compaction settings from it.
//   - primaryModel: func returning the primary model name (for GetContextUsage denominator).
//   - activeCloudModel: func returning the active cloud model (for AssembleHistory hard-override).
//   - engine: func returning the dispatch engine (for SuggestNextPrompt); may return nil.
//   - openTurnRunner: func returning the current open turn runner (for ProposeContextEdit); may return nil.
//   - cloudProvider: func returning the cloud LLM provider (for ProposeContextEdit); may return nil.
//   - cloudModel: func returning the active cloud model string (for ProposeContextEdit); may return "".
func New(
	convAgent ConvAgent,
	cfgSvc cfgsvc.Service,
	primaryModel func() string,
	activeCloudModel func() string,
	engine func() *dispatch.Engine,
	openTurnRunner func() agent.TurnRunner,
	cloudProvider func() inference.Provider,
	cloudModel func() string,
) Service {
	return &svc{
		convAgent:        convAgent,
		cfgSvc:           cfgSvc,
		primaryModel:     primaryModel,
		activeCloudModel: activeCloudModel,
		engine:           engine,
		openTurnRunner:   openTurnRunner,
		cloudProvider:    cloudProvider,
		cloudModel:       cloudModel,
		elisionFloors:    map[string]int64{},
	}
}

// elisionFloor returns the conversation's /elide-context floor, 0 if unset.
func (x *svc) elisionFloor(convID string) int64 {
	x.elideMu.Lock()
	defer x.elideMu.Unlock()
	return x.elisionFloors[convID]
}

// setElisionFloor advances the conversation's floor; it never regresses, so an
// automatic tool-elision-only pass can't undo a manual /elide-context.
func (x *svc) setElisionFloor(convID string, floor int64) {
	x.elideMu.Lock()
	defer x.elideMu.Unlock()
	if floor > x.elisionFloors[convID] {
		x.elisionFloors[convID] = floor
	}
}

// advanceElisionFloor is the tool-elision-only compaction pass, wired into the
// generator via SetElideOnlyFn: apply the compaction activation gate, then
// move the conversation's elision floor up to the newest turn outside the
// verbatim-recent window. No summarizer, no persisted state — the floor is
// in-memory and the stored raw turns are untouched.
func (x *svc) advanceElisionFloor(ctx context.Context, convID string) (pre, post, stubbed int, changed bool, err error) {
	store := x.Store()
	if store == nil {
		return 0, 0, 0, false, fmt.Errorf("no conversation store")
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return 0, 0, 0, false, err
	}
	if len(turns) == 0 {
		return 0, 0, 0, false, nil
	}
	compSnap := x.cfgSvc.Get().Compaction
	raw := requestassembly.EstimateRawTokens(turns)
	if raw < compSnap.ActivationFloorTokens {
		return 0, 0, 0, false, nil // activation gate — same as the LLM pass
	}
	floor, ok := compactor.ElisionFloor(turns, compSnap.VerbatimRecent)
	if !ok || floor <= x.elisionFloor(convID) {
		return 0, 0, 0, false, nil // nothing new outside the verbatim window
	}
	state, _ := store.GetCompaction(ctx, convID)
	pre = x.sentViewTokens(convID, turns, state, raw)
	_, stubbed = compactor.StubToolResultsThrough(turns, floor)
	x.setElisionFloor(convID, floor)
	post = x.sentViewTokens(convID, turns, state, raw)
	return pre, post, stubbed, true, nil
}

// --- Setters for owned fields ---

// SetRetentionSweeper attaches the background retention sweeper.
func (x *svc) SetRetentionSweeper(sw *retention.Sweeper) { x.retentionSweeper = sw }

// SetCompactionGenerator attaches the background compaction scheduler and
// wires the tool-elision-only pass implementation (the floors live here).
func (x *svc) SetCompactionGenerator(g *compactiongen.Generator) {
	x.compactionGen = g
	if g != nil {
		g.SetElideOnlyFn(x.advanceElisionFloor)
		g.SetContextUsageFn(x.recordCompactionContextUsage)
	}
}

// recordCompactionContextUsage caches the context accounting a compaction pass
// already computed. The pass supplies the send-view total (and, when it loaded
// turns, the raw total); this fills in the model/window the meter measures
// against, which is provider state the compactor has no business knowing.
//
// rawTokens <= 0 means "no new raw measurement" (the elision-only pass never
// loads turns), so any previously cached raw value is preserved rather than
// overwritten with a bogus zero.
func (x *svc) recordCompactionContextUsage(ctx context.Context, convID string, sentTokens, rawTokens int) {
	store := x.Store()
	if store == nil || convID == "" || sentTokens <= 0 {
		return
	}
	model := x.primaryModel()
	window := contextmeter.MeterWindow(x.cfgSvc.Get(), model)

	if rawTokens <= 0 {
		if prev, ok, err := store.GetContextUsage(ctx, convID); err == nil && ok {
			rawTokens = prev.RawTokens
		}
	}

	// A compaction pass measures conversation content, not a full provider
	// request: it has no system prompt or tool schemas to account for. Record
	// what it actually measured and leave the request-overhead fields to the
	// turn writer, which sees real provider requests.
	usage := conversation.ContextUsage{
		ConversationID:     convID,
		TokensUsed:         sentTokens,
		RawTokens:          rawTokens,
		MessageTokens:      sentTokens,
		ContextWindow:      window.Tokens,
		ContextWindowKnown: window.Known,
		Model:              model,
		Source:             contextUsageSourceCompaction,
		ComputedAt:         time.Now(),
	}
	if err := store.SaveContextUsage(ctx, usage); err != nil {
		fmt.Fprintf(os.Stderr, "[compaction] cache context usage failed %s: %v\n", convID, err)
	}
}

// SetContextLoader attaches the project-context loader.
func (x *svc) SetContextLoader(l *projectctx.Loader) { x.contextLoader = l }

// RecordTurnContextUsage caches a turn's exact request accounting. The meter
// prefers this over a compaction-derived snapshot because it includes real
// system-prompt and tool-schema overhead.
//
// The raw (uncompacted) size is not recomputed here — deriving it would mean
// re-reading every turn on the turn path, which is exactly the cost this cache
// exists to avoid. Any previously cached raw value is carried forward instead.
func (x *svc) RecordTurnContextUsage(ctx context.Context, convID string, u TurnContextUsage) {
	store := x.Store()
	if store == nil || convID == "" || u.EstimatedRequestTokens <= 0 {
		return
	}

	raw := 0
	if prev, ok, err := store.GetContextUsage(ctx, convID); err == nil && ok {
		raw = prev.RawTokens
	}

	model := x.primaryModel()
	window := u.ContextWindow
	known := u.ContextWindowKnown
	if window <= 0 {
		w := contextmeter.ModelWindowFor(model)
		window, known = w.Tokens, w.Known
	}

	usage := conversation.ContextUsage{
		ConversationID:     convID,
		TokensUsed:         u.MessageTokens,
		RawTokens:          raw,
		MessageTokens:      u.MessageTokens,
		SystemTokens:       u.SystemTokens,
		ToolSchemaTokens:   u.ToolSchemaTokens,
		OutputReserve:      u.OutputReserveTokens,
		EstimatedRequest:   u.EstimatedRequestTokens,
		ContextWindow:      window,
		ContextWindowKnown: known,
		Model:              model,
		Source:             contextUsageSourceTurn,
		ComputedAt:         time.Now(),
	}
	if err := store.SaveContextUsage(ctx, usage); err != nil {
		fmt.Fprintf(os.Stderr, "[context-meter] cache turn usage failed %s: %v\n", convID, err)
	}
}

// --- Accessors for owned fields used by the front door ---

// ContextLoader exposes the loader so InstallCapabilities can pass it to
// capabilities.Services.ProjectCtx.
func (x *svc) ContextLoader() *projectctx.Loader { return x.contextLoader }

// CompactionGen exposes the generator so RegenerateContext can delegate to it.
func (x *svc) CompactionGen() *compactiongen.Generator { return x.compactionGen }

// --- Config hot-reload helpers called by UpdateConfig ---

// UpdateRetentionConfig hot-reloads the sweeper configuration.
func (x *svc) UpdateRetentionConfig(cfg retention.Config) {
	if x.retentionSweeper != nil {
		x.retentionSweeper.SetConfig(cfg)
	}
}

// SetCompactionEnabled enables/disables the background compaction generator.
func (x *svc) SetCompactionEnabled(enabled bool) {
	if x.compactionGen != nil {
		x.compactionGen.SetEnabled(enabled)
	}
}

// SetToolElisionOnly flips tool-elision-only mode on the generator.
func (x *svc) SetToolElisionOnly(v bool) {
	if x.compactionGen != nil {
		x.compactionGen.SetToolElisionOnly(v)
	}
}

// --- Core interface methods ---

// Store returns the live conversation store, or nil.
func (x *svc) Store() conversation.Store {
	if x.convAgent == nil {
		return nil
	}
	return x.convAgent.PersistentStore()
}

// LoadProjectContext returns the project's .cercano/context.md content, or "".
func (x *svc) LoadProjectContext(workDir string) string {
	if x.contextLoader == nil || workDir == "" {
		return ""
	}
	c, _ := x.contextLoader.Load(workDir)
	return c
}

// PersistTurn writes one conversation turn (any role) to the persistent store,
// with BlocksJSON and concatenated text Content. Best-effort: store errors are
// logged but never surfaced, so a write failure can't abort the turn. Called
// incrementally as the tool loop produces each message (crash resilience).
func (x *svc) PersistTurn(ctx context.Context, convID string, m llm.Message) {
	if x.convAgent == nil || convID == "" {
		return
	}
	store := x.convAgent.PersistentStore()
	if store == nil {
		return
	}
	blocksJSON, err := json.Marshal(m.Blocks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] marshal blocks failed: %v\n", err)
		return
	}
	var text string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	role := string(m.Role)
	if err := store.Append(ctx, conversation.Turn{
		ConversationID: convID,
		Role:           role,
		Content:        text,
		BlocksJSON:     string(blocksJSON),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] Append(%s, %s) failed: %v\n", role, convID, err)
	}
}

// AssembleHistory builds the conversation history to send: the compacted view
// (consolidated summary + live tail) when compaction state exists, else the full
// history. If the assembled history exceeds the hard-override fraction of the
// model's max context, it schedules a background compaction pass and degrades
// the view with LLM-free elision and front-drop rather than blocking.
//
// Unlike the legacy assembleHistory helper, this method gets the store from the
// service itself (no store parameter).
func (x *svc) AssembleHistory(ctx context.Context, convID string) []llm.Message {
	return x.AssembleHistoryForTarget(ctx, convID, requestassembly.Target{Model: x.activeCloudModel()}).Messages
}

func (x *svc) AssembleHistoryForTarget(ctx context.Context, convID string, target requestassembly.Target) requestassembly.Result {
	return x.assembleHistoryForTarget(ctx, convID, target, true)
}

func (x *svc) assembleHistoryForTarget(ctx context.Context, convID string, target requestassembly.Target, schedule bool) requestassembly.Result {
	store := x.Store()
	if store == nil {
		return requestassembly.Result{}
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", convID, err)
		return requestassembly.Result{}
	}
	state, _ := store.GetCompaction(ctx, convID)
	assembled := requestassembly.Assemble(turns, state, x.cfgSvc.Get().Compaction, x.elisionFloor(convID), target, contextmeter.Default())
	acct := assembled.Accounting
	if schedule && acct.Scheduled {
		// Never compact inline — kick the background generator (debounced,
		// deduped, timeout-bounded) and bring THIS turn under the limit with
		// LLM-free steps only.
		x.convAgent.ScheduleCompaction(convID)
		if acct.Truncated {
			fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — truncated %d oldest messages (background pass scheduled)\n",
				convID, acct.InitialTokens, acct.HardLimit, acct.DroppedMessages)
		} else {
			fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — elision brought it under (background pass scheduled)\n",
				convID, acct.InitialTokens, acct.HardLimit)
		}
	}
	return assembled
}

// --- RPC-body implementations ---

// ListConversations returns persisted conversation summaries for the /history picker.
func (x *svc) ListConversations(ctx context.Context, req *proto.ListConversationsRequest) (*proto.ListConversationsResponse, error) {
	if x.convAgent == nil {
		return &proto.ListConversationsResponse{}, nil
	}
	infos, err := x.convAgent.ListConversations(ctx, req.GetProjectDir(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &proto.ListConversationsResponse{Conversations: make([]*proto.Conversation, 0, len(infos))}
	for _, i := range infos {
		out.Conversations = append(out.Conversations, &proto.Conversation{
			Id:             i.ID,
			Title:          i.Title,
			ProjectDir:     i.ProjectDir,
			Model:          i.Model,
			StartedAt:      i.StartedAt.Unix(),
			LastTurnAt:     i.LastTurnAt.Unix(),
			TurnCount:      int32(i.TurnCount),
			Recap:          i.Recap,
			RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
		})
	}
	return out, nil
}

// GetConversation returns a single conversation's metadata including its living
// recap. Lightweight: no turn rehydration.
func (x *svc) GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error) {
	if x.convAgent == nil {
		return &proto.Conversation{}, nil
	}
	i, err := x.convAgent.GetConversation(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	return &proto.Conversation{
		Id:             i.ID,
		Title:          i.Title,
		ProjectDir:     i.ProjectDir,
		Model:          i.Model,
		StartedAt:      i.StartedAt.Unix(),
		LastTurnAt:     i.LastTurnAt.Unix(),
		TurnCount:      int32(i.TurnCount),
		Recap:          i.Recap,
		RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
	}, nil
}

const (
	resumeConversationChunkTargetBytes  = 8 << 20
	resumeConversationChunkHardMaxBytes = 48 << 20
	resumeViewportDefaultTailTurns      = 80
	resumeViewportDefaultOlderChunk     = 200
)

// ResumeConversation loads persisted turns for a conversation, rehydrates the
// in-memory session store, returns the turns so the CLI can render them in scrollback.
func (x *svc) ResumeConversation(ctx context.Context, req *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error) {
	if x.convAgent == nil {
		return &proto.ResumeConversationResponse{}, nil
	}
	turns, err := x.convAgent.ResumeConversation(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	out := &proto.ResumeConversationResponse{Turns: make([]*proto.PersistedTurn, 0, len(turns))}
	for _, t := range turns {
		out.Turns = append(out.Turns, persistedTurnProto(t))
	}
	return out, nil
}

// StreamResumeConversation loads the same logical transcript as
// ResumeConversation, but sends it in byte-budgeted batches so large histories
// do not become one oversized gRPC response message. The agent-side resume call
// still runs once up front so session rehydration and context-meter behavior
// match the unary compatibility RPC.
func (x *svc) StreamResumeConversation(req *proto.ResumeConversationRequest, stream proto.Agent_StreamResumeConversationServer) error {
	if x.convAgent == nil {
		return nil
	}
	turns, err := x.convAgent.ResumeConversation(stream.Context(), req.GetConversationId())
	if err != nil {
		return err
	}
	return sendResumeTurnChunks(req.GetConversationId(), turns, resumeConversationChunkTargetBytes, stream.Send)
}

func (x *svc) StreamResumeConversationViewportFirst(req *proto.ResumeConversationViewportFirstRequest, stream proto.Agent_StreamResumeConversationViewportFirstServer) error {
	if x.convAgent == nil {
		return nil
	}
	store := x.convAgent.PersistentStore()
	if store == nil {
		return nil
	}
	ctx := stream.Context()
	convID := req.GetConversationId()
	tailTurns := int(req.GetTailTurns())
	if tailTurns <= 0 {
		tailTurns = resumeViewportDefaultTailTurns
	}
	olderChunkTurns := int(req.GetOlderChunkTurns())
	if olderChunkTurns <= 0 {
		olderChunkTurns = resumeViewportDefaultOlderChunk
	}

	tail, tailStart, total, err := store.GetTailTurns(ctx, convID, tailTurns)
	if err != nil {
		return err
	}
	if err := stream.Send(resumeViewportEvent(proto.ResumeConversationViewportFirstEvent_TAIL, convID, tail, tailStart, total)); err != nil {
		return err
	}

	hydrationDone := make(chan error, 1)
	go func() {
		_, err := x.convAgent.ResumeConversation(ctx, convID)
		hydrationDone <- err
	}()

	hydrationSent := false
	flushHydration := func(block bool) error {
		if hydrationSent {
			return nil
		}
		if block {
			select {
			case err := <-hydrationDone:
				if err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			select {
			case err := <-hydrationDone:
				if err != nil {
					return err
				}
			default:
				return nil
			}
		}
		hydrationSent = true
		return stream.Send(resumeViewportEvent(proto.ResumeConversationViewportFirstEvent_HYDRATION_COMPLETE, convID, nil, tailStart, total))
	}

	for before := tailStart; before > 0; {
		if err := flushHydration(false); err != nil {
			return err
		}
		start := before - olderChunkTurns
		if start < 0 {
			start = 0
		}
		page, err := store.GetTurnPage(ctx, convID, start, before-start)
		if err != nil {
			return err
		}
		if err := stream.Send(resumeViewportEvent(proto.ResumeConversationViewportFirstEvent_OLDER, convID, page, start, total)); err != nil {
			return err
		}
		before = start
	}
	if err := stream.Send(resumeViewportEvent(proto.ResumeConversationViewportFirstEvent_BACKFILL_COMPLETE, convID, nil, 0, total)); err != nil {
		return err
	}
	return flushHydration(true)
}

func resumeViewportEvent(kind proto.ResumeConversationViewportFirstEvent_Kind, conversationID string, turns []conversation.Turn, startIndex, total int) *proto.ResumeConversationViewportFirstEvent {
	out := &proto.ResumeConversationViewportFirstEvent{
		Kind:           kind,
		ConversationId: conversationID,
		StartIndex:     int32(startIndex),
		TotalTurns:     int32(total),
	}
	if len(turns) > 0 {
		out.Turns = make([]*proto.PersistedTurn, 0, len(turns))
		for _, t := range turns {
			out.Turns = append(out.Turns, persistedTurnProto(t))
		}
	}
	return out
}

func sendResumeTurnChunks(conversationID string, turns []conversation.Turn, targetBytes int, send func(*proto.ResumeConversationChunk) error) error {
	return sendResumeTurnChunksWithLimits(conversationID, turns, targetBytes, resumeConversationChunkHardMaxBytes, send)
}

func sendResumeTurnChunksWithLimits(conversationID string, turns []conversation.Turn, targetBytes, hardMaxBytes int, send func(*proto.ResumeConversationChunk) error) error {
	if targetBytes <= 0 {
		targetBytes = resumeConversationChunkTargetBytes
	}
	if hardMaxBytes <= 0 {
		hardMaxBytes = resumeConversationChunkHardMaxBytes
	}
	chunk := &proto.ResumeConversationChunk{Turns: make([]*proto.PersistedTurn, 0)}
	chunkBytes := 0
	flush := func() error {
		if len(chunk.GetTurns()) == 0 {
			return nil
		}
		if err := send(chunk); err != nil {
			return err
		}
		chunk = &proto.ResumeConversationChunk{Turns: make([]*proto.PersistedTurn, 0)}
		chunkBytes = 0
		return nil
	}
	for _, t := range turns {
		pt := persistedTurnProto(t)
		turnBytes := estimatePersistedTurnBytes(pt)
		if turnBytes > hardMaxBytes {
			return fmt.Errorf("conversation %s contains an individual turn too large to stream safely: turn %s is about %d bytes", conversationID, pt.GetId(), turnBytes)
		}
		if chunkBytes > 0 && chunkBytes+turnBytes > targetBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		chunk.Turns = append(chunk.Turns, pt)
		chunkBytes += turnBytes
	}
	return flush()
}

func persistedTurnProto(t conversation.Turn) *proto.PersistedTurn {
	return &proto.PersistedTurn{
		Id:             t.ID,
		ConversationId: t.ConversationID,
		Role:           t.Role,
		Content:        t.Content,
		TokensIn:       int32(t.TokensIn),
		TokensOut:      int32(t.TokensOut),
		LatencyMs:      int32(t.LatencyMs),
		CreatedAt:      t.CreatedAt.Unix(),
		ContentJson:    t.BlocksJSON,
	}
}

func estimatePersistedTurnBytes(t *proto.PersistedTurn) int {
	if t == nil {
		return 0
	}
	// String payload dominates resume size. The constant covers protobuf tags,
	// lengths, numeric fields, and slice overhead with enough slack for batching.
	return 128 + len(t.GetId()) + len(t.GetConversationId()) + len(t.GetRole()) + len(t.GetContent()) + len(t.GetContentJson())
}

// DeleteConversation hard-deletes a conversation.
func (x *svc) DeleteConversation(ctx context.Context, req *proto.DeleteConversationRequest) (*proto.DeleteConversationResponse, error) {
	if x.convAgent == nil {
		return &proto.DeleteConversationResponse{Ok: true}, nil
	}
	if err := x.convAgent.DeleteConversation(ctx, req.GetConversationId()); err != nil {
		return nil, err
	}
	return &proto.DeleteConversationResponse{Ok: true}, nil
}

// RenameConversation updates a conversation's title.
func (x *svc) RenameConversation(ctx context.Context, req *proto.RenameConversationRequest) (*proto.RenameConversationResponse, error) {
	if x.convAgent == nil {
		return &proto.RenameConversationResponse{Ok: true}, nil
	}
	if err := x.convAgent.RenameConversation(ctx, req.GetConversationId(), req.GetTitle()); err != nil {
		return nil, err
	}
	return &proto.RenameConversationResponse{Ok: true}, nil
}

// GetConversationTurns returns display-ready summaries of a conversation's turns
// for the /c context viewer. Reads the store only (side-effect-free).
// ListSubAgents returns the persisted sub-agent (dispatch) conversations
// spawned under a parent conversation, in spawn order, each carrying the
// granted tool set so a resumed CLI can reopen the sub-agent tab exactly as it
// showed live. The transcript itself is fetched per child via
// ResumeConversation — this call is just the identity + tool-set index.
func (x *svc) ListSubAgents(ctx context.Context, req *proto.ListSubAgentsRequest) (*proto.ListSubAgentsResponse, error) {
	out := &proto.ListSubAgentsResponse{}
	if x.convAgent == nil {
		return out, nil
	}
	store := x.convAgent.PersistentStore()
	parentID := req.GetParentId()
	if store == nil || parentID == "" {
		return out, nil
	}
	children, err := store.ListChildren(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	for _, c := range children {
		out.Subagents = append(out.Subagents, &proto.SubAgentConversation{
			Id:           c.ID,
			ParentId:     c.ParentID,
			Title:        c.Title,
			GrantedTools: c.GrantedTools,
		})
	}
	return out, nil
}

func (x *svc) DismissSubAgent(ctx context.Context, req *proto.DismissSubAgentRequest) (*proto.DismissSubAgentResponse, error) {
	out := &proto.DismissSubAgentResponse{}
	if x.convAgent == nil {
		return out, nil
	}
	store := x.convAgent.PersistentStore()
	id := req.GetConversationId()
	if store == nil || id == "" {
		return out, nil
	}
	if err := store.MarkSubagentDismissed(ctx, id); err != nil {
		return nil, fmt.Errorf("mark subagent dismissed: %w", err)
	}
	return out, nil
}

func (x *svc) GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error) {
	out := &proto.GetConversationTurnsResponse{}
	if x.convAgent == nil {
		return out, nil
	}
	store := x.convAgent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get turns: %w", err)
	}
	tok := contextmeter.Default()
	for _, t := range turns {
		out.Turns = append(out.Turns, ContextTurnView(t, tok))
	}
	return out, nil
}

// GetContextUsage reports cumulative token usage vs. the active model's
// context-window size for a conversation.
func (x *svc) GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error) {
	convID := req.GetConversationId()
	// The denominator is the locus route's PRIMARY model window — stable
	// across agent restarts and unaffected by per-turn fallbacks or background
	// summarizer traffic. The per-conversation meter counter's max follows
	// whatever model served the last turn and defaults to the open model on a
	// fresh registry, which made the bar jump (e.g. 200k → 128k) after every
	// restart until the first cloud-served turn re-baselined it.
	// On local locus routes the denominator is the size we actually launch the
	// runtime with, not the model family's published window — see MeterWindow.
	modelWindow := contextmeter.MeterWindow(x.cfgSvc.Get(), x.primaryModel())
	max := modelWindow.Tokens

	isCompacting := false
	if x.convAgent != nil {
		isCompacting = x.convAgent.IsCompacting(convID)
	}

	resp := &proto.GetContextUsageResponse{
		ModelMax:           int32(max),
		Compacting:         isCompacting,
		ContextWindowKnown: modelWindow.Known,
		UsageSource:        "none",
	}

	store := x.Store()
	if store == nil || convID == "" {
		return resp, nil
	}

	// Serve the durable snapshot. Recomputing provider-facing accounting here
	// means assembling the full history, which on a large conversation takes
	// far longer than the client's poll deadline; the client then treated the
	// failed poll as zero usage. A cached real number, marked with its
	// provenance, beats an unbounded recomputation that reports 0.
	snap, ok, err := store.GetContextUsage(ctx, convID)
	if err != nil || !ok {
		// Cold start: no pass has run and no turn has been served for this
		// conversation in any process. Rather than show a blank meter, measure
		// raw storage pressure with a SQL aggregate (bodies stay in the DB) and
		// warm the cache with it. It is labeled as a raw estimate, not request
		// accounting, and the first turn or pass replaces it with real numbers.
		snap, ok = x.warmRawContextUsage(ctx, convID, modelWindow)
		if !ok {
			// Genuinely nothing to measure (no turns): honestly unknown.
			// usage_source stays "none" so the client says "not computed yet"
			// rather than showing a confident 0.
			return resp, nil
		}
	}

	sent := snap.EstimatedRequest
	if sent <= 0 {
		sent = snap.TokensUsed
	}
	if snap.ContextWindow > 0 {
		max = snap.ContextWindow
		resp.ModelMax = int32(max)
		resp.ContextWindowKnown = snap.ContextWindowKnown
	}
	if max > 0 {
		pct := float64(sent) / float64(max)
		if pct > 1 {
			pct = 1
		}
		resp.Percent = pct
	}

	resp.TokensUsed = int32(sent)
	resp.RawTokens = int32(snap.RawTokens)
	resp.MessageTokens = int32(snap.MessageTokens)
	resp.SystemTokens = int32(snap.SystemTokens)
	resp.ToolSchemaTokens = int32(snap.ToolSchemaTokens)
	resp.OutputReserveTokens = int32(snap.OutputReserve)
	resp.EstimatedRequestTokens = int32(sent)
	resp.UsageSource = snapshotUsageSource(snap.Source)
	resp.UsageComputedAt = snap.ComputedAt.Unix()
	resp.UsageStale = x.snapshotIsStale(ctx, convID, snap)
	return resp, nil
}

// snapshotUsageSource maps stored provenance to the wire vocabulary. A raw
// estimate is reported distinctly so the client never presents storage pressure
// as if it were measured provider request accounting.
func snapshotUsageSource(stored string) string {
	if stored == contextUsageSourceRawEstimate {
		return "raw_estimate"
	}
	return "snapshot"
}

// warmRawContextUsage handles the cold-start miss: estimate the conversation's
// raw size via a bounded SQL aggregate, persist it so later polls are a single
// row read, and return it. ok is false when the conversation has no turns.
//
// This is a lower bound deliberately: it is the uncompacted storage size, with
// no compaction, system prompt, or tool schemas accounted for. It exists so a
// large loaded conversation shows a real number instead of nothing, and it is
// overwritten by the first turn or compaction pass.
func (x *svc) warmRawContextUsage(ctx context.Context, convID string, window contextmeter.ModelWindow) (conversation.ContextUsage, bool) {
	store := x.Store()
	if store == nil {
		return conversation.ContextUsage{}, false
	}
	raw, err := store.EstimateRawTokens(ctx, convID)
	if err != nil || raw <= 0 {
		return conversation.ContextUsage{}, false
	}

	// Raw storage pressure is NOT the send view. For a compacted conversation
	// the frozen backlog has been replaced by summaries, and raw counts things
	// the provider never sees in full — inline image payloads above all. Use
	// the compactor's own persisted accounting for the sent figure, and keep
	// the raw aggregate strictly as the raw figure.
	sent := raw
	if state, cerr := store.GetCompaction(ctx, convID); cerr == nil && state.CompactedTokens > 0 {
		sent = state.CompactedTokens
	}

	snap := conversation.ContextUsage{
		ConversationID:     convID,
		TokensUsed:         sent,
		RawTokens:          raw,
		MessageTokens:      sent,
		ContextWindow:      window.Tokens,
		ContextWindowKnown: window.Known,
		Model:              x.primaryModel(),
		Source:             contextUsageSourceRawEstimate,
		ComputedAt:         time.Now(),
	}
	if err := store.SaveContextUsage(ctx, snap); err != nil {
		fmt.Fprintf(os.Stderr, "[context-meter] warm raw usage failed %s: %v\n", convID, err)
	}
	return snap, true
}

// snapshotIsStale reports whether turns were appended after the snapshot was
// computed, which makes its numbers a lower bound rather than a current
// reading. Deliberately a cheap metadata lookup: the whole point of the cache
// is that answering the meter must not touch turn bodies.
func (x *svc) snapshotIsStale(ctx context.Context, convID string, snap conversation.ContextUsage) bool {
	if snap.ComputedAt.IsZero() {
		return true
	}
	store := x.Store()
	if store == nil {
		return false
	}
	info, err := store.Get(ctx, convID)
	if err != nil {
		return false
	}
	return info.LastTurnAt.After(snap.ComputedAt)
}

// sentViewTokens computes the sent-view token count the meter and
// /elide-context report: the compacted view (or full history) with the
// conversation's elision floor and the configured mechanical elisions applied,
// mirroring AssembleHistory. Falls back to the cheap raw estimate on the fast
// path (no compaction, no elision, no floor) because the footer polls this
// frequently.
func (x *svc) sentViewTokens(convID string, turns []conversation.Turn, state conversation.Compaction, raw int) int {
	compSnap := x.cfgSvc.Get().Compaction
	floor := x.elisionFloor(convID)
	if state.ConsolidatedJSON == "" && !compSnap.Enabled && !compSnap.ElideToolResults && !compSnap.LossyToolElision && floor <= 0 {
		return raw
	}
	assembled := requestassembly.Assemble(turns, state, compSnap, floor, requestassembly.Target{
		Model: x.primaryModel(),
	}, contextmeter.Default())
	return assembled.Accounting.FinalTokens
}

// ElideContext implements the /elide-context RPC: record "now" as the
// conversation's elision floor so every tool-result body up to this point is
// stubbed at context-assembly time. In-memory and send-view only — the stored
// raw turns are untouched and the floor resets on agent restart. Tool results
// produced after this call stay intact until the next call.
func (x *svc) ElideContext(ctx context.Context, req *proto.ElideContextRequest) (*proto.ElideContextResponse, error) {
	convID := req.GetConversationId()
	if convID == "" {
		return nil, fmt.Errorf("conversation_id is required")
	}
	store := x.Store()
	if store == nil {
		return nil, fmt.Errorf("no conversation store — the agent is running without persistence")
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get turns: %w", err)
	}
	if len(turns) == 0 {
		return &proto.ElideContextResponse{}, nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	raw := requestassembly.EstimateRawTokens(turns)

	pre := x.sentViewTokens(convID, turns, state, raw)
	// GetTurns returns turns in created_at order; the newest turn's timestamp
	// is the floor, so everything currently in the context is covered.
	floor := turns[len(turns)-1].CreatedAt.Unix()
	_, stubbed := compactor.StubToolResultsThrough(turns, floor)
	x.setElisionFloor(convID, floor)
	post := x.sentViewTokens(convID, turns, state, raw)

	return &proto.ElideContextResponse{
		PreTokens:  int32(pre),
		PostTokens: int32(post),
		Stubbed:    int32(stubbed),
	}, nil
}

// GetCompactionState returns the compaction summary + frozen/live split for the /c viewer.
func (x *svc) GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error) {
	convID := req.GetConversationId()
	isCompacting := false
	if x.convAgent != nil {
		isCompacting = x.convAgent.IsCompacting(convID)
	}
	out := &proto.GetCompactionStateResponse{Compacting: isCompacting}
	store := x.Store()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return out, nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)
	out.SentTokens = int32(compaction.ProviderTotalTokens(contextmeter.Default(), view))
	out.RawTokens = int32(requestassembly.EstimateRawTokens(turns))
	out.FrozenThrough = state.FrozenThrough
	for _, t := range turns {
		if t.CreatedAt.Unix() <= state.FrozenThrough {
			out.FrozenTurns++
		} else {
			out.LiveTurns++
		}
	}
	if state.SegmentSummariesJSON != "" {
		var segs []compaction.StructuredSummary
		if json.Unmarshal([]byte(state.SegmentSummariesJSON), &segs) == nil {
			out.CompactedSegments = int32(len(segs))
		}
	}
	if state.ConsolidatedJSON != "" {
		var cs compaction.StructuredSummary
		if json.Unmarshal([]byte(state.ConsolidatedJSON), &cs) == nil {
			out.ConsolidatedSummary = cs.RenderBlock().Text
		}
	}
	return out, nil
}

// ExportContext returns the full uncapped raw history as a JSON []llm.Message.
func (x *svc) ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error) {
	store := x.Store()
	if store == nil || req.GetConversationId() == "" {
		return &proto.ExportContextResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(agent.BuildLLMHistory(turns))
	if err != nil {
		return nil, err
	}
	return &proto.ExportContextResponse{Json: string(b)}, nil
}

// RegenerateContext rebuilds a conversation's derived compaction state from its
// raw turns (or, with clear_only, drops it so the raw history is sent as-is),
// streaming progress to the client.
func (x *svc) RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error {
	convID := req.GetConversationId()
	if convID == "" {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "conversation_id is required"})
	}
	if x.compactionGen == nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "compaction is not available (agent is running without a conversation store)"})
	}

	if req.GetClearOnly() && req.GetIncremental() {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "clear_only and incremental are mutually exclusive"})
	}

	progress := func(line string) {
		_ = stream.Send(&proto.RegenerateContextProgress{Line: line})
	}
	if req.GetIncremental() {
		// /compact manually starts the same background compaction path used by
		// request/hard-limit compaction, but it does not wait for the pass. The
		// pass claims the conversation immediately (so the status bar can show
		// real Compacting=true), persists bounded progress, and reschedules
		// itself while backlog remains.
		go func() { _ = x.compactionGen.CompactNow(context.Background(), convID) }()
		return stream.Send(&proto.RegenerateContextProgress{
			Done: true,
			Ok:   true,
			Line: "context compaction started in background",
		})
	}
	var pre, post int
	var err error
	if req.GetClearOnly() {
		pre, post, err = x.compactionGen.Clear(stream.Context(), convID, progress)
	} else {
		pre, post, err = x.compactionGen.Regenerate(stream.Context(), convID, false, progress)
	}
	if err != nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: err.Error(), PreTokens: int32(pre)})
	}
	verb := "rebuilt"
	if req.GetClearOnly() {
		verb = "cleared — full raw history restored"
	}
	return stream.Send(&proto.RegenerateContextProgress{
		Done:       true,
		Ok:         true,
		Line:       fmt.Sprintf("context %s: ~%d → ~%d tokens", verb, pre, post),
		PreTokens:  int32(pre),
		PostTokens: int32(post),
	})
}

// ProposeContextEdit runs the picker model over the conversation's turn
// summaries and returns a validated deletion proposal. Read-only.
func (x *svc) ProposeContextEdit(ctx context.Context, req *proto.ProposeContextEditRequest) (*proto.ProposeContextEditResponse, error) {
	if x.convAgent == nil {
		return &proto.ProposeContextEditResponse{}, nil
	}
	store := x.convAgent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.ProposeContextEditResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, err
	}
	tok := contextmeter.Default()
	summaries := make([]contextedit.TurnSummary, 0, len(turns))
	for _, t := range turns {
		ct := ContextTurnView(t, tok)
		summaries = append(summaries, contextedit.TurnSummary{
			ID: ct.GetId(), Role: ct.GetRole(), Kind: ct.GetKind(), Preview: ct.GetPreview(),
		})
	}

	var local, cloud contextedit.CompleteFunc
	if op := x.openTurnRunner(); op != nil {
		local = func(c context.Context, prompt string) (string, error) {
			resp, err := op.Process(c, &agent.Request{Input: prompt})
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
	}
	if cloudProv := x.cloudProvider(); cloudProv != nil {
		cm := x.cloudModel()
		cloud = func(c context.Context, prompt string) (string, error) {
			resp, err := cloudProv.Chat(c, llm.ChatRequest{
				Model:     cm,
				Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: prompt}}}},
				MaxTokens: 1024,
			})
			if err != nil {
				return "", err
			}
			var out string
			for _, b := range resp.Blocks {
				if b.Type == llm.BlockText {
					out += b.Text
				}
			}
			return out, nil
		}
	}
	if local == nil && cloud == nil {
		return nil, fmt.Errorf("no model available for context editing")
	}

	p, err := contextedit.Propose(ctx, req.GetInstruction(), summaries, local, cloud)
	if err != nil {
		return nil, err
	}
	return &proto.ProposeContextEditResponse{DeleteIds: p.DeleteIDs, Rationale: p.Rationale}, nil
}

// DeleteConversationTurns hard-deletes the named turns. The next tool-loop turn
// rebuilds history from the store, so the context shrinks automatically.
func (x *svc) DeleteConversationTurns(ctx context.Context, req *proto.DeleteConversationTurnsRequest) (*proto.DeleteConversationTurnsResponse, error) {
	if x.convAgent == nil {
		return &proto.DeleteConversationTurnsResponse{}, nil
	}
	store := x.convAgent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.DeleteConversationTurnsResponse{}, nil
	}
	if err := store.DeleteTurns(ctx, convID, req.GetTurnId()); err != nil {
		return nil, err
	}
	return &proto.DeleteConversationTurnsResponse{Deleted: int32(len(req.GetTurnId()))}, nil
}

// SuggestNextPrompt asks the local co-processor for one short follow-up prompt
// the user might send next, based on the conversation's living recap + the tail
// of recent turns. Degrades to an empty response on any failure.
func (x *svc) SuggestNextPrompt(ctx context.Context, req *proto.SuggestNextPromptRequest) (*proto.SuggestNextPromptResponse, error) {
	empty := &proto.SuggestNextPromptResponse{}
	convID := req.GetConversationId()
	e := x.engine()
	if convID == "" || e == nil {
		return empty, nil
	}
	store := x.Store()
	if store == nil {
		return empty, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil || len(turns) == 0 {
		return empty, nil
	}
	// Pull the recap for background; fall back to just the turn tail when
	// none has been consolidated yet.
	recap := ""
	if info, err := store.Get(ctx, convID); err == nil {
		recap = info.Recap
	}
	// Last 6 turns give the coproc immediate context without ballooning the
	// prompt on long conversations. Recap covers the deeper history.
	tailN := 6
	if len(turns) < tailN {
		tailN = len(turns)
	}
	var tail strings.Builder
	for _, t := range turns[len(turns)-tailN:] {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			continue
		}
		if len(content) > 400 {
			content = content[:400] + "…"
		}
		fmt.Fprintf(&tail, "[%s]\n%s\n\n", t.Role, content)
	}
	prompt := buildSuggestNextPromptPrompt(recap, tail.String())

	res, err := e.Dispatch(ctx, dispatch.Spec{
		Mode:        dispatch.OneShot,
		Tier:        config.TierFastLightText,
		Role:        dispatch.RoleCoproc,
		Prompt:      prompt,
		Source:      "suggest_next_prompt",
		RecordUsage: true,
	})
	if err != nil {
		return empty, nil
	}
	return &proto.SuggestNextPromptResponse{Suggestion: SanitizeSuggestion(res.Text)}, nil
}

func buildSuggestNextPromptPrompt(recap, tail string) string {
	var promptB strings.Builder
	promptB.WriteString("You generate ghost-text tab-complete suggestions for the user's next prompt in a coding-agent conversation. ")
	promptB.WriteString("Do not imitate casual user chatter. Infer the current task state and suggest the most useful next user action, or output nothing if no high-confidence useful action exists.\n\n")
	promptB.WriteString("Output contract:\n")
	promptB.WriteString("- Output exactly ONE next prompt the user could send, under 80 characters.\n")
	promptB.WriteString("- Output an empty string if the next action is unclear or low-value.\n")
	promptB.WriteString("- Output ONLY the prompt text: no quotes, bullets, labels, commentary, or punctuation prefixes.\n\n")
	promptB.WriteString("Good suggestion classes:\n")
	promptB.WriteString("- Approve a waiting decision: e.g. Proceed with the recommended option.\n")
	promptB.WriteString("- Choose among options: e.g. Use the structured suggestion engine.\n")
	promptB.WriteString("- Ask for verification when code changed: e.g. Run the focused tests.\n")
	promptB.WriteString("- Continue approved implementation: e.g. Implement the prompt builder tests.\n")
	promptB.WriteString("- Ask for a concise summary after completed work: e.g. Summarize what changed.\n")
	promptB.WriteString("- Ask to checkpoint/land only when work is complete and verified.\n\n")
	promptB.WriteString("Avoid bad suggestions:\n")
	promptB.WriteString("- Do not suggest work already completed in the recent turns.\n")
	promptB.WriteString("- Do not suggest generic prompts like 'what next?', 'continue', or 'tell me more'.\n")
	promptB.WriteString("- Do not suggest running tests unless verification is actually pending.\n")
	promptB.WriteString("- Do not suggest checkpoint/land if edits are still in progress or unverified.\n\n")
	if strings.TrimSpace(recap) != "" {
		promptB.WriteString("Recap of the conversation so far:\n")
		promptB.WriteString(recap)
		promptB.WriteString("\n\n")
	}
	promptB.WriteString("Most recent turns:\n")
	promptB.WriteString(tail)
	promptB.WriteString("\nNext useful user prompt:")
	return promptB.String()
}

// SanitizeSuggestion normalizes a coproc's suggestion text: single line, no
// surrounding quotes, capped at 80 characters. Belt-and-suspenders against
// models that ignore the "no formatting" rule. Exported for tests in the
// server package that lock in the normalization shape.
func SanitizeSuggestion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	// Strip matched surrounding quote characters (", ', `).
	for len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			s = strings.TrimSpace(s[1 : len(s)-1])
			continue
		}
		break
	}
	// Drop leading list/label punctuation the model might inject anyway.
	s = strings.TrimLeft(s, "-*•> ")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
