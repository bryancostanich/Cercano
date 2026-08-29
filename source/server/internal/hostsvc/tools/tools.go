// Package tools provides the tool catalog service — the single home for the
// agent's tool registry, capability registry, and dispatch engine.
//
// The Catalog interface is what the front door (Server) depends on; the
// concrete Service type holds the three registries and the collaborators
// needed by RunAgenticDispatch without reaching back into the Server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/failurelog"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Catalog is the interface the front door (Server) depends on for tool,
// capability, and dispatch access. All tool-related call sites go through
// this interface.
type Catalog interface {
	// Registry returns the current agent tool registry.
	Registry() *agenttools.Registry
	// SetRegistry attaches a new agent tool registry.
	SetRegistry(r *agenttools.Registry)
	// CapRegistry returns the capability registry.
	CapRegistry() *capabilities.Registry
	// SetCapRegistry attaches the capability registry.
	SetCapRegistry(r *capabilities.Registry)
	// Engine returns the dispatch engine.
	Engine() *dispatch.Engine
	// SetEngine wires the dispatch engine and installs the agentic runner.
	SetEngine(e *dispatch.Engine)
	// SetPermBroker updates the permission broker used by RunAgenticDispatch.
	// Called by the front door when permissions are wired (SetPermissions).
	SetPermBroker(b permissions.Broker)
	// SetContextWindowResolver installs the resolver mapping a dispatch
	// sub-agent's resolved model to its input context window (tokens), feeding
	// the pre-flight size guard. Unset = guard disabled.
	SetContextWindowResolver(fn func(model string, isCloud bool) int)
	// SetFailureLog installs the sanitized failure/degradation diagnostic sink.
	SetFailureLog(w *failurelog.Writer)
	// GrantedRegistry builds the least-privilege sub-registry for a dispatch.
	// Returns the registry, the granted tool names, the ignored-unknown names,
	// and any error (e.g. empty resulting catalog).
	GrantedRegistry(tools []string) (*agenttools.Registry, []string, []string, error)
	// GetToolCallStore returns the conversation store (for GetToolCall).
	GetToolCallStore() conversation.Store
	// RunAgenticDispatch implements dispatch.AgenticRunner for the engine.
	RunAgenticDispatch(ctx context.Context, spec dispatch.Spec, sel inference.Selection, model string) (dispatch.Result, error)
	// InvokeCapability resolves and executes a named capability.
	// argsJSON is the JSON-encoded arguments (may be nil). workDir is the
	// caller's working directory. Returns (resultJSON, isError, errMsg).
	InvokeCapability(ctx context.Context, name string, argsJSON json.RawMessage, workDir string) ([]byte, bool, string)
}

// Service is the concrete implementation of Catalog.
type Service struct {
	toolRegistry   *agenttools.Registry
	capRegistry    *capabilities.Registry
	dispatchEngine *dispatch.Engine

	// Collaborators injected at construction time so RunAgenticDispatch
	// never reaches back into Server.
	//
	// permBroker: supplies Mode() and Store() for permission gating.
	// systemPrompt: builds the system prompt string from a working directory.
	//   Remains on the front door (buildSystemPrompt) and is passed here as a
	//   func so Task 5 can swap it out without changing this package.
	// store: returns the conversation store for sub-agent persistence and
	//   GetToolCall. May return nil (persistence skipped, dispatch still runs).
	// persistTurn: persists one turn in a sub-agent conversation.
	//   May be nil (turns not persisted; dispatch still runs).
	//
	// NOTE: store and persistTurn are func values rather than a
	// persistence.Service interface. hostsvc/persistence doesn't exist yet
	// (Task 5); the func-value seam keeps this package from importing it, so
	// tools depends only on the closures it is handed.
	permBroker   permissions.Broker
	systemPrompt func(workDir string) string
	store        func() conversation.Store
	persistTurn  func(ctx context.Context, convID string, m llm.Message)

	// ensureSubagent creates the sub-agent conversation row. In-process this is
	// unset and RunAgenticDispatch falls back to the store directly; the worker
	// sets it to a host-stream proxy (the worker has no local conversation store).
	ensureSubagent func(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) error

	// contextWindowFor resolves the input context window (in tokens) for a
	// dispatch sub-agent's resolved model, feeding RunAgenticDispatch's
	// pre-flight size guard (see agent.ToolLoopInput.ContextWindow). Returns 0
	// when the window is unknown (e.g. a cloud model whose window we don't
	// track), which disables the guard for that call. nil = guard always off.
	// A func-value seam so this package need not import config.
	contextWindowFor func(model string, isCloud bool) int

	// failureLog records sanitized dispatch failure/degradation events. Nil disables logging.
	failureLog *failurelog.Writer
}

