# Watchdog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A protocol-enforcement supervisor: a stateful `internal/watchdog` package whose `Gate` runs pluggable checks (v1: debug-loop) via a fast one-shot model, and challenges the main model to comply-or-justify — wired into `agent.RunToolLoop` as a second, mode-independent gate.

**Architecture:** `internal/watchdog` holds pure supervision logic (Check registry, verdict parsing, the intervention state machine, the `justify` tool). It calls an injected `OneShotFunc` (the dispatch engine's OneShot at a hardcoded "fast" model) for model-backed checks. `RunToolLoop` gains an optional `WatchdogGate` callback that fires per W/X tool call; on `challenge` it injects a synthetic tool result instead of executing, so the main model re-plans. The server constructs the watchdog and wires the callback + the `justify` tool.

**Tech Stack:** Go 1.26; `internal/agent` (RunToolLoop/ToolLoopInput/LoopEvent), `internal/dispatch` (Engine.Dispatch/Spec/OneShot), `internal/agenttools` (Tool/Registry), `internal/llm` (Message/Block), `internal/protocols` (0b protocol bodies).

## Global Constraints

- **Fail-open — the watchdog must never wedge the main loop.** Any watchdog error (model unreachable, verdict unparseable, panic) results in `allow`; log a warning. Supervision degrading always beats blocking real work.
- **Default off.** The watchdog is inert unless `enabled` in config. When disabled, `RunToolLoop` behaves exactly as today (the `WatchdogGate` callback is nil).
- **Orthogonal to permissions.** The watchdog gate runs independent of R/W/X permission mode (it supervises even under `bypass`). It does NOT replace the permission gate — an `allow` from the watchdog still flows into the normal permission gate.
- **Pure package.** `internal/watchdog` imports no server/mcp packages and holds no model client — it calls an injected `OneShotFunc`. It may import `internal/agenttools`, `internal/llm`, `internal/protocols`.
- **The `justify` tool never executes work** — it only records an override into watchdog state and returns an acknowledgment.
- Commit messages must not contain the word "Claude"; no `Co-Authored-By` trailer.
- `go test ./...` green and `gofmt` clean in `source/server` after every task.

---

## File Structure

- `internal/watchdog/watchdog.go` — `Watchdog` type (config, registry, oneShot, per-conversation state), `Gate`, `Decision`.
- `internal/watchdog/verdict.go` — `Verdict{Violation,Protocol,Challenge}` + `parseVerdict`.
- `internal/watchdog/check.go` — `Action`, `Check` interface, `OneShotFunc`.
- `internal/watchdog/debugloop.go` — the v1 `debug-loop` check.
- `internal/watchdog/justify.go` — the `justify` agenttools.Tool bound to a Watchdog.
- `internal/agent/toolloop.go` — add `WatchdogGate` to `ToolLoopInput`; insert the gate in the wxCalls loop; add echo events.
- `internal/agent/watchdog_seam.go` — the `WatchdogGate`/`WatchdogDecision` types (in package agent so toolloop can use them without importing watchdog).
- `internal/server/watchdog_wire.go` — build the Watchdog from config + a dispatch OneShot handle; provide the gate + justify tool.
- `pkg/config/config.go` — a `Watchdog` config block.

---

## Phase 1 — the watchdog package (pure, model-stubbable)

### Task 1: Verdict + parse

**Files:**
- Create: `source/server/internal/watchdog/verdict.go`, `verdict_test.go`

**Interfaces:**
- Produces: `type Verdict struct { Violation bool; Protocol string; Challenge string }`; `func parseVerdict(protocol, modelText string) Verdict`.
- Parses the fast model's completion. Convention: the model emits `VIOLATION: yes|no` and `CHALLENGE: <one line>`. Conservative default: if VIOLATION is absent/ambiguous → **no violation** (fail-open — don't challenge on garbage). `Protocol` is passed in (the check knows it).

- [ ] **Step 1: Write the failing test**

```go
package watchdog

import "testing"

func TestParseVerdict(t *testing.T) {
	v := parseVerdict("debug-loop", "VIOLATION: yes\nCHALLENGE: You're editing to fix a bug with no debug evidence.")
	if !v.Violation || v.Protocol != "debug-loop" || v.Challenge == "" {
		t.Fatalf("expected a violation with a challenge, got %+v", v)
	}
	if parseVerdict("debug-loop", "VIOLATION: no").Violation {
		t.Fatal("VIOLATION: no must be no violation")
	}
	// Fail-open on garbage: no clear violation → no challenge.
	if parseVerdict("debug-loop", "the model rambled without a verdict").Violation {
		t.Fatal("ambiguous output must default to no violation")
	}
}
```

