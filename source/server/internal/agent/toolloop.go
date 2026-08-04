package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

type LoopEventKind string

const (
	LoopToolUseStart     LoopEventKind = "tool_use_start"
	LoopToolUseStop      LoopEventKind = "tool_use_stop"
	LoopToolExecStart    LoopEventKind = "tool_exec_start"
	LoopToolExecComplete LoopEventKind = "tool_exec_complete"
	LoopProgress         LoopEventKind = "progress"
	// LoopNotice carries a resilience-engine narration line ("anthropic quota
	// reached — switching to openai") in Summary. Display-only: it reaches the
	// user via the progress channel and is never part of the transcript.
	LoopNotice             LoopEventKind = "notice"
	LoopPermissionRequired LoopEventKind = "permission_required"
	LoopWatchdogChallenge  LoopEventKind = "watchdog_challenge"
	LoopWatchdogEscalate   LoopEventKind = "watchdog_escalate"
	LoopWatchdogEcho       LoopEventKind = "watchdog_echo"
	LoopWatchdogBlock      LoopEventKind = "watchdog_block"
)

type LoopEvent struct {
	Kind        LoopEventKind
	ToolUseID   string
	ToolName    string
	ArgsJSON    string
	Tier        string
	Destructive bool // display-only ⚠ hint (MCP destructiveHint); never affects gating
	Summary     string
	Detail      string
	IsError     bool
	// StartLine mirrors Result.StartLine on tool_exec_complete events: the
	// 1-based line where a file edit/write began (0 = not applicable).
	StartLine int

	TaskChangeKind string
	TaskSnapshot   agenttools.TaskProgressSnapshot

	SubAgentID       string
	SubAgentParentID string
	SubAgentTitle    string
	SubAgentKind     string
	GrantedTools     []string
	IgnoredTools     []string
}

func loopProgressEvent(defaultToolUseID, defaultToolName string, progress agenttools.ProgressEvent) LoopEvent {
	ev := LoopEvent{
		Kind:             LoopProgress,
		ToolUseID:        defaultToolUseID,
		ToolName:         defaultToolName,
		Summary:          progress.Text,
		ArgsJSON:         progress.ArgsJSON,
		Detail:           progress.Detail,
		IsError:          progress.IsError,
		StartLine:        progress.StartLine,
		TaskChangeKind:   progress.TaskChangeKind,
		TaskSnapshot:     progress.TaskSnapshot,
		SubAgentID:       progress.SubAgentID,
		SubAgentParentID: progress.SubAgentParentID,
		SubAgentTitle:    progress.SubAgentTitle,
		SubAgentKind:     progress.Kind,
		GrantedTools:     append([]string(nil), progress.GrantedTools...),
		IgnoredTools:     append([]string(nil), progress.IgnoredTools...),
	}
	if progress.ToolUseID != "" {
		ev.ToolUseID = progress.ToolUseID
	}
	if progress.ToolName != "" {
		ev.ToolName = progress.ToolName
	}
	if progress.Summary != "" {
		ev.Summary = progress.Summary
	}
	if ev.Summary == "" {
		ev.Summary = progress.Text
	}
	return ev
}