// SetContextWindowResolver installs the resolver that maps a dispatch
// sub-agent's resolved model to its input context window in tokens. The front
// door wires this from config (e.g. config.LlamaServer.ContextSize for local
// models). Leaving it unset disables the dispatch pre-flight size guard.
func (x *Service) SetContextWindowResolver(fn func(model string, isCloud bool) int) {
	x.contextWindowFor = fn
}

// SetFailureLog installs the sanitized failure/degradation diagnostic sink.
func (x *Service) SetFailureLog(w *failurelog.Writer) { x.failureLog = w }

// New constructs a Catalog with the collaborators required by RunAgenticDispatch.
//
//   - permBroker: the permission broker (for Mode() and Store()); may be nil
//     (dispatch defaults to permissive mode with no permission store).
//   - systemPrompt: func that builds the system prompt for a working directory.
//     Pass the front door's buildSystemPrompt. May be nil (empty prompt used).
//   - store: func returning the conversation store for sub-agent persistence
//     and GetToolCall. May return nil (persistence skipped, dispatch still runs).
//   - persistTurn: func that persists one turn in a sub-agent conversation.
//     May be nil (turns not persisted; dispatch still runs).
func New(
	permBroker permissions.Broker,
	systemPrompt func(workDir string) string,
	store func() conversation.Store,
	persistTurn func(ctx context.Context, convID string, m llm.Message),
) *Service {
	return &Service{
		permBroker:   permBroker,
		systemPrompt: systemPrompt,
		store:        store,
		persistTurn:  persistTurn,
	}
}

// Registry returns the current agent tool registry.
func (x *Service) Registry() *agenttools.Registry { return x.toolRegistry }

// SetRegistry attaches a new agent tool registry.
func (x *Service) SetRegistry(r *agenttools.Registry) { x.toolRegistry = r }

// CapRegistry returns the capability registry.
func (x *Service) CapRegistry() *capabilities.Registry { return x.capRegistry }

// SetCapRegistry attaches the capability registry.
func (x *Service) SetCapRegistry(r *capabilities.Registry) { x.capRegistry = r }

// Engine returns the dispatch engine.
func (x *Service) Engine() *dispatch.Engine { return x.dispatchEngine }

// SetEngine wires the dispatch engine and installs this service's agentic
// runner so Agentic dispatches can call agent.RunToolLoop without creating an
// import cycle between internal/dispatch and internal/agent.
func (x *Service) SetEngine(e *dispatch.Engine) {
	x.dispatchEngine = e
	if e != nil {
		e.SetAgenticRunner(x.RunAgenticDispatch)
	}
}

// SetPermBroker updates the permission broker. Called by the front door when
// the permission store is wired (after construction).
func (x *Service) SetPermBroker(b permissions.Broker) { x.permBroker = b }

// GetToolCallStore returns the conversation store for use by GetToolCall.
func (x *Service) GetToolCallStore() conversation.Store {
	if x.store == nil {
		return nil
	}
	return x.store()
}

// resolveGrantName maps a caller-supplied tool name to a registered tool.
// Exact match wins; only on miss does it strip a leading `mcp__<server>__`
// segment and try again. This lets callers who accidentally emit host-prefixed
// names (e.g. an MCP host that presents Cercano's tools as `mcp__oc__Read` to
// the model, then passes the same string as data into `workflow.tools`) still
// find the underlying tool — without shadowing a legitimately hosted MCP tool
// that happens to be registered under its literal fully-qualified name.
//
// Returns the resolved name and true on success, or ("", false) on miss.
func (x *Service) resolveGrantName(requested string) (string, bool) {
	if _, ok := x.toolRegistry.Get(requested); ok {
		return requested, true
	}
	if rest, ok := strings.CutPrefix(requested, "mcp__"); ok {
		if idx := strings.Index(rest, "__"); idx >= 0 {
			stripped := rest[idx+2:]
			if stripped != "" {
				if _, ok := x.toolRegistry.Get(stripped); ok {
					return stripped, true
				}
			}
		}
	}
	return "", false
}

