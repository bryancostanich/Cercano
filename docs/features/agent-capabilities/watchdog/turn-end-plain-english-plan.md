# Watchdog turn_end + plain-english Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the watchdog supervise the model's final reply text — at turn end, judge it with a `plain-english` check and, on a violation, reopen the turn for a rewrite.

**Architecture:** A `plain-english` `Check` + a `turn_end` identity fix in `internal/watchdog`; a second `WatchdogTurnEnd` seam on `ToolLoopInput` that reopens the turn on a challenge; and a server adapter mapping it to `Gate(Action{Kind:"turn_end"})`.

**Tech Stack:** Go 1.26 (`cercano/source/server`), reusing the increment-1 watchdog seam pattern + increment-2 challenge rendering.

## Global Constraints

- **Fail-open, never trap the turn:** nil gate, gate error, parse failure, or model timeout → the reply returns unchanged. The reopen path is taken ONLY on an explicit `challenge`/`block` verdict.
- **Reopen, don't block:** a turn_end violation appends a revise-instruction to history and continues the loop (the model regenerates); it never drops or edits the reply itself. The loop's iteration cap bounds rewrites.
- **turn_end escalate is graceful** (v1): on `escalate`, return the reply and emit `LoopWatchdogEscalate` (surfaced in scrollback) — do NOT prompt. A proper human-confirm for turn_end is a follow-on.
- **Watchdog stays default-OFF.** `plain-english` runs only when the watchdog is enabled.
- Commit messages contain no "Claude"; no `Co-Authored-By`. gofmt-clean touched files; `go build ./...` + `go test` green before each commit; `git status` clean after.

---

## File Structure

- `source/server/internal/watchdog/watchdog.go` — `keyFor` turn_end identity.
- `source/server/internal/watchdog/plainenglish.go` (+ `_test.go`) — the check.
- `source/server/internal/server/watchdog_wire.go` — register `plain-english`.
- `source/server/pkg/config/config.go` — default checks list.
- `source/server/internal/agent/watchdog_seam.go` — `WatchdogTurnEnd` type.
- `source/server/internal/agent/toolloop.go` — `ToolLoopInput` field + the turn-end intervention at line 209.
- `source/server/internal/server/server.go` — the `wdTurnEnd` adapter + `runMainLoop` param + both call sites.

---

### Task 1: watchdog turn_end support (identity + plain-english check)

**Files:**
- Modify: `source/server/internal/watchdog/watchdog.go` (`keyFor` ~line 85)
- Create: `source/server/internal/watchdog/plainenglish.go`, `plainenglish_test.go`
- Modify: `source/server/internal/server/watchdog_wire.go` (~line 34), `source/server/pkg/config/config.go` (Defaults) + `config_test.go`

**Interfaces:**
- Produces: `func PlainEnglishCheck() Check`; `keyFor` keys `turn_end` actions on `Text`.

- [ ] **Step 1: Write the failing test** (`plainenglish_test.go` + a keyFor case in `watchdog_test.go`):

```go
// plainenglish_test.go
package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestPlainEnglishApplies(t *testing.T) {
	c := PlainEnglishCheck()
	long := strings.Repeat("word ", 20) // > 40 chars
	if !c.Applies(Action{Kind: "turn_end", Text: long}) {
		t.Fatal("a substantive turn_end reply should apply")
	}
	if c.Applies(Action{Kind: "turn_end", Text: "Done."}) {
		t.Fatal("a terse reply must be skipped")
	}
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Text: long}) {
		t.Fatal("tool_call must not apply")
	}
}

func TestPlainEnglishEvaluate(t *testing.T) {
	long := strings.Repeat("leverage synergies ", 5)
	var gotPrompt string
	oneShot := func(_ context.Context, p string) (string, error) {
		gotPrompt = p
		return "VIOLATION: yes\nCHALLENGE: drop the corporate jargon", nil
	}
	v, err := PlainEnglishCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: long}, oneShot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "plain-english" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, long) {
		t.Fatal("prompt must embed the reply text")
	}
	// nil oneShot → fail open
	if vn, _ := PlainEnglishCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: long}, nil); vn.Violation {
		t.Fatal("nil oneShot must fail open")
	}
}
```