type ToolLoopInput struct {
	Provider    inference.Provider
	Registry    *agenttools.Registry
	Permissions *PermissionStore

	// Profile is a capability fence layered on top of the permission mode (see
	// profile.go). The zero value restricts nothing. A read-only Profile (e.g.
	// PlanProfile) both hides forbidden tools from the model and denies them at
	// the gate. It is orthogonal to Permissions.Mode(), which planning still
	// honors for the reads it allows.
	Profile     Profile
	ConvHistory []llm.Message
	UserInput   string
	Images      []InlineImage
	Model       string
	// Tier is the capability-tier name Model was resolved from (empty for the
	// provider's default). Rides every ChatRequest as routing metadata so the
	// cloud failover composite can re-resolve the tier in the backup vendor's
	// namespace (dispatch sub-agents pin tier-resolved models).
	Tier   string
	System string

	// PermissionRequester is the callback the loop uses to surface a
	// confirm prompt to the active client (nil = auto-allow, useful in tests).
	PermissionRequester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (allow bool, err error)

	// EventSink receives lifecycle events as the loop runs. Nil-safe.
	EventSink func(ev LoopEvent)

	// WatchdogGate, when set, is a protocol-supervision gate consulted before
	// each W/X tool call, independent of permission mode. nil = disabled.
	WatchdogGate WatchdogGate

	// WatchdogTurnEnd supervises the model's final reply text at turn boundaries.
	// nil = disabled.
	WatchdogTurnEnd WatchdogTurnEnd

	// OnTextDelta, when set, receives assistant text deltas as they stream from
	// the provider, so the server can forward them to the client for live
	// token-by-token rendering. Nil-safe.
	OnTextDelta func(string)

	// OnTurnComplete, when set, is called with each assistant / tool-result
	// message as soon as it's appended to the history — so the host can persist
	// turns incrementally (crash resilience) instead of only at end-of-turn. It
	// is NOT called for the leading user message (the host persists that up front
	// before the loop runs). Nil-safe.
	OnTurnComplete func(m llm.Message)

	// MaxIterations caps the number of LLM round-trips this call may make.
	// 0 means use config.DefaultToolLoopMaxIterations; -1 means unlimited.
	MaxIterations int

	// MaxTokensPerTurn sets the MaxTokens field on each llm.ChatRequest.
	// 0 means use the package default (4096).
	MaxTokensPerTurn int

	// Temperature, when non-nil, is forwarded to the provider for every model
	// turn. A pointer to 0 requests greedy decoding; sub-agent dispatch uses
	// this because local tool-call compliance is extremely temperature-sensitive.
	Temperature *float64

	// FlattenToolResults feeds tool results back as ordinary user text instead of
	// provider-native tool-result messages. Some local OpenAI-compatible runtimes
	// (qwen via mistral.rs) can emit tool calls but derail when consuming
	// role:"tool" result history; this compatibility mode preserves tool use
	// while giving the model plain text evidence for the final answer.
	FlattenToolResults bool

	// ConversationID names the conversation this loop serves. Threaded onto
	// ctx so tools that spawn linked work (dispatch) can record lineage.
	ConversationID string

	// WorkDir is the turn's working directory. Threaded onto ctx so tools
	// resolve relative paths against it — never via process cwd.
	WorkDir string
}

type ToolLoopResult struct {
	FinalText    string
	FinalBlocks  []llm.Block
	Iterations   int
	History      []llm.Message
	InputTokens  int // last LLM call's provider-reported input tokens (context occupancy)
	OutputTokens int // last LLM call's provider-reported output tokens
}

// MaxToolLoopIterations caps the LLM round-trips per turn when no explicit
// ToolLoopInput.MaxIterations is supplied. Kept as an alias for older tests and
// callers; the config package owns the single source of truth.
const MaxToolLoopIterations = config.DefaultToolLoopMaxIterations

func summarizeResult(res *agenttools.Result) string {
	if res == nil {
		return ""
	}
	// Prefer the curated one-line Note (e.g. Bash's "exit 1 · 2ms", or a
	// truncation/fallback caveat). It's purpose-built for a glance; the raw
	// result text is not.
	if res.Note != "" {
		return truncateRunes(res.Note, 80)
	}
	switch res.Type {
	case agenttools.ResultText:
		// First line only, truncated rune-safely (the old text[:80] sliced
		// bytes and could split a multibyte rune into invalid UTF-8).
		return truncateRunes(firstLine(res.Text), 80)
	case agenttools.ResultRows:
		return fmt.Sprintf("rows: %d", len(res.Rows))
	case agenttools.ResultJSON:
		return fmt.Sprintf("json: %d bytes", len(res.JSON))
	}
	return ""
}

// firstLine returns s up to the first newline.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// truncateRunes caps s to max runes, appending an ellipsis when it cuts. It
// counts runes (not bytes), so it never splits a multibyte character.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func toolNamesForLog(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	return names
}

func temperatureForLog(t *float64) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%g", *t)
}

func systemHasLeanSubagentMarker(system string) bool {
	return strings.Contains(system, "bounded Cercano sub-agent")
}

func flattenToolUseSummary(calls []llm.Block) string {
	if len(calls) == 0 {
		return "I used the granted tools."
	}
	var b strings.Builder
	b.WriteString("I used the granted tools: ")
	for i, call := range calls {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(call.ToolName)
	}
	b.WriteString(".")
	return b.String()
}