// GrantedRegistry builds the least-privilege tool registry for an agentic
// dispatch. With no requested tools, it grants read-only tools. With requested
// tools, it grants exactly the named set — W/X tools included: the dispatch
// call itself escalates to an X-tier confirm when the grant is write-capable
// (dispatchCap.TierFor), so by the time this runs a human has approved the
// toolset (or the mode is bypass). Unknown names are ignored and reported.
//
// Returns an error whenever the resulting catalog would be empty. Spawning a
// sub-agent with no tools is never what the caller intended, and the resulting
// loop (model improvises tool calls with no schema, gets errors, hits the
// 3-consecutive-error abort) is far worse than a clear error naming the
// offending inputs and the registered tools available.
//
// Returns the registry plus the granted and ignored-unknown name lists, which
// ride back to the caller on dispatch.Result.
func (x *Service) GrantedRegistry(tools []string) (*agenttools.Registry, []string, []string, error) {
	reg := agenttools.NewRegistry()
	var ignored, normalized []string
	if len(tools) > 0 {
		for _, name := range tools {
			resolved, ok := x.resolveGrantName(name)
			if !ok {
				ignored = append(ignored, name)
				continue
			}
			if resolved != name {
				normalized = append(normalized, name+"→"+resolved)
			}
			t, _ := x.toolRegistry.Get(resolved)
			_ = reg.Register(t)
		}
		if len(ignored) > 0 {
			log.Printf("[dispatch] subagent grant: ignored unknown tool names %v", ignored)
		}
		if len(reg.All()) == 0 {
			return nil, nil, ignored, fmt.Errorf(
				"dispatch: none of the requested tools are registered: %v; %s",
				tools, x.availableToolsHint(),
			)
		}
	} else {
		for _, t := range x.toolRegistry.Filter(agenttools.PermR) {
			_ = reg.Register(t)
		}
		if len(reg.All()) == 0 {
			return nil, nil, nil, fmt.Errorf(
				"dispatch: no read-only tools available in the registry to grant as the default sub-agent catalog",
			)
		}
	}
	logGrantSuccess(reg, normalized)
	return reg, registryToolNames(reg), ignored, nil
}

// registryToolNames returns the names of every tool in reg, for logging.
func registryToolNames(reg *agenttools.Registry) []string {
	all := reg.All()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name())
	}
	return names
}

// logGrantSuccess makes the grant success path visible in the agent log. The
// silent-success gap bit hard once: a cleanly normalized grant emitted no log
// lines at all, and "no dispatch log lines" was then read as "no dispatch
// happened" during a live post-mortem.
func logGrantSuccess(reg *agenttools.Registry, normalized []string) {
	note := ""
	if len(normalized) > 0 {
		note = fmt.Sprintf(" (normalized: %v)", normalized)
	}
	names := registryToolNames(reg)
	log.Printf("[dispatch] subagent grant: granted %d tool(s) %v%s", len(names), names, note)
}

// mutatingToolNames returns the set of tool names in reg tagged PermW or PermX
// (write / destructive). Derived from the registry — never a hardcoded list —
// so any newly added write tool is classified correctly with no drift.
func mutatingToolNames(reg *agenttools.Registry) map[string]bool {
	out := map[string]bool{}
	if reg == nil {
		return out
	}
	for _, t := range reg.All() {
		if p := t.Permission(); p == agenttools.PermW || p == agenttools.PermX {
			out[t.Name()] = true
		}
	}
	return out
}

// calledToolNames walks a finished tool loop's history and returns the set of
// tool names the sub-agent actually invoked (BlockToolUse blocks).
func calledToolNames(history []llm.Message) map[string]bool {
	out := map[string]bool{}
	for _, m := range history {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse && b.ToolName != "" {
				out[b.ToolName] = true
			}
		}
	}
	return out
}