- [ ] **Step 2: Run; fail.** `cd source/server && go test ./internal/watchdog/ -run TestParseVerdict -v` — package undefined.

- [ ] **Step 3: Implement**

```go
package watchdog

import "strings"

// Verdict is a check's judgment of a proposed action.
type Verdict struct {
	Violation bool
	Protocol  string
	Challenge string
}

// parseVerdict reads a fast-model completion of the form
// "VIOLATION: yes|no\nCHALLENGE: <one line>". It is conservative: only an
// explicit affirmative counts as a violation (so ambiguous/garbage output
// fails open — no challenge).
func parseVerdict(protocol, modelText string) Verdict {
	v := Verdict{Protocol: protocol}
	for _, line := range strings.Split(modelText, "\n") {
		l := strings.TrimSpace(line)
		low := strings.ToLower(l)
		switch {
		case strings.HasPrefix(low, "violation:"):
			val := strings.TrimSpace(low[len("violation:"):])
			v.Violation = val == "yes" || val == "true"
		case strings.HasPrefix(low, "challenge:"):
			v.Challenge = strings.TrimSpace(l[len("challenge:"):])
		}
	}
	if v.Violation && v.Challenge == "" {
		v.Challenge = "You appear to be skipping the " + protocol + " protocol — comply or justify."
	}
	if !v.Violation {
		v.Challenge = ""
	}
	return v
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> add source/server/internal/watchdog/verdict.go source/server/internal/watchdog/verdict_test.go
git -C <worktree> commit -m "feat(watchdog): Verdict + conservative parseVerdict"
```

### Task 2: Action + Check interface

**Files:**
- Create: `source/server/internal/watchdog/check.go`, `check_test.go`

**Interfaces:**
- Produces:
  ```go
  type Action struct {
      Kind       string          // "tool_call" | "turn_end"
      ToolName   string
      ToolArgs   json.RawMessage
      Text       string
      Transcript []llm.Message
  }
  type OneShotFunc func(ctx context.Context, prompt string) (string, error)
  type Check interface {
      Name() string
      Applies(a Action) bool
      Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error)
  }
  ```

- [ ] **Step 1: Write the failing test** (a fake check exercising the interface)

```go
package watchdog

import (
	"context"
	"testing"
)

type fakeCheck struct{ applies bool; verdict Verdict }

func (fakeCheck) Name() string                 { return "fake" }
func (f fakeCheck) Applies(a Action) bool       { return f.applies }
func (f fakeCheck) Evaluate(_ context.Context, _ Action, _ OneShotFunc) (Verdict, error) {
	return f.verdict, nil
}

func TestCheckInterface(t *testing.T) {
	var c Check = fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "fake", Challenge: "x"}}
	if !c.Applies(Action{Kind: "tool_call"}) {
		t.Fatal("Applies should be true")
	}
	v, err := c.Evaluate(context.Background(), Action{}, nil)
	if err != nil || !v.Violation {
		t.Fatalf("Evaluate: %+v %v", v, err)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** `check.go` with the types above (import `context`, `encoding/json`, `cercano/source/server/internal/llm`).

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(watchdog): Action + Check interface + OneShotFunc"
```

### Task 3: the debug-loop check

**Files:**
- Create: `source/server/internal/watchdog/debugloop.go`, `debugloop_test.go`

**Interfaces:**
- Produces: `func DebugLoopCheck() Check`. `Applies` = tool_call with ToolName in {`edit_file`,`write_file`,`rm_file`,`git_reset_hard`} (the W/X edit/destructive set). `Evaluate` builds a prompt from the transcript + the proposed edit and calls `oneShot`, parsing the result via `parseVerdict("debug-loop", ...)`.

- [ ] **Step 1: Write the failing test** (stubbed oneShot; assert Applies + that a "VIOLATION: yes" completion yields a violation, and the prompt embeds the tool name)