```go
// add to watchdog_test.go
func TestKeyForTurnEndUsesText(t *testing.T) {
	a1 := Action{Kind: "turn_end", Text: "reply one"}
	a2 := Action{Kind: "turn_end", Text: "reply two"}
	if keyFor("plain-english", a1) == keyFor("plain-english", a2) {
		t.Fatal("different turn_end texts must yield different keys")
	}
	if keyFor("plain-english", a1) != keyFor("plain-english", Action{Kind: "turn_end", Text: "reply one"}) {
		t.Fatal("same turn_end text must yield the same key")
	}
	// tool_call identity unchanged (keys on ToolArgs, not Text)
	tc := Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"p":"a"}`), Text: "ignored"}
	if keyFor("debug-loop", tc) != keyFor("debug-loop", Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"p":"a"}`), Text: "different"}) {
		t.Fatal("tool_call key must ignore Text")
	}
}
```

- [ ] **Step 2: Run; fail.** `cd source/server && go test ./internal/watchdog/ -run 'PlainEnglish|KeyForTurnEnd' -v`.

- [ ] **Step 3: Fix `keyFor`** (`watchdog.go` ~85):

```go
func keyFor(protocol string, a Action) string {
	identity := a.ToolArgs
	if a.Kind == "turn_end" {
		identity = []byte(a.Text)
	}
	sum := sha256.Sum256(identity)
	return fmt.Sprintf("%s|%s|%x", protocol, a.ToolName, sum[:6])
}
```

- [ ] **Step 4: Implement `plainenglish.go`:**

```go
package watchdog

import (
	"context"
	"strings"
)

type plainEnglishCheck struct{}

// PlainEnglishCheck flags an assistant reply that talks down, uses LLM/corporate
// jargon, or over-hedges instead of plain colleague-level English.
func PlainEnglishCheck() Check { return plainEnglishCheck{} }

func (plainEnglishCheck) Name() string { return "plain-english" }

// plainEnglishMinLen skips terse acknowledgements ("Done.", "Yes.") so the check
// doesn't fire on trivial replies.
const plainEnglishMinLen = 40

func (plainEnglishCheck) Applies(a Action) bool {
	return a.Kind == "turn_end" && len(strings.TrimSpace(a.Text)) >= plainEnglishMinLen
}

func (plainEnglishCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "plain-english"}, nil // fail open
	}
	out, err := oneShot(ctx, buildPlainEnglishPrompt(a.Text))
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("plain-english", out), nil
}

func buildPlainEnglishPrompt(text string) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing a plain-English register for the assistant's replies.\n")
	b.WriteString("Judge ONLY whether the reply below talks down to the user, uses LLM/corporate jargon or filler (e.g. \"delve\", \"leverage\", \"it's important to note\", \"I'd be happy to help!\"), or over-hedges — instead of plain, colleague-level English that assumes the reader knows the domain. Concise, direct, technical prose is NOT a violation.\n\n")
	b.WriteString("Reply:\n")
	b.WriteString(text)
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}
```

- [ ] **Step 5: Run; pass.** `go test ./internal/watchdog/ -run 'PlainEnglish|KeyForTurnEnd' -v`.

- [ ] **Step 6: Register + default.** In `watchdog_wire.go` `switch name` (after `"commit-checkpoint"`):

```go
		case "plain-english":
			checks = append(checks, watchdog.PlainEnglishCheck())
```

In `config.go` `Defaults()`:

```go
			Checks:        []string{"debug-loop", "commit-checkpoint", "plain-english"},
```

Update the `TestWatchdogDefaults` assertion in `config_test.go` to expect the 3-element list.

- [ ] **Step 7: Verify + commit.** `go test ./internal/watchdog/ ./internal/server/ ./pkg/config/ -count=1` green; gofmt clean; `go build ./...` clean.

```bash
git -C <worktree> commit -am "feat(watchdog): plain-english check + turn_end action identity"
```

### Task 2: the turn_end seam in RunToolLoop