// detectSuspiciousNoOp decides whether a finished agentic dispatch looks like a
// no-op that lied about completing. It reports the HIGH-CONFIDENCE case only:
// the sub-agent was granted at least one write/execute tool, called NONE of
// them, yet returned a non-empty final answer. That is a provable
// contradiction — a fix/migration cannot have happened without a write or exec
// call — not a guess about whether the task "needed" more work.
//
// Intentionally NOT flagged (returns false): a read-only sub-agent that made
// few calls. "Trace why X happens" answered after one Grep may be lazy or may
// be a genuinely easy trace; we cannot tell, so flagging it would be a false
// positive. Those are logged at low signal by the caller instead.
//
// finalText is the assembled answer; called is the set from calledToolNames;
// mutating is the set from mutatingToolNames over the granted registry.
func suspiciousNoOpMessage(reason string) string {
	return "sub-agent failed validation: " + reason
}

func detectSuspiciousNoOp(finalText string, called, mutating map[string]bool) (bool, string) {
	if len(mutating) == 0 {
		return false, "" // no write/exec tool was granted — no contradiction possible
	}
	if strings.TrimSpace(finalText) == "" {
		return false, "" // no "done" claim to contradict
	}
	for name := range called {
		if mutating[name] {
			return false, "" // it used at least one write/exec tool — genuine work
		}
	}
	granted := make([]string, 0, len(mutating))
	for name := range mutating {
		granted = append(granted, name)
	}
	sort.Strings(granted)
	reason := fmt.Sprintf("granted write/execute tool(s) [%s] but called none of them, yet reported completion — the delegated work likely did not happen", strings.Join(granted, ", "))
	return true, reason
}

// availableToolsHint returns a comma-separated list of registered tool names,
// truncated so a pathological registry doesn't blow up the error message.
func (x *Service) availableToolsHint() string {
	const maxNames = 30
	pool := x.toolRegistry.All()
	if len(pool) == 0 {
		return "no tools are registered in the sub-agent's catalog"
	}
	names := make([]string, 0, len(pool))
	for _, t := range pool {
		names = append(names, t.Name())
	}
	suffix := ""
	if len(names) > maxNames {
		names = names[:maxNames]
		suffix = fmt.Sprintf(" (+%d more)", len(pool)-maxNames)
	}
	return fmt.Sprintf("available tools: %s%s", strings.Join(names, ", "), suffix)
}

// dispatchStore returns the persistent conversation store used for sub-agent
// loop persistence, or nil when no store func is wired (persistence is then
// skipped; dispatch still runs).
func (x *Service) dispatchStore() conversation.Store {
	if x.store == nil {
		return nil
	}
	return x.store()
}

func (x *Service) logDispatchFailure(event string, spec dispatch.Spec, subConvID, provider, model string, isCloud bool, granted, ignored []string, err error, extra failurelog.Event) {
	if x == nil || x.failureLog == nil {
		return
	}
	fields := failurelog.Event{
		"scope":           "dispatch",
		"conversation_id": spec.ConversationID,
		"dispatch_id":     subConvID,
		"provider":        provider,
		"model":           model,
		"tier":            string(spec.Tier),
		"is_cloud":        isCloud,
		"tool_names":      append([]string(nil), granted...),
		"ignored_tools":   append([]string(nil), ignored...),
	}
	if err != nil {
		fields["error_class"] = string(llm.ClassOf(err))
		fields["message"] = failurelog.SanitizeMessage(err.Error())
	}
	for k, v := range extra {
		fields[k] = v
	}
	x.failureLog.Log(event, fields)
}

// SetEnsureSubagent installs the func that creates a sub-agent conversation row.
// The worker wires this to a host-stream proxy (it has no local store); in-
// process it stays nil and ensureSubagentConv falls back to the store.
func (x *Service) SetEnsureSubagent(fn func(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) error) {
	x.ensureSubagent = fn
}

// ensureSubagentConv creates the sub-agent conversation row and reports whether
// persistence is active. It prefers the injected ensureSubagent proxy (worker
// path) and otherwise uses the local store (in-process). Returns false when
// neither is available or the create fails — dispatch still runs, its turns just
// are not persisted.
func (x *Service) ensureSubagentConv(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) bool {
	ensure := x.ensureSubagent
	if ensure == nil {
		st := x.dispatchStore()
		if st == nil {
			return false
		}
		ensure = st.EnsureSubagentConversation
	}
	if err := ensure(ctx, id, parentID, projectDir, model, grantedTools); err != nil {
		log.Printf("[dispatch] subagent persistence unavailable: %v", err)
		return false
	}
	return true
}

