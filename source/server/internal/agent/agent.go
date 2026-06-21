package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cercano/source/server/internal/conversation"
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

// Agent is the top-level orchestrator for AI requests.
type Agent struct {
	router       Router
	coordinator  Coordinator
	conversation *ConversationStore
	persistent   conversation.Store
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

	if a.conversation == nil || req.ConversationID == "" {
		return
	}

	history, err := a.conversation.LoadHistory(ctx, req.ConversationID)
	if err != nil || history == "" {
		return
	}

	augmented = history + "\n" + original
	return
}

// storeConversationTurn compacts the response and stores the turn in BOTH
// the in-memory session store (fast intra-session history) AND the persistent
// SQLite store (cross-restart /history & /resume), if configured.
func (a *Agent) storeConversationTurn(ctx context.Context, conversationID, originalInput string, resp *Response) {
	if conversationID == "" {
		return
	}
	compacted := CompactResponse(resp)
	if a.conversation != nil {
		_ = a.conversation.AppendTurn(ctx, conversationID, originalInput, compacted)
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
	}
}

// ResumeConversation loads persisted turns for the given conversation id and
// hydrates them into the in-memory session store, so that the next call to
// loadHistory sees full context. Returns the persisted turns so the caller
// (CLI) can render them in the scrollback. If no persistent store is
// configured the returned slice is empty.
func (a *Agent) ResumeConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error) {
	if a.persistent == nil {
		return nil, nil
	}
	turns, err := a.persistent.GetTurns(ctx, conversationID)
	if err != nil {
		return nil, err
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

// ListConversations returns persisted conversation summaries (project filter
// + limit). Empty slice if no persistent store is configured.
func (a *Agent) ListConversations(ctx context.Context, projectDir string, limit int) ([]conversation.Info, error) {
	if a.persistent == nil {
		return nil, nil
	}
	return a.persistent.List(ctx, projectDir, limit)
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
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
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
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
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
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
	return res, nil
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
			a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
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
		a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
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
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, res)
	return res, nil
}