**Files:**
- Modify: `source/server/internal/agent/watchdog_seam.go` (add the type)
- Modify: `source/server/internal/agent/toolloop.go` (`ToolLoopInput` field + the intervention at ~line 209)
- Test: `source/server/internal/agent/watchdog_loop_test.go`

**Interfaces:**
- Consumes: `WatchdogDecision` (existing); `LoopWatchdogChallenge`/`LoopWatchdogEscalate` (existing).
- Produces: `type WatchdogTurnEnd func(ctx, finalText string, transcript []llm.Message) WatchdogDecision`; `ToolLoopInput.WatchdogTurnEnd`.

- [ ] **Step 1: Add the seam type** to `watchdog_seam.go`:

```go
// WatchdogTurnEnd, when set, is consulted with the model's final reply text
// before the turn returns. nil = disabled. Fail-open: any error → the reply is
// returned unchanged; the turn is reopened only on an explicit challenge/block.
type WatchdogTurnEnd func(ctx context.Context, finalText string, transcript []llm.Message) WatchdogDecision
```

- [ ] **Step 2: Add the `ToolLoopInput` field** (next to `WatchdogGate`):

```go
	// WatchdogTurnEnd supervises the model's final reply text at turn boundaries.
	// nil = disabled.
	WatchdogTurnEnd WatchdogTurnEnd
```