// RunAgenticDispatch implements dispatch.AgenticRunner. It is wired onto the
// dispatch.Engine via SetEngine so that internal/dispatch need not import
// internal/agent (which would create an import cycle).
//
// It builds a least-privilege registry, assembles a system prompt, and runs
// agent.RunToolLoop, returning the final text and token counts.
func (x *Service) RunAgenticDispatch(ctx context.Context, spec dispatch.Spec, sel inference.Selection, model string) (dispatch.Result, error) {
	// 1. Build the least-privilege tool registry. W/X grants are legitimate
	// here: the dispatch call itself gated as X at the parent when the grant
	// was write-capable, so execution implies human approval (or bypass).
	reg, granted, ignored, err := x.GrantedRegistry(spec.Tools)
	if err != nil {
		x.logDispatchFailure("dispatch.failed", spec, "", "", model, sel.IsCloud, nil, ignored, err, failurelog.Event{
			"error_class": "grant_failed",
		})
		emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{Kind: "error", Text: fmt.Sprintf("sub-agent grant failed: %v", err), IsError: true})
		return dispatch.Result{}, err
	}
	// Grant + ignored-tool details ride on the "started" event below, which
	// carries GrantedTools/IgnoredTools for the sub-agent tab to render. A
	// separate pre-start emit here had no SubAgentID yet, so it leaked into
	// the parent transcript instead of the sub-agent's own tab.

	// Permission scope for the sub-loop. A W/X grant was approved as a set —
	// that one approval covers the run, so the sub-loop runs pre-authorized
	// (static bypass store; the granted registry stays the hard boundary).
	// R-only grants keep the parent store: R never gates anyway.
	var perms *agent.PermissionStore
	if x.permBroker != nil {
		perms = x.permBroker.Store()
	}
	for _, t := range reg.All() {
		if t.Permission() != agenttools.PermR {
			perms = agent.NewStaticPermissionStore(agent.ModeBypass)
			break
		}
	}

	// 2. Build a lean sub-agent prompt. Do NOT reuse the main-agent system
	// prompt here: it contains recursive delegation/planning/git-protocol rules
	// intended for the top-level agent, and local models have been observed to
	// ignore tools and emit unrelated text under that prompt. A dispatch worker
	// needs bounded task/tool instructions, not full-frontier agency.
	system := buildSubagentSystemPrompt(spec.WorkDir, granted)

	// 3. Sub-agent identity + persistence. The sub-agent's conversation id is
	// minted unconditionally: it is the
	// sub-agent's own identity — it drives the sub-agent's tab (every emitted
	// event carries it as SubAgentID) and its scoped provider session — so it
	// must exist whether or not persistence is wired. Persistence is a
	// best-effort layer keyed on this same id: when a store is available
	// (in-process directly, or the worker via its host proxy) the loop's turns
	// land in a hidden "subagent" conversation linked to the parent, so a
	// dispatch can be post-mortemed from the DB instead of dead-ending at the
	// parent's tool_result.
	subConvID := conversation.NewID()
	var onTurn func(m llm.Message)
	// persisted reports whether the sub-agent conversation ROW was created (a
	// store in-process, or the worker's host proxy). subConvID still identifies
	// the tab regardless; the Result's SubConversationID reflects persistence only.
	persisted := x.ensureSubagentConv(ctx, subConvID, spec.ConversationID, spec.WorkDir, model, granted)
	if persisted && x.persistTurn != nil {
		x.persistTurn(ctx, subConvID, agent.UserMessage(spec.Task, nil))
		onTurn = func(m llm.Message) { x.persistTurn(ctx, subConvID, m) }
	}
	provider := ""
	if sel.Provider != nil {
		provider = sel.Provider.Name()
	}
	location := "open"
	if sel.IsCloud {
		location = "cloud"
	}
	log.Printf("[dispatch] subagent start: conv=%s route=%s provider=%s model=%s tier=%s tools=%v", subConvID, location, provider, model, spec.Tier, registryToolNames(reg))
	subTitle := "sub"
	emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "started", Text: fmt.Sprintf("sub-agent start: conv=%s route=%s provider=%s model=%s tier=%s tools=%s", subConvID, location, provider, model, spec.Tier, strings.Join(granted, ", ")), GrantedTools: granted, IgnoredTools: ignored})
	emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "prompt", Text: spec.Task, GrantedTools: granted, IgnoredTools: ignored})

	// The subagent's provider calls must carry their OWN session identity, not
	// the parent conversation's (inherited via ctx), so anomaly attribution and
	// per-session tagging stay disjoint from the parent. subConvID is always set
	// (minted above).
	ctx = llm.WithSessionID(ctx, subConvID)

	// 4. Run the bounded tool loop. Use greedy decoding: qwen/mistral.rs tool-call
	// compliance is extremely temperature-sensitive, and dispatch workers value
	// deterministic tool use over creative prose.
	greedy := 0.0
	var buf strings.Builder
	// Resolve the sub-agent model's context window for the pre-flight size
	// guard. 0 (unknown, or no resolver wired) disables the guard for this call.
	contextWindow := 0
	if x.contextWindowFor != nil {
		contextWindow = x.contextWindowFor(model, sel.IsCloud)
	}
	res, err := agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:           sel.Provider,
		Model:              model,
		Tier:               string(spec.Tier),
		System:             system,
		ContextWindow:      contextWindow,
		Registry:           reg,
		Permissions:        perms,
		UserInput:          spec.Task,
		MaxIterations:      spec.MaxIterations,
		Temperature:        &greedy,
		FlattenToolResults: true,
		WorkDir:            spec.WorkDir,
		ConversationID:     subConvID, // nested dispatches link to this sub-conversation
		PreauthorizedTools: granted,
		OnTextDelta: func(t string) {
			buf.WriteString(t)
			emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "token", Text: t, GrantedTools: granted, IgnoredTools: ignored})
		},
		EventSink: func(ev agent.LoopEvent) {
			if ev.Kind == agent.LoopToolExecComplete && ev.IsError {
				x.logDispatchFailure("dispatch.tool_error", spec, subConvID, provider, model, sel.IsCloud, granted, ignored, nil, failurelog.Event{
					"tool_name":   ev.ToolName,
					"error_class": "tool_error",
					"message":     "sub-agent tool returned an error",
				})
			}
			if progress, ok := formatSubagentLoopEvent(subConvID, spec.ConversationID, subTitle, ev); ok {
				emitDispatchProgress(spec.Emit, progress)
			}
		},
		OnTurnComplete: onTurn,
		// PermissionRequester: nil — R-tier won't gate; W/X runs pre-authorized after the parent grant confirm.
	})
	if err != nil {
		log.Printf("[dispatch] subagent done: conv=%s err=%v", subConvID, err)
		x.logDispatchFailure("dispatch.tool_loop_failed", spec, subConvID, provider, model, sel.IsCloud, granted, ignored, err, nil)
		emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "error", Text: fmt.Sprintf("sub-agent failed: conv=%s err=%v", subConvID, err), GrantedTools: granted, IgnoredTools: ignored, IsError: true})
		return dispatch.Result{}, err
	}
	log.Printf("[dispatch] subagent done: conv=%s route=%s provider=%s model=%s tier=%s iterations=%d tokens_in=%d tokens_out=%d",
		subConvID, location, provider, model, spec.Tier, res.Iterations, res.InputTokens, res.OutputTokens)
	emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "done", Text: fmt.Sprintf("sub-agent done: conv=%s iterations=%d", subConvID, res.Iterations), GrantedTools: granted, IgnoredTools: ignored})

	// 5. Assemble result. Prefer ToolLoopResult.FinalText (the last assistant
	// text block from the loop); fall back to the streamed buf if it's empty
	// (should not happen in practice, but defensive).
	text := res.FinalText
	if text == "" {
		text = buf.String()
	}

	// SubConversationID reports the PERSISTED sub-conversation (empty when the row
	// was not created); the tab is driven separately by the SubAgentID events.
	subConvResult := ""
	if persisted {
		subConvResult = subConvID
	}

	// No-op detection. Classify the granted registry (authoritative source for
	// R/W/X) and the tools the loop actually called, then check for the
	// provable contradiction: equipped to write/exec, wrote/exec'd nothing,
	// still claimed done. This is a hard validation error for write/exec grants
	// so the parent cannot trust fabricated completion.
	mutating := mutatingToolNames(reg)
	// Use the tools the loop actually invoked, recorded directly from each
	// turn's BlockToolUse blocks (res.CalledTools). Do NOT re-derive from
	// res.History: in the sub-agent flatten path the model-facing history
	// replaces tool_use blocks with a text summary, so History carries no
	// BlockToolUse and every run would look like called=[] — a false no-op.
	called := make(map[string]bool, len(res.CalledTools))
	for _, name := range res.CalledTools {
		called[name] = true
	}
	suspicious, reason := detectSuspiciousNoOp(text, called, mutating)
	if suspicious {
		log.Printf("[dispatch] subagent SUSPICIOUS no-op: conv=%s granted_write=%v called=%v reason=%q",
			subConvID, sortedKeys(mutating), sortedKeys(called), reason)
		x.logDispatchFailure("dispatch.degraded", spec, subConvID, provider, model, sel.IsCloud, granted, ignored, nil, failurelog.Event{
			"error_class":        "suspicious_noop",
			"message":            suspiciousNoOpMessage(reason),
			"called_tools_count": len(called),
			"called_tools":       sortedKeys(called),
		})
		emitDispatchProgress(spec.Emit, agenttools.ProgressEvent{SubAgentID: subConvID, SubAgentParentID: spec.ConversationID, SubAgentTitle: subTitle, Kind: "error", Text: suspiciousNoOpMessage(reason), GrantedTools: granted, IgnoredTools: ignored, IsError: true})
		return dispatch.Result{
			Text:              text,
			Model:             model,
			Provider:          provider,
			Tier:              string(spec.Tier),
			IsCloud:           sel.IsCloud,
			InputTokens:       res.InputTokens,
			OutputTokens:      res.OutputTokens,
			SubConversationID: subConvResult,
			GrantedTools:      granted,
			IgnoredTools:      ignored,
			Suspicious:        true,
			SuspicionReason:   reason,
		}, fmt.Errorf("%s", suspiciousNoOpMessage(reason))
	} else if len(called) <= 1 && len(mutating) == 0 {
		// Low-signal read-only run: not flagged (no provable contradiction),
		// but logged so we can study how often near-empty runs happen without
		// crying wolf on the returned Result.
		log.Printf("[dispatch] subagent low-signal run: conv=%s called=%v (read-only grant, <=1 tool call)",
			subConvID, sortedKeys(called))
		x.logDispatchFailure("dispatch.degraded", spec, subConvID, provider, model, sel.IsCloud, granted, ignored, nil, failurelog.Event{
			"error_class":        "low_signal",
			"message":            "read-only sub-agent completed with one or fewer tool calls",
			"called_tools_count": len(called),
			"called_tools":       sortedKeys(called),
		})
	}

	return dispatch.Result{
		Text:              text,
		Model:             model,
		Provider:          provider,
		Tier:              string(spec.Tier),
		IsCloud:           sel.IsCloud,
		InputTokens:       res.InputTokens,
		OutputTokens:      res.OutputTokens,
		SubConversationID: subConvResult,
		GrantedTools:      granted,
		IgnoredTools:      ignored,
		Suspicious:        suspicious,
		SuspicionReason:   reason,
	}, nil
}

