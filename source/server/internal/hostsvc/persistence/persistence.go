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

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactiongen"
	"cercano/source/server/internal/compactor"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/contextedit"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
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

	// Setters called by wiring code (cmd/cercano or watchdog_wire).
	SetRetentionSweeper(sw *retention.Sweeper)
	SetCompactionGenerator(g *compactiongen.Generator)
	SetContextLoader(l *projectctx.Loader)

	// RPC-body implementations (front door delegates to these).
	ListConversations(ctx context.Context, req *proto.ListConversationsRequest) (*proto.ListConversationsResponse, error)
	GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error)
	ResumeConversation(ctx context.Context, req *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error)
	DeleteConversation(ctx context.Context, req *proto.DeleteConversationRequest) (*proto.DeleteConversationResponse, error)
	RenameConversation(ctx context.Context, req *proto.RenameConversationRequest) (*proto.RenameConversationResponse, error)
	GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error)
	ListSubAgents(ctx context.Context, req *proto.ListSubAgentsRequest) (*proto.ListSubAgentsResponse, error)
	GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error)
	GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error)
	ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error)
	RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error
	ProposeContextEdit(ctx context.Context, req *proto.ProposeContextEditRequest) (*proto.ProposeContextEditResponse, error)
	DeleteConversationTurns(ctx context.Context, req *proto.DeleteConversationTurnsRequest) (*proto.DeleteConversationTurnsResponse, error)
	SuggestNextPrompt(ctx context.Context, req *proto.SuggestNextPromptRequest) (*proto.SuggestNextPromptResponse, error)
}

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

	// openLegacy returns the legacy open-model provider for ProposeContextEdit.
	openLegacy func() *legacymodels.OpenModelProvider

	// cloudProvider returns the cloud LLM provider for ProposeContextEdit.
	cloudProvider func() llm.Provider

	// cloudModel returns the active cloud model string for ProposeContextEdit.
	cloudModel func() string

	// Owned fields (moved off Server.struct).
	retentionSweeper *retention.Sweeper
	compactionGen    *compactiongen.Generator
	contextLoader    *projectctx.Loader
}

// New constructs a Service.
//
//   - convAgent: the *agent.Agent (or a test fake) for store access + conversation CRUD.
//   - cfgSvc: config service; AssembleHistory reads compaction settings from it.
//   - primaryModel: func returning the primary model name (for GetContextUsage denominator).
//   - activeCloudModel: func returning the active cloud model (for AssembleHistory hard-override).
//   - engine: func returning the dispatch engine (for SuggestNextPrompt); may return nil.
//   - openLegacy: func returning the legacy open provider (for ProposeContextEdit); may return nil.
//   - cloudProvider: func returning the cloud LLM provider (for ProposeContextEdit); may return nil.
//   - cloudModel: func returning the active cloud model string (for ProposeContextEdit); may return "".
func New(
	convAgent ConvAgent,
	cfgSvc cfgsvc.Service,
	primaryModel func() string,
	activeCloudModel func() string,
	engine func() *dispatch.Engine,
	openLegacy func() *legacymodels.OpenModelProvider,
	cloudProvider func() llm.Provider,
	cloudModel func() string,
) Service {
	return &svc{
		convAgent:        convAgent,
		cfgSvc:           cfgSvc,
		primaryModel:     primaryModel,
		activeCloudModel: activeCloudModel,
		engine:           engine,
		openLegacy:       openLegacy,
		cloudProvider:    cloudProvider,
		cloudModel:       cloudModel,
	}
}

// --- Setters for owned fields ---

// SetRetentionSweeper attaches the background retention sweeper.
func (x *svc) SetRetentionSweeper(sw *retention.Sweeper) { x.retentionSweeper = sw }

// SetCompactionGenerator attaches the background compaction scheduler.
func (x *svc) SetCompactionGenerator(g *compactiongen.Generator) { x.compactionGen = g }