- [ ] **Step 3: Write the failing test** in `watchdog_loop_test.go` (mirror `TestWatchdogGate_*`'s fake-provider harness):

```go
func TestWatchdogTurnEnd_ChallengeReopensTurn(t *testing.T) {
	// Turn 1: model returns a "bad" plain-text reply (no tool calls).
	// Turn 2: model returns a "good" reply.
	prov := &scriptedProvider{replies: []string{"jargon-laden reply here", "clean reply"}}
	calls := 0
	gate := func(_ context.Context, finalText string, _ []llm.Message) WatchdogDecision {
		calls++
		if calls == 1 {
			return WatchdogDecision{Action: "challenge", Protocol: "plain-english", Challenge: "too much jargon"}
		}
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), Permissions: nil,
		UserInput: "hi", WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "clean reply" {
		t.Fatalf("expected the revised reply to be returned, got %q", res.FinalText)
	}
	if calls != 2 {
		t.Fatalf("gate should fire on both turn ends, got %d", calls)
	}
}

func TestWatchdogTurnEnd_AllowReturns(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"a perfectly fine substantive reply"}}
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi", WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "a perfectly fine substantive reply" {
		t.Fatalf("allow must return the reply unchanged, got %q", res.FinalText)
	}
}
```

(Use the existing test harness in this file: reuse its fake provider + registry helpers; if the existing fake provider isn't named `scriptedProvider`/`emptyRegistry`, adapt to the real helpers — read the file first. The key assertions are the two above.)

- [ ] **Step 4: Run; fail.** `go test ./internal/agent/ -run WatchdogTurnEnd -v`.

- [ ] **Step 5: Implement the intervention.** In `toolloop.go`, replace the turn-end return block (~line 209) with:

```go
		if len(toolCalls) == 0 {
			if in.WatchdogTurnEnd != nil && strings.TrimSpace(finalText) != "" {
				wd := in.WatchdogTurnEnd(ctx, finalText, hist)
				switch wd.Action {
				case "challenge", "block":
					note := "⚡ watchdog (" + wd.Protocol + "): " + wd.Challenge +
						" Rewrite your reply in plain, colleague-level English"
					if wd.Action == "block" {
						note += " (rewrite required — no override)."
					} else {
						note += ", or call `justify` with a reason."
					}
					emit(LoopEvent{Kind: LoopWatchdogChallenge, Detail: wd.Protocol, Summary: wd.Challenge})
					appendTurn(llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})
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
```

NOTE the `appendTurn(assistant resp.Blocks)` before the revise message: confirm whether the loop already appended the assistant turn earlier in this iteration (there is an `appendTurn(llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})` around line 189). If it is already appended before line 209, DROP the duplicate `appendTurn(assistant...)` line above and append ONLY the revise user message. Read lines 185-210 first and match reality — the history must contain the assistant reply exactly once, then the revise user message.

- [ ] **Step 6: Run; pass** (+ existing `internal/agent` tests unchanged when `WatchdogTurnEnd` is nil). `go test ./internal/agent/ -count=1`.

- [ ] **Step 7: Commit**

```bash
git -C <worktree> commit -am "feat(agent): WatchdogTurnEnd seam — reopen the turn on a text challenge"
```

### Task 3: server wiring

**Files:**
- Modify: `source/server/internal/server/server.go` (the `wdGate` block ~1924, `runMainLoop` ~2029 + its two call sites ~1954/1981)
- Test: `source/server/internal/server/*_test.go`

**Interfaces:**
- Consumes: `agent.WatchdogTurnEnd` (Task 2); the guarded `wd` snapshot (from the dev-settings increment).
- Produces: `runMainLoop` gains a `watchdogTurnEnd agent.WatchdogTurnEnd` param.

- [ ] **Step 1: Build the turn-end adapter.** In the per-turn watchdog block (where `wdGate` is built, ~1924, using the RLock-guarded `wd` snapshot), add right after the `wdGate` closure:

```go
	var wdTurnEnd agent.WatchdogTurnEnd
	if wd != nil {
		wdTurnEnd = func(ctx context.Context, finalText string, transcript []llm.Message) agent.WatchdogDecision {
			d := wd.Gate(ctx, convID, watchdog.Action{Kind: "turn_end", Text: finalText, Transcript: transcript})
			return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge}
		}
	}
```

- [ ] **Step 2: Thread it through `runMainLoop`.** Add a `watchdogTurnEnd agent.WatchdogTurnEnd` parameter to `runMainLoop` (next to `watchdogGate`), and set `WatchdogTurnEnd: watchdogTurnEnd` in the `ToolLoopInput`. Pass `wdTurnEnd` at BOTH call sites (~1954 and the fallback ~1981).

- [ ] **Step 3: Write the failing test** (mirror the increment-2 gate-adapter test): assert the turn-end adapter maps a `watchdog.Decision{Action:"challenge",Protocol:"plain-english",Challenge:"x"}` (via a stub watchdog or the real Gate over a stubbed check) to `agent.WatchdogDecision{Action:"challenge",Protocol:"plain-english",Challenge:"x"}`. If a full server harness is heavy, test the adapter closure shape directly.

- [ ] **Step 4: Verify + commit.** `go test ./internal/server/ ./internal/agent/ ./internal/watchdog/ -count=1` green; `go build ./...` clean; gofmt clean.

```bash
git -C <worktree> commit -am "feat(server): wire WatchdogTurnEnd to Gate(turn_end)"
```

---

## Self-Review

- **Spec coverage:** plain-english check + turn_end identity (T1); the reopen-the-turn seam + intervention (T2); server adapter (T3). Fail-open: nil oneShot (T1), nil gate / allow / error → return unchanged (T2). Reopen-not-block (T2 append+continue). turn_end escalate graceful (T2, flagged as a v1 simplification of the design's confirm-prompt). Default-ON when watchdog enabled (T1 defaults). `LoopWatchdogChallenge` emit → renders via the increment-2 CLI path (no new client work).
- **Placeholder scan:** the soft spots are the T2/T3 test harness helper names (`scriptedProvider`/`emptyRegistry`/the gate-adapter test) — mitigated by "mirror the existing `TestWatchdogGate_*` harness in the same file"; all production code is exact. T2 Step 5 flags the one real integration check (assistant-turn append de-dup) with explicit instructions.
- **Type consistency:** `PlainEnglishCheck`/`plainEnglishMinLen`/`buildPlainEnglishPrompt` (T1); `keyFor` turn_end branch (T1) consumed by `Gate` identity; `WatchdogTurnEnd func(ctx, finalText string, transcript []llm.Message) WatchdogDecision` defined (T2) and adapted (T3); `watchdog.Action{Kind:"turn_end", Text, Transcript}` consistent T1↔T3.
- **Dependency order:** T1 (check + identity) → T2 (seam, independent of T1 but the feature needs both) → T3 (wiring, needs T2's type + T1's check registered).