// sortedKeys returns the keys of a string-set in stable order, for logging.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func emitDispatchProgress(emit func(agenttools.ProgressEvent), ev agenttools.ProgressEvent) {
	if emit == nil {
		return
	}
	emit(ev)
}

func buildSubagentSystemPrompt(workDir string, grantedTools []string) string {
	var b strings.Builder
	b.WriteString("You are a bounded Cercano sub-agent. Complete only the delegated task.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Use only the tools provided in this request. Do not mention or call unavailable tools.\n")
	b.WriteString("- Do not delegate to another agent. Do not call dispatch/workflow.\n")
	b.WriteString("- If the answer depends on repository contents, call Read, Grep, Glob, or another granted inspection tool before answering.\n")
	b.WriteString("- Do not claim you inspected, ran, or verified anything unless you used a tool in this run.\n")
	b.WriteString("- Keep the final answer concise and limited to the delegated task. Include file:line citations when applicable.\n")
	b.WriteString("- End when the delegated task is complete; do not ask for follow-up work.\n\n")
	b.WriteString("<env>\n")
	if strings.TrimSpace(workDir) != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", workDir)
	}
	fmt.Fprintf(&b, "Platform: %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "Today's date: %s\n", time.Now().Format("2006-01-02"))
	b.WriteString("</env>\n\n")
	b.WriteString("Resolve relative file paths against the working directory; do not search outside it unless the delegated task explicitly provides an absolute path.\n")
	if len(grantedTools) > 0 {
		b.WriteString("\n<granted_tools>\n")
		for _, name := range grantedTools {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		b.WriteString("</granted_tools>\n")
	}
	return b.String()
}

func formatSubagentLoopEvent(id, parentID, title string, ev agent.LoopEvent) (agenttools.ProgressEvent, bool) {
	progress := agenttools.ProgressEvent{
		SubAgentID:       id,
		SubAgentParentID: parentID,
		SubAgentTitle:    title,
		ToolUseID:        ev.ToolUseID,
		ToolName:         ev.ToolName,
		ArgsSummary:      ev.Summary,
		ArgsJSON:         ev.ArgsJSON,
		Summary:          ev.Summary,
		Detail:           ev.Detail,
		StartLine:        ev.StartLine,
		IsError:          ev.IsError,
	}
	switch ev.Kind {
	case agent.LoopToolUseStart:
		progress.Kind = "tool_use_start"
		progress.Text = fmt.Sprintf("sub-agent planned tool: %s", ev.ToolName)
	case agent.LoopToolUseStop:
		progress.Kind = "tool_use_stop"
		if ev.Summary != "" {
			progress.Text = fmt.Sprintf("sub-agent tool args: %s %s", ev.ToolName, ev.Summary)
		} else {
			progress.Text = fmt.Sprintf("sub-agent tool args ready: %s", ev.ToolName)
		}
	case agent.LoopToolExecStart:
		progress.Kind = "tool_exec_start"
		progress.Text = fmt.Sprintf("sub-agent running tool: %s", ev.ToolName)
	case agent.LoopToolExecComplete:
		progress.Kind = "tool_exec_complete"
		if ev.IsError {
			progress.Text = fmt.Sprintf("sub-agent tool failed: %s — %s", ev.ToolName, ev.Summary)
		} else if ev.Summary != "" {
			progress.Text = fmt.Sprintf("sub-agent tool complete: %s — %s", ev.ToolName, ev.Summary)
		} else {
			progress.Text = fmt.Sprintf("sub-agent tool complete: %s", ev.ToolName)
		}
	case agent.LoopProgress:
		if ev.SubAgentID != "" {
			progress.SubAgentID = ev.SubAgentID
			progress.SubAgentParentID = ev.SubAgentParentID
			if progress.SubAgentParentID == "" {
				progress.SubAgentParentID = id
			}
			progress.SubAgentTitle = ev.SubAgentTitle
			progress.GrantedTools = append([]string(nil), ev.GrantedTools...)
			progress.IgnoredTools = append([]string(nil), ev.IgnoredTools...)
		}
		progress.Kind = ev.SubAgentKind
		progress.Text = ev.Summary
		if progress.Kind == "" {
			progress.Kind = "progress"
		}
	default:
		return agenttools.ProgressEvent{}, false
	}
	return progress, true
}

// InvokeCapability resolves and executes a named capability. Used by the front
// door's InvokeCapability RPC so the logic lives in one place.
// Returns (resultJSON, isError, errMsg).
func (x *Service) InvokeCapability(ctx context.Context, name string, argsJSON json.RawMessage, workDir string) ([]byte, bool, string) {
	if x.capRegistry == nil {
		return nil, true, "capability registry not initialized"
	}
	cap, ok := x.capRegistry.Get(name)
	if !ok {
		return nil, true, fmt.Sprintf("unknown capability %q", name)
	}
	call := &capabilities.Call{
		Args:    argsJSON,
		WorkDir: workDir,
		// Allow-all: the host (MCP layer) is responsible for permission gating.
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
		Svc:               x.capRegistry.Services(),
	}
	res, err := cap.Execute(ctx, call)
	if err != nil {
		return nil, true, err.Error()
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, true, "marshal result: " + err.Error()
	}
	return b, false, ""
}