```go
package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestDebugLoopApplies(t *testing.T) {
	c := DebugLoopCheck()
	if !c.Applies(Action{Kind: "tool_call", ToolName: "edit_file"}) {
		t.Fatal("edit_file should apply")
	}
	if c.Applies(Action{Kind: "tool_call", ToolName: "read_file"}) {
		t.Fatal("read_file must not apply")
	}
	if c.Applies(Action{Kind: "turn_end"}) {
		t.Fatal("turn_end must not apply to debug-loop")
	}
}

func TestDebugLoopEvaluate(t *testing.T) {
	var gotPrompt string
	oneShot := func(_ context.Context, prompt string) (string, error) {
		gotPrompt = prompt
		return "VIOLATION: yes\nCHALLENGE: no debug evidence", nil
	}
	v, err := DebugLoopCheck().Evaluate(context.Background(),
		Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"x.go"}`)}, oneShot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "debug-loop" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, "edit_file") {
		t.Fatalf("prompt should reference the proposed action: %q", gotPrompt)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package watchdog

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

type debugLoopCheck struct{}

// DebugLoopCheck flags edits made to fix a bug/test failure with no evidence of
// the systematic-debugging loop in the recent transcript.
func DebugLoopCheck() Check { return debugLoopCheck{} }

func (debugLoopCheck) Name() string { return "debug-loop" }

var editTools = map[string]bool{"edit_file": true, "write_file": true, "rm_file": true, "git_reset_hard": true}

func (debugLoopCheck) Applies(a Action) bool {
	return a.Kind == "tool_call" && editTools[a.ToolName]
}

func (debugLoopCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "debug-loop"}, nil // no model → fail open
	}
	prompt := buildDebugLoopPrompt(a)
	out, err := oneShot(ctx, prompt)
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("debug-loop", out), nil
}

func buildDebugLoopPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing the systematic-debugging protocol.\n")
	b.WriteString("The agent is about to run a code-mutating tool. Judge ONLY whether it is fixing a bug or test failure WITHOUT evidence in the recent transcript of the debug loop: reducing to the smallest failing case, observing actual data/output, and confirming the root cause with a probe. Refactors, new features, and edits with clear prior evidence are NOT violations.\n\n")
	fmt.Fprintf(&b, "Proposed action: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Recent transcript:\n")
	b.WriteString(transcriptTail(a.Transcript, 12))
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

