package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/protocols"
	"cercano/source/server/internal/watchdog"
)

// Core implements TurnRunner. It holds no process-global mutable state, so
// many Cores may run concurrently (embedded mode) or one per worker process
// (Phase 5) — identical code path either way.
type Core struct{ d Deps }

// New constructs a Core from the injected service dependencies.
func New(d Deps) *Core { return &Core{d: d} }

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

	// 1. Resolve the provider per the active Locus Mode.
	provider, isCloud, fellBack, err := c.d.Providers.Main()
	if err != nil {
		// *_only mode with its required tier unavailable — return a synthetic
		// result so the host can send a terminal FinalResponse.
		return Result{FinalText: "Locus: " + err.Error()}, nil
	}
	if fellBack {
		sink.Emit(Event{
			Kind: EventProgress,
			Text: fmt.Sprintf("⚠ preferred tier unavailable — falling back to %s (%s)",
				provider.Name(), c.d.Providers.MainModel(isCloud)),
		})
	}

	// Announce the route so the client shows the correct engine badge.
	sink.Emit(Event{
		Kind:    EventRouteSelected,
		Model:   c.d.Providers.MainModel(isCloud),
		IsCloud: isCloud,
	})

	// 2. Assemble conversation history.
	var convHistory []llm.Message
	if c.d.Persist != nil && req.ConversationID != "" {
		convHistory = c.d.Persist.AssembleHistory(ctx, req.ConversationID)
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

	// 5. Internal adapter: agent.LoopEvent → runner.Event, forwarded to sink.
	loopSink := makeLoopSink(sink)

	// 6. Build the permission store for the loop.
	var permStore *agent.PermissionStore
	if c.d.Perms != nil {
		permStore = c.d.Perms.Store()
	}

	// 6b. Read the active capability profile (read-only planning fence / future
	// modes). Live accessor: read at turn time so a mid-session mode switch
	// takes effect on the next turn. Nil accessor = unrestricted (zero Profile).
	var profile agent.Profile
	if c.d.Profiles != nil {
		profile = c.d.Profiles()
	}

	// Run the tool loop on the primary provider.
	result, loopErr := c.runLoop(ctx, req, provider, isCloud,
		loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile)

	// 6.5. Same-provider turn retry: a busy-class loop error is a transient
	// server-side failure — often a mid-stream one, where the resilience
	// engine deliberately cannot re-serve (content already flowed). At the
	// turn level a full re-run IS safe: the failed iteration's partial output
	// was never persisted, and the re-run supersedes it — the same contract
	// the cross-tier fallback below has always relied on. One narrated
	// attempt on the same provider before any tier change.
	if loopErr != nil && ctx.Err() == nil && llm.ClassOf(loopErr) == llm.ErrBusy {
		notice := fmt.Sprintf("⚠ %s server busy — trying once more", provider.Name())
		fmt.Fprintf(os.Stderr, "[resilience] turn retry: %s (%v)\n", provider.Name(), loopErr)
		sink.Emit(Event{Kind: EventProgress, Text: notice})
		result, loopErr = c.runLoop(ctx, req, provider, isCloud,
			loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile)
	}

	// 7. Cross-tier fallback: on error, attempt the other tier if locus allows.
	var fallbackNotice string
	if loopErr != nil {
		mode, _ := locus.ParseMode(c.d.Config.Get().LocusMode)
		res := mode.Main()
		fbProv := c.d.Providers.Cloud()
		fbCloud := true
		if res.Fallback == locus.TierLocal {
			fbProv, fbCloud = c.d.Providers.Open(), false
		}
		if !fellBack && res.CrossAllowed && fbProv != nil {
			fallbackNotice = fmt.Sprintf("⚠ %s failed (%v) — retrying on %s", provider.Name(), loopErr, fbProv.Name())
			sink.Emit(Event{Kind: EventProgress, Text: fallbackNotice})
			sink.Emit(Event{
				Kind:    EventRouteSelected,
				Model:   c.d.Providers.MainModel(fbCloud),
				IsCloud: fbCloud,
			})
			provider = fbProv
			isCloud = fbCloud
			result, loopErr = c.runLoop(ctx, req, fbProv, fbCloud,
				loopSink, requester, convHistory, onTextDelta, onTurn, wdGate, wdTurnEnd, gateRegistry, permStore, profile)
		}
		if loopErr != nil {
			return Result{}, fmt.Errorf("tool loop error: %w", loopErr)
		}
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
	c.d.Agent.RecordContextUsage(req.ConversationID, c.d.Providers.MainModel(isCloud),
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
) (agent.ToolLoopResult, error) {
	maxIterations := 0
	if c.d.Config != nil {
		maxIterations = c.d.Config.Get().ToolLoop.MaxIterations
	}

	return agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            provider,
		Registry:            gateRegistry,
		Permissions:         permStore,
		Profile:             profile,
		UserInput:           req.Input,
		Images:              req.Images,
		Model:               c.d.Providers.MainModel(isCloud),
		System:              BuildSystemPrompt(c.d, req.WorkDir),
		WorkDir:             req.WorkDir,
		ConversationID:      req.ConversationID,
		EventSink:           loopSink,
		PermissionRequester: requester,
		ConvHistory:         convHistory,
		OnTextDelta:         onTextDelta,
		OnTurnComplete:      onTurn,
		MaxIterations:       maxIterations,
		WatchdogGate:        wdGate,
		WatchdogTurnEnd:     wdTurnEnd,
	})
}

// makeLoopSink builds the agent.LoopEvent → runner.Event adapter.
// It is the single point that maps the tool-loop's internal event vocabulary
// to the runner's proto-free Event type. The host's protoSink then maps
// runner.Event → stream.Send(proto...).
func makeLoopSink(sink EventSink) func(agent.LoopEvent) {
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
			sink.Emit(Event{
				Kind:      EventToolExecStart,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
			})

		case agent.LoopToolExecComplete:
			fmt.Fprintf(os.Stderr, "[tool-loop]   -> %s (err=%v) %s\n", ev.Summary, ev.IsError, ev.Detail)
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
			// shows it as the live status line.
			fmt.Fprintf(os.Stderr, "[resilience] %s\n", ev.Summary)
			sink.Emit(Event{Kind: EventProgress, Text: "⚠ " + ev.Summary})

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
func BuildSystemPrompt(d Deps, workDir string) string {
	env := loopEnv{
		WorkDir:  workDir,
		Platform: runtime.GOOS,
		Date:     time.Now().Format("2006-01-02"),
	}
	if workDir != "" {
		env.GitRepo, env.GitBranch = runnerGitInfo(workDir)
	}
	steering := protocols.SteeringBlock(protocols.ForDomain(protocols.DomainCore))
	projectCtx := ""
	if d.Persist != nil {
		projectCtx = d.Persist.LoadProjectContext(workDir)
	}
	return buildToolLoopSystem(env, steering, runnerDirectorySnapshot(workDir, 80), projectCtx)
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