func flattenToolResultsForModel(calls, results []llm.Block) string {
	var b strings.Builder
	b.WriteString("Tool results from the granted tools follow. Use only these results and the original user request to continue.\n\n")
	byID := make(map[string]llm.Block, len(calls))
	for _, call := range calls {
		byID[call.ToolUseID] = call
	}
	for i, result := range results {
		call := byID[result.ToolUseRef]
		if i > 0 {
			b.WriteString("\n\n")
		}
		name := call.ToolName
		if name == "" {
			name = "tool"
		}
		fmt.Fprintf(&b, "Tool result from %s(%s):\n", name, string(call.ToolInput))
		if result.IsError {
			b.WriteString("ERROR: ")
		}
		b.WriteString(result.Content)
	}
	b.WriteString("\n\nUsing only the tool results above, answer the original request. If the original request asked for citations, include exact file:line citations from the tool output.")
	return b.String()
}

func RunToolLoop(ctx context.Context, in ToolLoopInput) (ToolLoopResult, error) {
	ctx = agenttools.WithWorkDir(ctx, in.WorkDir)
	ctx = agenttools.WithConversationID(ctx, in.ConversationID)
	if !in.Provider.Capabilities().SupportsTools {
		return ToolLoopResult{}, fmt.Errorf("provider %s does not support tools", in.Provider.Name())
	}

	emit := func(ev LoopEvent) {
		if in.EventSink != nil {
			in.EventSink(ev)
		}
	}

	maxIters, unlimitedIters := config.EffectiveMaxIterations(in.MaxIterations)
	maxTokens := 4096
	if in.MaxTokensPerTurn > 0 {
		maxTokens = in.MaxTokensPerTurn
	}

	hist := append([]llm.Message{}, in.ConvHistory...)
	hist = append(hist, llm.Message{
		Role:   llm.RoleUser,
		Blocks: buildUserBlocks(in.UserInput, in.Images),
	})

	// appendTurn records a new (this-turn) assistant or tool-result message into
	// the model-facing history and notifies the host (OnTurnComplete) so it can
	// persist it immediately. The leading user message above is intentionally NOT
	// routed through here — the host persists it up front before the loop runs.
	appendTurn := func(m llm.Message) {
		hist = append(hist, m)
		if in.OnTurnComplete != nil {
			in.OnTurnComplete(m)
		}
	}
	// appendModelTurn records only the next model's view of history. It is used
	// by FlattenToolResults so local providers see plain text while persistence
	// still records the original structured tool_use/tool_result transcript.
	appendModelTurn := func(m llm.Message) { hist = append(hist, m) }
	persistTurn := func(m llm.Message) {
		if in.OnTurnComplete != nil {
			in.OnTurnComplete(m)
		}
	}

	// Advertisement half of the capability profile (see profile.go): when the
	// profile restricts, forbidden tools are filtered out of the catalog so the
	// model never reaches for a tool it can't have. Enforcement is separate,
	// below at the gate — filtering is ergonomics, not the fence.
	var catalog []llm.Tool
	if in.Profile.Restricts() {
		catalog = agenttools.BuildToolCatalogFiltered(in.Registry, in.Profile.Allows)
	} else {
		catalog = agenttools.BuildToolCatalog(in.Registry)
	}
	consecutiveErrors := 0
	var lastIn, lastOut int

	// seenToolUse tracks every tool_use id already emitted in this conversation
	// (seeded from prior turns). A well-formed stream never reuses a tool_use id
	// across turns, so a re-emitted id means the model turn replays earlier
	// content — the fabricated-turn fingerprint the framing guard cannot see.
	seenToolUse := map[string]bool{}
	for _, m := range in.ConvHistory {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse && b.ToolUseID != "" {
				seenToolUse[b.ToolUseID] = true
			}
		}
	}

	for iter := 0; unlimitedIters || iter < maxIters; iter++ {
		req := llm.ChatRequest{
			Model:       in.Model,
			Tier:        in.Tier,
			System:      in.System,
			Messages:    hist,
			Tools:       catalog,
			MaxTokens:   maxTokens,
			Temperature: in.Temperature,
		}
		log.Printf("[tool-loop] model request: conv=%s provider=%s model=%s iter=%d stream=true temp=%s max_tokens=%d tools=%v lean_subagent_prompt=%t flatten_tool_results=%t system_prefix=%q user_prefix=%q history=%d",
			in.ConversationID, in.Provider.Name(), in.Model, iter+1, temperatureForLog(req.Temperature), req.MaxTokens, toolNamesForLog(req.Tools), systemHasLeanSubagentMarker(req.System), in.FlattenToolResults, truncateRunes(strings.TrimSpace(req.System), 120), truncateRunes(strings.TrimSpace(in.UserInput), 120), len(req.Messages))
		rdr, err := in.Provider.StreamChat(ctx, req)
		if err != nil {
			return ToolLoopResult{}, err
		}
		resp, err := collectStream(ctx, rdr, in.OnTextDelta, noticeSink(in))
		rdr.Close()
		if err != nil {
			return ToolLoopResult{}, err
		}
		lastIn, lastOut = resp.InputTokens, resp.OutputTokens
		noteAssembledTurn(in.ConversationID, resp.Blocks, seenToolUse)

		var toolCalls []llm.Block
		var finalText string
		for _, b := range resp.Blocks {
			if b.Type == llm.BlockToolUse {
				toolCalls = append(toolCalls, b)
			}
			if b.Type == llm.BlockText {
				finalText += b.Text
			}
		}
		if len(toolCalls) > 0 && in.FlattenToolResults {
			persistTurn(llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})
			appendModelTurn(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: flattenToolUseSummary(toolCalls)}}})
		} else {
			appendTurn(llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})
		}
		log.Printf("[tool-loop] model response: conv=%s provider=%s model=%s iter=%d tool_calls=%d text_len=%d tokens_in=%d tokens_out=%d text_prefix=%q",
			in.ConversationID, in.Provider.Name(), in.Model, iter+1, len(toolCalls), len([]rune(finalText)), lastIn, lastOut, truncateRunes(strings.TrimSpace(finalText), 160))
		if len(toolCalls) == 0 {
			if in.WatchdogTurnEnd != nil && strings.TrimSpace(finalText) != "" {
				wd := in.WatchdogTurnEnd(ctx, finalText, hist)
				switch wd.Action {
				case "challenge", "block":
					revise := wd.Revise
					if revise == "" {
						revise = "Address the issue and revise your reply"
					}
					var note string
					if wd.Action == "block" {
						note = "Blocked — comply required. watchdog (" + wd.Protocol + "): " + wd.Challenge + ". " + revise + " (required — no override)."
					} else {
						note = "Challenge — comply or justify. watchdog (" + wd.Protocol + "): " + wd.Challenge + ". " + revise + ", or call `justify` with a reason."
					}
					emit(LoopEvent{Kind: LoopWatchdogChallenge, Detail: wd.Protocol, Summary: wd.Challenge})
					// The assistant turn was already appended above (line 201); append
					// only the revise user message so the assistant reply appears once.
					appendTurn(llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: note}}})
					continue
				case "escalate":
					// v1: give up gracefully — surface the escalation and return the
					// reply (a human-confirm for turn_end is a follow-on).
					emit(LoopEvent{Kind: LoopWatchdogEscalate, Detail: wd.Protocol, Summary: wd.Challenge})
				case "allow", "":
					// fall through to return
				}
			}
			return ToolLoopResult{
				FinalText: finalText, FinalBlocks: resp.Blocks,
				Iterations: iter + 1, History: hist,
				InputTokens: lastIn, OutputTokens: lastOut,
			}, nil
		}

		for _, tc := range toolCalls {
			emit(LoopEvent{Kind: LoopToolUseStart, ToolUseID: tc.ToolUseID, ToolName: tc.ToolName})
			emit(LoopEvent{Kind: LoopToolUseStop, ToolUseID: tc.ToolUseID, ToolName: tc.ToolName, ArgsJSON: string(tc.ToolInput)})
		}

		results := make([]llm.Block, 0, len(toolCalls))

		type pendingCall struct {
			block llm.Block
			tool  agenttools.Tool
			tier  llm.Permission
		}
		var rCalls, wxCalls []pendingCall
		for _, tc := range toolCalls {
			// A wrapped malformed input never reaches the tool: answer with
			// the raw text so the model can see exactly what it emitted.
			if raw, malformed := llm.MalformedToolInput(tc.ToolInput); malformed {
				// Distinguish a genuine authoring mistake from a size-limit
				// truncation. When the response was cut off at the output-token
				// cap (StopReason "length"/"max_tokens"), the arguments were
				// sliced off mid-JSON — the input is not "invalid", it is
				// incomplete because the payload was too large to emit in one
				// call. Telling the model to "resend valid JSON" here makes it
				// resend the same oversized call and truncate again (an infinite
				// retry). Instead, name the truncation and tell it to chunk.
				var content string
				if llm.IsLengthTruncation(resp.StopReason) {
					content = fmt.Sprintf("this tool call was cut off at the output-token limit — the arguments are incomplete because the payload was too large to emit in one call, not because the JSON was malformed. Do NOT resend the same call. If you are writing a file, split it into several smaller Write/Edit calls (write part, then append the rest with additional Edit calls). Raw (truncated) input received: %s", truncateForError(raw))
				} else {
					content = fmt.Sprintf("tool input was not valid JSON — resend the call with arguments as a single valid JSON object. Raw input received: %s", truncateForError(raw))
				}
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: content,
					IsError: true,
				})
				continue
			}
			tool, ok := in.Registry.Get(tc.ToolName)
			if !ok {
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: "unknown tool: " + tc.ToolName, IsError: true,
				})
				continue
			}
			perm := tool.Permission()
			if ap, ok := tool.(agenttools.ArgsPermissioner); ok {
				perm = ap.PermissionFor(tc.ToolInput)
			}
			tier := agenttools.PermissionToLLM(perm)
			// Enforcement half of the capability profile (see profile.go): the
			// fence. A tool the active profile forbids is denied outright here,
			// before it can be run (R path) or confirmed (W/X path) — there is
			// no "yes" available. This is not the same as the catalog filter
			// above: filtering only shapes what the model is offered; this
			// catches a forbidden tool that arrives by any other route
			// (hallucinated name, replayed turn, future code path).
			if !in.Profile.Allows(tier, tc.ToolName) {
				emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: tc.ToolUseID, ToolName: tc.ToolName, Summary: "blocked by " + in.Profile.Name + " profile", IsError: true})
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: fmt.Sprintf("blocked: the %q profile is read-only — the tool %q (%s) is unavailable. Only read and plan actions are permitted while planning.", in.Profile.Name, tc.ToolName, tier),
					IsError: true,
				})
				continue
			}
			pc := pendingCall{block: tc, tool: tool, tier: tier}
			if tier == llm.PermR {
				rCalls = append(rCalls, pc)
			} else {
				wxCalls = append(wxCalls, pc)
			}
		}

		type rr struct {
			idx int
			res llm.Block
		}
		rChan := make(chan rr, len(rCalls))
		for i, pc := range rCalls {
			go func(i int, pc pendingCall) {
				execCtx := agenttools.WithProgressEmitter(ctx, func(progress agenttools.ProgressEvent) {
					emit(loopProgressEvent(pc.block.ToolUseID, pc.block.ToolName, progress))
				})
				emit(LoopEvent{Kind: LoopToolExecStart, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName})
				res, err := pc.tool.Execute(execCtx, pc.block.ToolInput)
				out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
				if err != nil {
					out.Content = err.Error()
					out.IsError = true
					emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: err.Error(), IsError: true})
				} else {
					out.Content = res.LLMContent()
					out.StartLine = res.StartLine
					emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: summarizeResult(res), Detail: res.Detail, StartLine: res.StartLine, IsError: false})
				}
				rChan <- rr{idx: i, res: out}
			}(i, pc)
		}
		rResults := make([]llm.Block, len(rCalls))
		for range rCalls {
			r := <-rChan
			rResults[r.idx] = r.res
		}
		results = append(results, rResults...)

		watchdogIntervened := false
		for _, pc := range wxCalls {
			watchdogApproved := false
			if in.WatchdogGate != nil {
				wd := in.WatchdogGate(ctx, pc.block.ToolName, pc.block.ToolInput, hist)
				switch wd.Action {
				case "challenge":
					emit(LoopEvent{Kind: LoopWatchdogChallenge, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Tier: string(pc.tier), Detail: wd.Protocol, Summary: wd.Challenge})
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "Challenge — comply or justify. watchdog (" + wd.Protocol + "): " + wd.Challenge + " Follow the protocol first, or call `justify` with a reason to override.",
						IsError: false,
					})
					watchdogIntervened = true
					continue
				case "block":
					emit(LoopEvent{Kind: LoopWatchdogBlock, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Tier: string(pc.tier), Detail: wd.Protocol, Summary: wd.Challenge})
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "⚡ watchdog (" + wd.Protocol + "): " + wd.Challenge + " Blocked — follow the protocol first (no override available).",
						IsError: true,
					})
					watchdogIntervened = true
					continue
				case "escalate":
					emit(LoopEvent{Kind: LoopWatchdogEscalate, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Tier: string(pc.tier), Detail: wd.Protocol, Summary: wd.Challenge})
					if in.PermissionRequester == nil {
						results = append(results, llm.Block{
							Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
							Content: "⚡ watchdog escalation (" + wd.Protocol + "): no reviewer available — action not executed.",
							IsError: true,
						})
						watchdogIntervened = true
						continue
					}
					allow, err := in.PermissionRequester(ctx, pc.block.ToolUseID, pc.block.ToolName, pc.block.ToolInput, pc.tier, agenttools.IsDestructive(pc.tool))
					if err != nil {
						return ToolLoopResult{}, err
					}
					if !allow {
						results = append(results, llm.Block{
							Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
							Content: "watchdog escalation upheld by the user — action not executed.",
							IsError: true,
						})
						watchdogIntervened = true
						continue
					}
					// Human explicitly approved via the escalation prompt; skip the
					// normal permission gate below to avoid a second prompt.
					watchdogApproved = true
				case "allow", "":
					// fall through to the normal permission gate + execute
				}
			}
			// Read the permission mode per call (not once per turn) so a
			// mid-turn /strict|/permissive|/bypass change takes effect
			// immediately — consistent with the per-call allowlist read below
			// and with PermissionStore.Mode()'s "per gate decision" contract.
			mode := ModePermissive
			if in.Permissions != nil {
				mode = in.Permissions.Mode()
			}
			isMCP := agenttools.OriginOf(pc.tool) == agenttools.OriginMCP
			allowlisted := in.Permissions != nil && in.Permissions.IsMCPAllowed(pc.block.ToolName)
			if !watchdogApproved && GateDecisionForMCP(mode, pc.tier, isMCP, allowlisted) {
				if in.PermissionRequester == nil {
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "no permission requester wired", IsError: true,
					})
					appendTurn(llm.Message{Role: llm.RoleUser, Blocks: results})
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, nil
				}
				destructive := agenttools.IsDestructive(pc.tool)
				emit(LoopEvent{Kind: LoopPermissionRequired, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, ArgsJSON: string(pc.block.ToolInput), Tier: string(pc.tier), Destructive: destructive})
				allow, err := in.PermissionRequester(ctx, pc.block.ToolUseID, pc.block.ToolName, pc.block.ToolInput, pc.tier, destructive)
				var followUp *FollowUpDenial
				if errors.As(err, &followUp) {
					// "Chat about this": the user declined the tool but sent a redirect.
					// Record it as this call's tool_result and CONTINUE the turn so the
					// model responds to the redirect inline rather than on a fresh turn.
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: followUp.Message, IsError: true,
					})
					continue
				}
				if err != nil {
					return ToolLoopResult{}, err
				}
				if !allow {
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "user denied execution", IsError: true,
					})
					appendTurn(llm.Message{Role: llm.RoleUser, Blocks: results})
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, nil
				}
			}
			execCtx := agenttools.WithProgressEmitter(ctx, func(progress agenttools.ProgressEvent) {
				emit(loopProgressEvent(pc.block.ToolUseID, pc.block.ToolName, progress))
			})
			emit(LoopEvent{Kind: LoopToolExecStart, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName})
			res, err := pc.tool.Execute(execCtx, pc.block.ToolInput)
			out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
			if err != nil {
				out.Content = err.Error()
				out.IsError = true
				emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: err.Error(), IsError: true})
			} else {
				out.Content = res.LLMContent()
				out.StartLine = res.StartLine
				emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: summarizeResult(res), Detail: res.Detail, StartLine: res.StartLine, IsError: false})
			}
			results = append(results, out)
		}

		allErrored := true
		for _, r := range results {
			if !r.IsError {
				allErrored = false
				break
			}
		}
		if in.FlattenToolResults {
			persistTurn(llm.Message{Role: llm.RoleUser, Blocks: results})
			appendModelTurn(llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: flattenToolResultsForModel(toolCalls, results)}}})
		} else {
			appendTurn(llm.Message{Role: llm.RoleUser, Blocks: results})
		}

		switch {
		case watchdogIntervened:
			// Watchdog challenges/blocks/escalations are deliberate supervisory
			// refusals, not tool malfunctions — they must not feed the
			// consecutive-error abort (else strict-mode blocking could kill a turn).
		case allErrored:
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				return ToolLoopResult{
					FinalText: finalText, Iterations: iter + 1, History: hist,
					InputTokens: lastIn, OutputTokens: lastOut,
				}, fmt.Errorf("aborted: 3 consecutive iterations of tool errors")
			}
		default:
			consecutiveErrors = 0
		}
	}
	// Reached the iteration cap with tools still pending. Rather than erroring
	// out and discarding everything the model gathered, make one final pass with
	// no tools so it answers from what it has. Text deltas stream as usual.
	hist = append(hist, llm.Message{
		Role: llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: fmt.Sprintf(
			"You've reached the %d-step tool limit for this turn. Stop calling tools and give your best answer now using what you've gathered.",
			maxIters)}},
	})
	finalReq := llm.ChatRequest{Model: in.Model, Tier: in.Tier, System: in.System, Messages: hist, MaxTokens: maxTokens, Temperature: in.Temperature}
	rdr, err := in.Provider.StreamChat(ctx, finalReq)
	if err != nil {
		return ToolLoopResult{Iterations: maxIters, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, err
	}
	resp, err := collectStream(ctx, rdr, in.OnTextDelta, noticeSink(in))
	rdr.Close()
	if err != nil {
		return ToolLoopResult{Iterations: maxIters, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, err
	}
	var finalText string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			finalText += b.Text
		}
	}
	noteAssembledTurn(in.ConversationID, resp.Blocks, seenToolUse)
	hist = append(hist, llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})
	return ToolLoopResult{
		FinalText: finalText, FinalBlocks: resp.Blocks,
		Iterations: maxIters, History: hist,
		InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
	}, nil
}