// transcriptTail renders the last n messages as plain text for the prompt.
func transcriptTail(msgs []llm.Message, n int) string {
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			if blk.Type == llm.BlockText && strings.TrimSpace(blk.Text) != "" {
				fmt.Fprintf(&b, "[%s] %s\n", m.Role, strings.TrimSpace(blk.Text))
			}
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(watchdog): debug-loop check (Applies edit tools + model prompt/verdict)"
```

### Task 4: the Watchdog object + Gate + state

**Files:**
- Create: `source/server/internal/watchdog/watchdog.go`, `watchdog_test.go`

**Interfaces:**
- Produces:
  ```go
  type Mode string
  const ( ModeChallenge Mode = "challenge-and-justify"; ModeStrict Mode = "strict" )
  type Decision struct {
      Action    string // "allow" | "challenge" | "block" | "escalate"
      Protocol  string
      Challenge string
  }
  type Config struct { Mode Mode; EscalateAfter int } // EscalateAfter 0 → default 2
  func New(cfg Config, checks []Check, oneShot OneShotFunc) *Watchdog
  func (w *Watchdog) Gate(ctx context.Context, conversationID string, a Action) Decision
  func (w *Watchdog) recordJustify(conversationID, key string) // used by the justify tool
  ```
- State keyed per conversation: a set of justified/satisfied `key`s (don't re-challenge) and a repeat counter per `key`. `key = protocol + "|" + toolName + "|" + sha256(toolArgs)[:12]`.
- `Gate` logic: run each applicable check; on the first `Violation`: compute `key`; if already justified → `allow`; else increment the counter; if counter ≥ EscalateAfter → `escalate`; else in `ModeStrict` → `block`, in `ModeChallenge` → `challenge`. No violation → `allow`. **Any check error → allow (fail-open).**

- [ ] **Step 1: Write the failing test** (drive the state machine with fake checks/stubbed oneShot)

```go
package watchdog

import (
	"context"
	"testing"
)

func editAction() Action { return Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"x"}`)} }

func TestGateChallengeThenEscalate(t *testing.T) {
	w := New(Config{Mode: ModeChallenge, EscalateAfter: 2}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}}, nil)
	d1 := w.Gate(context.Background(), "conv", editAction())
	if d1.Action != "challenge" || d1.Challenge != "c" {
		t.Fatalf("first: %+v", d1)
	}
	d2 := w.Gate(context.Background(), "conv", editAction()) // same action repeats → hits threshold
	if d2.Action != "escalate" {
		t.Fatalf("second (repeat) should escalate, got %+v", d2)
	}
}

func TestGateJustifyAllows(t *testing.T) {
	w := New(Config{Mode: ModeChallenge}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}}, nil)
	a := editAction()
	if w.Gate(context.Background(), "conv", a).Action != "challenge" {
		t.Fatal("expected challenge")
	}
	w.recordJustify("conv", keyFor("debug-loop", a))
	if w.Gate(context.Background(), "conv", a).Action != "allow" {
		t.Fatal("after justify, the same action must be allowed")
	}
}

func TestGateStrictBlocks(t *testing.T) {
	w := New(Config{Mode: ModeStrict}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}}, nil)
	if w.Gate(context.Background(), "conv", editAction()).Action != "block" {
		t.Fatal("strict mode must block")
	}
}

func TestGateFailsOpenOnCheckError(t *testing.T) {
	w := New(Config{Mode: ModeChallenge}, []Check{errCheck{}}, nil)
	if w.Gate(context.Background(), "conv", editAction()).Action != "allow" {
		t.Fatal("a check error must fail open (allow)")
	}
}

type errCheck struct{}
func (errCheck) Name() string           { return "err" }
func (errCheck) Applies(Action) bool     { return true }
func (errCheck) Evaluate(context.Context, Action, OneShotFunc) (Verdict, error) { return Verdict{}, context.DeadlineExceeded }
```

- [ ] **Step 2: Run; fail. Step 3: Implement** `watchdog.go` with the `Watchdog` struct (a `sync.Mutex`-guarded map `conversationID → *convState{ justified map[string]bool; counts map[string]int }`), `New`, `Gate` (per the logic above; `keyFor(protocol, a)` exported-lowercase helper computing the sha256 key), `recordJustify`. Fail-open: wrap each `check.Evaluate` and on error `continue` (treat as no violation).

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(watchdog): Watchdog Gate + per-conversation state (challenge/block/escalate/justify)"
```

### Task 5: the `justify` tool

**Files:**
- Create: `source/server/internal/watchdog/justify.go`, `justify_test.go`

**Interfaces:**
- Consumes: `*Watchdog`, `agenttools.Tool`.
- Produces: `func (w *Watchdog) JustifyTool(conversationID string) agenttools.Tool`. An R-tier tool named `justify`, schema `{reason: string, tool: string, args?: string}`. `Execute` computes the key from `tool`+`args`+the pending protocol and calls `w.recordJustify`, returning an acknowledgment. It performs no work.
  - Simplify: the tool records an override for the CURRENT pending challenge on this conversation. Watchdog tracks the last-challenged key per conversation; `justify` records THAT key (so the model needn't echo the args). Add `lastChallenged map[conversationID]string` to state, set in `Gate` when returning `challenge`, consumed by `justify`.

- [ ] **Step 1: Write the failing test** — after a Gate `challenge`, invoking the justify tool's Execute records the override so the next Gate on the same action returns `allow`.

```go
package watchdog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestJustifyToolRecordsOverride(t *testing.T) {
	w := New(Config{Mode: ModeChallenge}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "c"}}}, nil)
	a := editAction()
	if w.Gate(context.Background(), "conv", a).Action != "challenge" {
		t.Fatal("expected challenge")
	}
	tool := w.JustifyTool("conv")
	if tool.Name() != "justify" {
		t.Fatalf("tool name: %s", tool.Name())
	}
	args, _ := json.Marshal(map[string]any{"reason": "obvious typo"})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if w.Gate(context.Background(), "conv", a).Action != "allow" {
		t.Fatal("after justify tool, the action must be allowed")
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** — add `lastChallenged` to `convState` (set in `Gate` on `challenge`/`escalate`), implement `JustifyTool` returning an `agenttools.Tool` (match the agenttools.Tool interface — read `internal/agenttools/tool.go` for the exact interface: Name/Description/Schema/Permission/Execute). `Execute` unmarshals `{reason}`, calls `w.recordJustify(conversationID, w.lastChallengedKey(conversationID))`, logs the override reason, returns an `agenttools.Result` acknowledgment. Permission R.

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(watchdog): justify tool records the pending override into watchdog state"
```

---

## Phase 2 — RunToolLoop integration

### Task 6: the `WatchdogGate` seam + wxCalls insertion

**Files:**
- Create: `source/server/internal/agent/watchdog_seam.go`
- Modify: `source/server/internal/agent/toolloop.go` (ToolLoopInput + the wxCalls loop ~line 268)
- Test: `source/server/internal/agent/watchdog_loop_test.go`

**Interfaces:**
- Produces (in package `agent`, so toolloop needn't import `watchdog`):
  ```go
  type WatchdogDecision struct { Action, Protocol, Challenge string } // Action: allow|challenge|block|escalate
  // WatchdogGate is called before a W/X tool executes. transcript is the history so far.
  type WatchdogGate func(ctx context.Context, toolName string, args json.RawMessage, transcript []llm.Message) WatchdogDecision
  ```
  and `ToolLoopInput` gains `WatchdogGate WatchdogGate` (nil = disabled).
- Behavior in the wxCalls loop, at the TOP of `for _, pc := range wxCalls` (before the permission-mode read): if `in.WatchdogGate != nil` AND `pc.tier` is W or X, call it. On:
  - `"allow"` / (nil gate) → fall through to the existing permission gate + execute (unchanged).
  - `"challenge"` → append a `BlockToolResult{ToolUseRef: pc.block.ToolUseID, Content: "⚡ watchdog (" + protocol + "): " + challenge + " Either follow the protocol first, or call `justify` with a reason to override.", IsError: false}`; emit a `LoopWatchdogChallenge` event; **do NOT execute the tool**; `continue` to the next wxCall.
  - `"block"` → same as challenge but Content says "blocked — follow the protocol first (no override)"; no justify mention.
  - `"escalate"` → surface to the human by calling `in.PermissionRequester` with a watchdog reason if set (reuse the confirm path): if the requester allows, fall through to execute; if it denies, append a "user upheld watchdog" error result and continue. If no requester, append the escalate note and continue (fail-safe: don't execute).

- [ ] **Step 1: Write the failing test** — drive `RunToolLoop` with a fake provider that (turn 1) proposes an `edit_file`, and a `WatchdogGate` stub returning `challenge`; assert the tool was NOT executed and the injected result contains "watchdog". Then a second script where the gate returns `allow` → the tool executes. Use the existing toolloop test harness (fake provider + registry + a stub edit tool that records execution) — read an existing `toolloop_*_test.go` for the pattern; do not invent a new harness.

- [ ] **Step 2: Run; fail. Step 3: Implement** the seam file + the wxCalls insertion. Keep the change surgical: a single `if in.WatchdogGate != nil { ... }` block at the top of the wxCall iteration that can `continue` (skip execution) or fall through. Add the `LoopWatchdogChallenge`/`LoopWatchdogEscalate` LoopEventKind constants.

- [ ] **Step 4: Run; pass (+ existing toolloop tests unchanged when WatchdogGate is nil). Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(agent): WatchdogGate seam in RunToolLoop (challenge/block/escalate on W/X calls)"
```

### Task 7: echo events (labeled watchdog/main threads)

**Files:**
- Modify: `source/server/internal/watchdog/watchdog.go` (emit callback) + `internal/agent/toolloop.go` (echo the main side)
- Test: `source/server/internal/watchdog/echo_test.go`

**Interfaces:**
- The `Watchdog` gains an optional `SetEcho(emit func(thread, text string))` (thread = "watchdog"|"main"). When set, `Gate` emits `("watchdog", "<pre-filter hit + verdict + challenge>")` on a violation. The `justify` tool emits `("main", "justify: <reason>")`. RunToolLoop, when echo is active, emits the main model's post-challenge response through the same sink (via the existing EventSink → the server maps it to scrollback).
- Keep it simple: the emit func is nil-safe; when nil (echo off) nothing is emitted.

- [ ] **Steps 1–5 (TDD):** stub the emit func; assert a violation emits a `"watchdog"`-thread line containing the challenge, and the justify tool emits a `"main"`-thread line with the reason. Wire the server side in Task 9. Commit.

```bash
git -C <worktree> commit -am "feat(watchdog): echo hook emits labeled watchdog/main thread lines"
```

---

## Phase 3 — config + server wiring

### Task 8: config block

**Files:**
- Modify: `source/server/pkg/config/config.go` (+ its test)

**Interfaces:**
- Produces: a `Watchdog` struct on `Config`:
  ```go
  Watchdog struct {
      Enabled       bool     `yaml:"enabled"`
      Mode          string   `yaml:"mode"`           // challenge-and-justify | strict
      Checks        []string `yaml:"checks"`         // e.g. ["debug-loop"]
      Model         string   `yaml:"model"`          // fast model; "" → hardcoded default
      EscalateAfter int      `yaml:"escalate_after"` // 0 → 2
      Echo          bool     `yaml:"echo"`
  } `yaml:"watchdog"`
  ```
- Defaults (in `config.Defaults()`): `Enabled:false`, `Mode:"challenge-and-justify"`, `Checks:["debug-loop"]`, `EscalateAfter:2`, `Echo:false`, `Model:""`.

- [ ] **Steps 1–5 (TDD):** test the block parses from yaml + defaults applied; commit.

```bash
git -C <worktree> commit -am "feat(config): watchdog config block (enabled/mode/checks/model/escalate_after/echo)"
```

### Task 9: server wiring (construct + inject + fast model)

**Files:**
- Create: `source/server/internal/server/watchdog_wire.go`
- Modify: `source/server/internal/server/server.go` (build the watchdog; pass its gate into the ToolLoopInput where `RunToolLoop` is invoked; register the justify tool when enabled)
- Modify: `cmd/cercano/main.go` if the server needs the config passed (it already holds `currentConfig`)
- Test: `source/server/internal/server/watchdog_wire_test.go`

**Interfaces:**
- Consumes: `watchdog.New`, `watchdog.DebugLoopCheck`, `s.dispatchEngine` (the coproc engine — its `OneShot` at a hardcoded fast model), `s.currentConfig.Watchdog`.
- Produces: `func (s *Server) buildWatchdog() *watchdog.Watchdog` — when `cfg.Watchdog.Enabled`, constructs the watchdog with the configured checks and a `OneShotFunc` that calls `s.dispatchEngine.Dispatch(ctx, dispatch.Spec{Mode: OneShot, Role: RoleCoproc, ModelOverride: fastModel, Prompt: prompt})` and returns `res.Text`. `fastModel = cfg.Watchdog.Model` or the hardcoded default (a small model constant, e.g. the configured local model, or a `defaultWatchdogModel` const — pick the smallest reliably-present local model; document the choice). Returns nil when disabled.
- In the server's `RunToolLoop` call site (the streaming handler), set `ToolLoopInput.WatchdogGate = wd.Gate`-adapter (a closure adapting `agent.WatchdogGate` → `wd.Gate`, converting `WatchdogDecision`↔`watchdog.Decision`, threading the conversation id), and add `wd.JustifyTool(convID)` to the registry when the watchdog is enabled. Wire echo → the server's scrollback event path when `cfg.Watchdog.Echo`.

- [ ] **Steps 1–5 (TDD):** test `buildWatchdog` returns nil when disabled and a non-nil watchdog (with a working Gate over a stubbed dispatch) when enabled; the gate adapter converts decisions correctly. Build + full suite. Commit.

```bash
git -C <worktree> commit -am "feat(server): construct + wire the watchdog (fast-model OneShot, gate, justify tool, echo)"
```

---

## Deferred / follow-ons (not this plan)

- Additional checks: `plain-english` (turn_end), `decision-protocol`, `checkpoint` (deterministic) — each implements `Check` + registers; no machinery change.
- The turn_end gate path in `RunToolLoop` (v1 wires only the W/X path; `Action.Kind == "turn_end"` is designed for the later text checks).
- The embedded matrix-router (the fast model is a hardcoded override until then).
- A `/watchdog` slash command (config toggles it for now).

---

## Self-Review

- **Spec coverage:** pure watchdog package (Tasks 1–5: verdict, Check, debug-loop, Gate+state, justify tool); RunToolLoop integration (Task 6: WatchdogGate seam + challenge/block/escalate); echo (Task 7); config (Task 8); server wiring incl. the fast-model OneShot (Task 9). Fail-open enforced in parseVerdict (conservative), Gate (check errors → allow), and the debug-loop check (nil oneShot → no violation). Default-off via config. Strict/challenge modes + escalate covered.
- **Placeholder scan:** the one soft spot is Task 9's `defaultWatchdogModel` (pick + document the smallest reliably-present local model) and the RunToolLoop insertion (Task 6 gives the exact placement + decision handling in prose + code fragments against the real wxCalls loop) — both are concrete instructions, not TBDs.
- **Type consistency:** `Verdict`/`Action`/`Check`/`OneShotFunc`/`Decision`/`Watchdog`/`keyFor` used consistently across Tasks 1–5; `WatchdogGate`/`WatchdogDecision` (package agent) in Tasks 6/9; `Config.Watchdog` (Task 8) consumed in Task 9.
- **Dependency note:** Phase 1 is self-contained (stub the model). Phase 2 depends on Phase 1's `Watchdog`. Phase 3 depends on both + the dispatch engine (built). The router "fast" class is a hardcoded model override until the matrix-router lands.