// SetContextLoader attaches the project-context loader.
func (x *svc) SetContextLoader(l *projectctx.Loader) { x.contextLoader = l }

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
	store := x.Store()
	if store == nil {
		return nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", convID, err)
		return nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	view, _ := compactor.BuildSendView(turns, state)

	compactionCfg := x.cfgSvc.Get().Compaction
	cloudModel := x.activeCloudModel()
	pct := compactionCfg.HardOverridePct
	if compactionCfg.Enabled && pct > 0 {
		hardLimit := int(float64(contextmeter.ModelMax(cloudModel)) * pct)
		if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
			// Never compact inline — kick the background generator (debounced,
			// deduped, timeout-bounded) and bring THIS turn under the limit with
			// LLM-free steps only.
			x.convAgent.ScheduleCompaction(convID)
			pre := compaction.TotalTokens(contextmeter.Default(), view)
			view, _ = compaction.ElideSupersededToolResults(view)
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
			}
			if compaction.TotalTokens(contextmeter.Default(), view) > hardLimit {
				preserve := 0
				if state.ConsolidatedJSON != "" {
					preserve = 1 // keep the consolidated-summary preamble
				}
				var dropped int
				view, dropped = compaction.TruncateOldestToFit(view, contextmeter.Default(), hardLimit, preserve)
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — truncated %d oldest messages (background pass scheduled)\n",
					convID, pre, hardLimit, dropped)
			} else {
				fmt.Fprintf(os.Stderr, "[compaction] hard-override %s: %d tokens > limit %d — elision brought it under (background pass scheduled)\n",
					convID, pre, hardLimit)
			}
		}
	}
	// Mechanical superseded-tool-result dedup. LLM-free, lossless, and safe to
	// apply on top of either a summarized view or the raw history — running it
	// twice is idempotent.
	if compactionCfg.ElideToolResults {
		view, _ = compaction.ElideSupersededToolResults(view)
	}
	// Recency-window elision. Stubs older tool_result content down to a marker;
	// keeps the last N intact. Applied after byte-identical dedup because the
	// two are complementary — the identical-dedup catches literal duplicates
	// among the kept N; the recency policy handles the long tail.
	if compactionCfg.LossyToolElision {
		view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
	}
	// Final pairing repair: idempotent on a valid view, and insurance for the
	// one degrade edge (a lone surviving tool_result after truncation) that
	// would otherwise reach the provider as an invalid message array.
	return llm.RepairPairing(view)
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
		out.Turns = append(out.Turns, &proto.PersistedTurn{
			Id:             t.ID,
			ConversationId: t.ConversationID,
			Role:           t.Role,
			Content:        t.Content,
			TokensIn:       int32(t.TokensIn),
			TokensOut:      int32(t.TokensOut),
			LatencyMs:      int32(t.LatencyMs),
			CreatedAt:      t.CreatedAt.Unix(),
		})
	}
	return out, nil
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
	max := contextmeter.ModelMax(x.primaryModel())
	sent, raw := 0, 0
	if store := x.Store(); store != nil && convID != "" {
		if turns, err := store.GetTurns(ctx, convID); err == nil {
			raw = estimateRawTokens(turns)
			state, _ := store.GetCompaction(ctx, convID)
			compSnap := x.cfgSvc.Get().Compaction
			elide := compSnap.ElideToolResults
			lossy := compSnap.LossyToolElision
			switch {
			case state.ConsolidatedJSON != "":
				// Compaction has run. Mirror AssembleHistory: summarized view
				// plus optional post-elision.
				view, _ := compactor.BuildSendView(turns, state)
				if elide {
					view, _ = compaction.ElideSupersededToolResults(view)
				}
				if lossy {
					view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
				}
				sent = compaction.TotalTokens(contextmeter.Default(), view)
			case elide || lossy:
				// No compaction but some elision is on. The meter must reflect
				// the elided view, not the raw history — otherwise a user
				// turns on a toggle and sees no change even though the model
				// receives less. Cost is one full-history tokenize per poll;
				// acceptable because the elided view is what the request path
				// is already building on every turn.
				view := agent.BuildLLMHistory(turns)
				if elide {
					view, _ = compaction.ElideSupersededToolResults(view)
				}
				if lossy {
					view, _ = compaction.KeepLastNToolResults(view, compaction.DefaultLossyElisionKeepLast)
				}
				sent = compaction.TotalTokens(contextmeter.Default(), view)
			default:
				// Fast path: no compaction, no elision. Cheap len/4 estimate
				// is intentional — the footer polls this frequently.
				sent = raw
			}
		}
	}
	var pct float64
	if max > 0 {
		pct = float64(sent) / float64(max)
		if pct > 1 {
			pct = 1
		}
	}
	isCompacting := false
	if x.convAgent != nil {
		isCompacting = x.convAgent.IsCompacting(convID)
	}
	return &proto.GetContextUsageResponse{
		TokensUsed: int32(sent), ModelMax: int32(max), Percent: pct,
		RawTokens: int32(raw), Compacting: isCompacting,
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
	out.SentTokens = int32(compaction.TotalTokens(contextmeter.Default(), view))
	out.RawTokens = int32(estimateRawTokens(turns))
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
// raw turns, streaming progress to the client.
func (x *svc) RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error {
	convID := req.GetConversationId()
	if convID == "" {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "conversation_id is required"})
	}
	if x.compactionGen == nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "compaction is not available (agent is running without a conversation store)"})
	}

	pre, post, err := x.compactionGen.Regenerate(stream.Context(), convID, req.GetIncremental(), func(line string) {
		_ = stream.Send(&proto.RegenerateContextProgress{Line: line})
	})
	if err != nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: err.Error(), PreTokens: int32(pre)})
	}
	verb := "rebuilt"
	if req.GetIncremental() {
		verb = "compacted"
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
	if op := x.openLegacy(); op != nil {
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
	var promptB strings.Builder
	promptB.WriteString("You are helping predict what a user might reasonably ask next in an ongoing conversation with an AI coding assistant. ")
	promptB.WriteString("Output ONE short natural next prompt the user might send, under 80 characters. ")
	promptB.WriteString("Rules: output ONLY the prompt text; no quotes, no formatting, no leading punctuation, no commentary, no labels.\n\n")
	if recap != "" {
		promptB.WriteString("Recap of the conversation so far:\n")
		promptB.WriteString(recap)
		promptB.WriteString("\n\n")
	}
	promptB.WriteString("Most recent turns:\n")
	promptB.WriteString(tail.String())
	promptB.WriteString("\nNext prompt:")

	res, err := e.Dispatch(ctx, dispatch.Spec{
		Mode:        dispatch.OneShot,
		Tier:        config.TierFastLightText,
		Role:        dispatch.RoleCoproc,
		Prompt:      promptB.String(),
		Source:      "suggest_next_prompt",
		RecordUsage: true,
	})
	if err != nil {
		return empty, nil
	}
	return &proto.SuggestNextPromptResponse{Suggestion: SanitizeSuggestion(res.Text)}, nil
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

// estimateRawTokens is a fast len/4 token estimate over the turns' text — used
// for the displayed raw/savings figure so the footer's frequent GetContextUsage
// poll never tokenizes the full uncompacted history with tiktoken.
//
// Uses the LARGER of Content and BlocksJSON per turn, not the sum: BlocksJSON
// is the canonical block-array serialization (what feeds BuildLLMHistory) and
// already contains every text body as a JSON string, so summing it with
// Content double-counts the text portion — inflating the number by ~8% on a
// text-heavy conversation. Content is retained only as the fallback for
// pre-BlocksJSON turns (older rows may have Content but an empty
// content_json column, since it defaulted to the empty string when added).
func estimateRawTokens(turns []conversation.Turn) int {
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