// noteAssembledTurn scans a freshly collected model turn for the resume-replay
// fingerprint: a tool_use id already emitted earlier in this conversation. A
// well-formed stream never reuses a tool_use id across turns, so a duplicate
// means the turn replays prior content (a fabricated turn well-formed enough to
// pass the framing guard). Each hit is recorded to the shared anomaly log with
// the conversation id and the replayed call. Best-effort; never blocks the loop.
func noteAssembledTurn(conversationID string, blocks []llm.Block, seen map[string]bool) {
	for _, b := range blocks {
		if b.Type != llm.BlockToolUse || b.ToolUseID == "" {
			continue
		}
		if seen[b.ToolUseID] {
			preview := string(b.ToolInput)
			if len(preview) > 200 {
				preview = preview[:200] + "\u2026"
			}
			llm.RecordAnomaly(conversationID, "replayed_tool_use",
				fmt.Sprintf("tool_use id=%s name=%s re-emitted in a later turn (replay fingerprint); input=%s",
					b.ToolUseID, b.ToolName, preview))
			continue
		}
		seen[b.ToolUseID] = true
	}
}

// truncateForError bounds raw model output quoted inside an error message.
func truncateForError(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (truncated)"
}

// collectStream delegates to llm.CollectStream, which owns the stream->
// ChatResponse aggregation (moved to the llm package so provider adapters
// can reuse it — the codex backend requires streaming). Thin wrapper so the
// loop call sites stay unchanged.
func collectStream(ctx context.Context, rdr llm.StreamReader, onText func(string), onNotice func(string)) (llm.ChatResponse, error) {
	return llm.CollectStream(ctx, rdr, onText, onNotice)
}

// noticeSink adapts the loop's event sink into the CollectStream notice
// callback, forwarding resilience-engine narration ("openai server busy —
// trying once more") to the client as a display-only loop event.
func noticeSink(in ToolLoopInput) func(string) {
	if in.EventSink == nil {
		return nil
	}
	return func(text string) {
		in.EventSink(LoopEvent{Kind: LoopNotice, Summary: text})
	}
}
