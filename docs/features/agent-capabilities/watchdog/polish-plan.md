# Watchdog Polish (Increment C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close out the watchdog's accepted follow-ons: three robustness fixes (decorated verdicts, echo race, stale check-toggle state), the logged test-tightening items, and dead-code/doc hygiene.

**Architecture:** No new machinery. Each fix is a small change to existing code, landed with its covering test. Grouped by module: T1 `internal/watchdog`, T2 `internal/agent` + `pkg/agentclient` (tests only), T3 CLI + docs.

**Tech Stack:** Go 1.26 (`cercano/source/server` + `cercano/source/clients/cli`).

## Global Constraints

- **No behavior changes beyond the three named fixes.** Everything else is tests, dead code, and docs.
- **Fail-open preserved:** parseVerdict still defaults to no-violation on genuine ambiguity; only decorated affirmatives (`yes.`/`yes (…)`/`true…`) now count as violations.
- **emitEcho invariant preserved:** the callback is never invoked while `w.mu` is held (snapshot under lock, call outside).
- Commit messages contain no "Claude"; no `Co-Authored-By`. gofmt-clean touched files; `go build ./...` + `go test ./... -count=1` green in each affected module before every commit; `git status` clean after.

---

## File Structure

- T1: `source/server/internal/watchdog/{verdict.go, verdict_test.go, watchdog.go, echo_test.go, debugloop_test.go, commitcheckpoint_test.go}`
- T2: `source/server/internal/agent/watchdog_loop_test.go`, `source/server/pkg/agentclient/client_watchdog_test.go`
- T3: `source/clients/cli/internal/ui/{settings_page.go, settings_build.go, watchdog_render_test.go, settings_build_test.go}`, `docs/features/agent-capabilities/watchdog/design.md`

---

### Task 1: watchdog package — verdict prefix-match, echo race, tests, dead code

**Files:**
- Modify: `source/server/internal/watchdog/verdict.go` (the `VIOLATION:` value check)
- Modify: `source/server/internal/watchdog/verdict_test.go`
- Modify: `source/server/internal/watchdog/watchdog.go` (`SetEcho` ~line 58, `emitEcho` ~line 62)
- Modify: `source/server/internal/watchdog/echo_test.go` (block/escalate branches)
- Modify: `source/server/internal/watchdog/debugloop_test.go` (nil-oneShot test)
- Modify: `source/server/internal/watchdog/commitcheckpoint_test.go` (remove `editAction2`)

**Interfaces:** no signature changes; `SetEcho`/`emitEcho`/`parseVerdict` keep their existing signatures.

- [ ] **Step 1: Write the failing verdict tests.** Add to `TestParseVerdict` in `verdict_test.go` (keep existing cases):

```go
	// Decorated affirmatives count as violations (prefix match).
	if !parseVerdict("debug-loop", "VIOLATION: yes.\nCHALLENGE: c").Violation {
		t.Fatal("'yes.' must count as a violation")
	}
	if !parseVerdict("debug-loop", "VIOLATION: yes (clearly)\nCHALLENGE: c").Violation {
		t.Fatal("'yes (clearly)' must count as a violation")
	}
	if !parseVerdict("debug-loop", "VIOLATION: true.").Violation {
		t.Fatal("'true.' must count as a violation")
	}
	// "no"-family values never count, decorated or not.
	if parseVerdict("debug-loop", "VIOLATION: no, looks fine").Violation {
		t.Fatal("'no, ...' must not count as a violation")
	}
	// The challenge is the model's line, not the fallback.
	v := parseVerdict("debug-loop", "VIOLATION: yes\nCHALLENGE: too much jargon here")
	if v.Challenge != "too much jargon here" {
		t.Fatalf("challenge must be the model's line, got %q", v.Challenge)
	}
```

- [ ] **Step 2: Run; fail.** `cd source/server && go test ./internal/watchdog/ -run TestParseVerdict -v` — the decorated cases fail.

- [ ] **Step 3: Fix `parseVerdict`.** In `verdict.go`, the value check is currently:

```go
			val := strings.TrimSpace(low[len("violation:"):])
			v.Violation = val == "yes" || val == "true"
```

Change to:

```go
			val := strings.TrimSpace(low[len("violation:"):])
			// Prefix match: fast local models sometimes decorate the verdict
			// ("yes.", "yes (clearly)"). Anything else — including "no" and
			// genuine garbage — fails open (no violation).
			v.Violation = strings.HasPrefix(val, "yes") || strings.HasPrefix(val, "true")
```

- [ ] **Step 4: Run; pass.** Same command — PASS.

- [ ] **Step 5: Write the failing echo-branch tests.** Add to `echo_test.go` (reuse the existing `fakeCheck` and the capture pattern from `TestEchoOnViolation`):

```go
func TestEchoOnBlock(t *testing.T) {
	w := New(Config{Mode: ModeStrict}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "blocked-c"}}}, nil)
	var got []string
	w.SetEcho(func(thread, text string) { got = append(got, thread+"|"+text) })
	if d := w.Gate(context.Background(), "conv", editAction()); d.Action != "block" {
		t.Fatalf("expected block, got %+v", d)
	}
	if len(got) != 1 || !strings.Contains(got[0], "watchdog|") || !strings.Contains(got[0], "blocked-c") {
		t.Fatalf("block must emit one watchdog-thread line with the challenge: %v", got)
	}
}

func TestEchoOnEscalate(t *testing.T) {
	w := New(Config{Mode: ModeChallenge, EscalateAfter: 2}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "debug-loop", Challenge: "esc-c"}}}, nil)
	var got []string
	w.SetEcho(func(thread, text string) { got = append(got, thread+"|"+text) })
	w.Gate(context.Background(), "conv", editAction()) // challenge (1st emit)
	got = nil
	if d := w.Gate(context.Background(), "conv", editAction()); d.Action != "escalate" {
		t.Fatalf("expected escalate, got %+v", d)
	}
	if len(got) != 1 || !strings.Contains(got[0], "watchdog|") || !strings.Contains(got[0], "esc-c") {
		t.Fatalf("escalate must emit one watchdog-thread line: %v", got)
	}
}
```

(`editAction()` is the existing helper in `watchdog_test.go`; add `"strings"`/`"context"` imports if missing.)

- [ ] **Step 6: Run; check.** `go test ./internal/watchdog/ -run TestEchoOn -v`. These may already PASS (the emit code covers all three decisions) — that is fine; they are coverage for previously untested branches. If one FAILS, the emit branch has a real bug: fix the emit in `watchdog.go` Gate so block/escalate emit exactly one line, and note it in the report.

- [ ] **Step 7: Fix the echo race.** In `watchdog.go`, replace `SetEcho` and `emitEcho`:

```go
// SetEcho registers a callback that is called on watchdog interventions and
// justify overrides. thread is "watchdog" or "main". Synchronized with w.mu,
// so it may be called on a live Watchdog; note the callback is global to the
// Watchdog, not per-conversation — under concurrent conversations, echo lines
// from all of them reach the same callback.
func (w *Watchdog) SetEcho(fn func(thread, text string)) {
	w.mu.Lock()
	w.echo = fn
	w.mu.Unlock()
}

// emitEcho calls the echo callback if one is registered. The callback itself
// is invoked outside w.mu (snapshot under lock, call outside), so callers may
// hold or not hold the mutex freely and the callback can never deadlock us.
func (w *Watchdog) emitEcho(thread, text string) {
	w.mu.Lock()
	fn := w.echo
	w.mu.Unlock()
	if fn != nil {
		fn(thread, text)
	}
}
```

CAUTION: `emitEcho` now takes `w.mu` briefly. Verify every existing `emitEcho` call site in `watchdog.go`/`justify.go` is OUTSIDE the mutex (they are — the increment-1 invariant was "never called while holding w.mu"); if any call site holds the lock, this deadlocks — check before committing by inspecting each call site.

- [ ] **Step 8: Write the failing (missing-coverage) debug-loop test.** Add to `debugloop_test.go`:

```go
func TestDebugLoopNilOneShotFailsOpen(t *testing.T) {
	a := Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"x.go"}`)}
	v, err := DebugLoopCheck().Evaluate(context.Background(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Violation {
		t.Fatal("nil oneShot must fail open (no violation)")
	}
	if v.Protocol != "debug-loop" {
		t.Fatalf("protocol: %q", v.Protocol)
	}
}
```

- [ ] **Step 9: Remove dead code.** Delete the unused `editAction2()` helper from `commitcheckpoint_test.go` (verify with `grep -rn "editAction2" source/server/internal/watchdog/` → only the definition).

- [ ] **Step 10: Verify + commit.** `go test ./internal/watchdog/ -count=1 -race` green (the `-race` run exercises the new locking); `gofmt -l internal/watchdog/` empty; `go build ./...` clean.

```bash
git -C <worktree> commit -am "fix(watchdog): accept decorated verdict affirmatives; synchronize echo; tighten tests"
```

### Task 2: turn_end branch tests + agentclient payload-loop e2e (tests only)

**Files:**
- Modify: `source/server/internal/agent/watchdog_loop_test.go`
- Modify: `source/server/pkg/agentclient/client_watchdog_test.go`

**Interfaces:** none — no production code changes. If any of these tests FAIL, that is a real bug: stop, report it, and fix only with controller sign-off (report DONE_WITH_CONCERNS).

- [ ] **Step 1: turn_end block test.** In `watchdog_loop_test.go`, mirror `TestWatchdogTurnEnd_ChallengeReopensTurn` (same `scriptedProvider`/`emptyRegistry` harness):

```go
func TestWatchdogTurnEnd_BlockReopensWithoutOverride(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"first reply", "revised reply"}}
	calls := 0
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		calls++
		if calls == 1 {
			return WatchdogDecision{Action: "block", Protocol: "plain-english", Challenge: "rewrite it"}
		}
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi",
		WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "revised reply" {
		t.Fatalf("block must reopen the turn; got %q", res.FinalText)
	}
	// The injected revise note must state no override is available and must
	// NOT offer justify.
	var note string
	for _, m := range res.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && strings.Contains(b.Text, "watchdog") {
				note = b.Text
			}
		}
	}
	if !strings.Contains(note, "no override") || strings.Contains(note, "justify") {
		t.Fatalf("block note wrong: %q", note)
	}
}

func TestWatchdogTurnEnd_EscalateReturnsReply(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"the reply"}}
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		return WatchdogDecision{Action: "escalate", Protocol: "plain-english", Challenge: "again"}
	}
	var kinds []LoopEventKind
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi",
		WatchdogTurnEnd: gate, MaxIterations: 5,
		EventSink: func(ev LoopEvent) { kinds = append(kinds, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "the reply" {
		t.Fatalf("escalate must return the reply unchanged, got %q", res.FinalText)
	}
	found := false
	for _, k := range kinds {
		if k == LoopWatchdogEscalate {
			found = true
		}
	}
	if !found {
		t.Fatal("escalate must emit LoopWatchdogEscalate")
	}
}
```

Adapt helper names to the real harness in that file if they differ (read it first); the assertions are the requirement. Note: the block-note assertion — the production note appends "(rewrite required — no override)." for block; asserting `no override` present and `justify` absent captures the contract. If the note text differs slightly, match the actual production string, but the justify-absent assertion must hold.

- [ ] **Step 2: Run.** `cd source/server && go test ./internal/agent/ -run WatchdogTurnEnd -v` — all PASS (these are coverage for existing behavior). A FAILURE is a real bug — stop and report.

- [ ] **Step 3: agentclient payload-loop e2e.** In `client_watchdog_test.go` (same package `agentclient`, so private fields are reachable). First READ `client.go` around lines 1140–1180 to get the exact method that opens `c.agent.StreamProcessRequest(...)` and runs the payload loop (its name, args, and return — likely a `StreamProcess`-style method returning `(<-chan StreamMsg, error)`), and the generated stream interface name (`proto.Agent_StreamProcessRequestClient`). Then add:

```go
// fakeProcessStream feeds canned StreamProcessResponses then io.EOF.
type fakeProcessStream struct {
	proto.Agent_StreamProcessRequestClient // embed; unused methods panic if called
	msgs []*proto.StreamProcessResponse
	i    int
}

func (f *fakeProcessStream) Recv() (*proto.StreamProcessResponse, error) {
	if f.i >= len(f.msgs) {
		return nil, io.EOF
	}
	m := f.msgs[f.i]
	f.i++
	return m, nil
}

// fakeAgentClient returns the fake stream from StreamProcessRequest.
type fakeAgentClient struct {
	proto.AgentClient // embed; unused methods panic if called
	stream proto.Agent_StreamProcessRequestClient
}

func (f *fakeAgentClient) StreamProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (proto.Agent_StreamProcessRequestClient, error) {
	return f.stream, nil
}

func TestStreamLoopDeliversWatchdogEvent(t *testing.T) {
	fake := &fakeAgentClient{stream: &fakeProcessStream{msgs: []*proto.StreamProcessResponse{
		{Payload: &proto.StreamProcessResponse_WatchdogEvent{WatchdogEvent: &proto.WatchdogEvent{
			Kind: "challenge", Protocol: "commit-checkpoint", Text: "commit first",
		}}},
	}}}
	c := &Client{agent: fake}
	out, err := c.StreamProcess(context.Background(), StreamProcessArgs{Input: "hi"}) // ← adapt to the REAL method name/signature found in Step 3's read
	if err != nil {
		t.Fatal(err)
	}
	var got *StreamMsg
	for m := range out {
		if m.Type == TypeWatchdog {
			mm := m
			got = &mm
		}
	}
	if got == nil {
		t.Fatal("no TypeWatchdog StreamMsg delivered through the real payload loop")
	}
	if got.WatchdogKind != "challenge" || got.Protocol != "commit-checkpoint" || got.Summary != "commit first" {
		t.Fatalf("mapping wrong: %+v", got)
	}
}
```

The method invocation line MUST be adapted to the real signature (that is the point of the read in this step); the fake-stream/fake-client scaffolding and the assertions are the requirement. Add `io`/`grpc` imports as needed. If `Client` construction requires more fields for this path (e.g. a non-nil conn guard), set the minimum; if the loop path can't run without a live gRPC conn, report DONE_WITH_CONCERNS explaining exactly what blocked it rather than watering the test down to the helper again.

- [ ] **Step 4: Run.** `go test ./pkg/agentclient/ -run StreamLoopDeliversWatchdog -v` — PASS.

- [ ] **Step 5: Verify + commit.** `go test ./internal/agent/ ./pkg/agentclient/ -count=1` green; gofmt clean; `go build ./...` clean.

```bash
git -C <worktree> commit -am "test(watchdog): cover turn_end block/escalate branches and the real agentclient payload loop"
```

### Task 3: CLI live-toggle fix, render-test tightening, comments, design-doc note

**Files:**
- Modify: `source/clients/cli/internal/ui/settings_page.go` (the `onCommit` currentChecks derivation, ~line 210)
- Modify: `source/clients/cli/internal/ui/settings_build.go` (comment)
- Modify: `source/clients/cli/internal/ui/settings_build_test.go` (live-derivation test)
- Modify: `source/clients/cli/internal/ui/watchdog_render_test.go` (block phrase + `_ = md` removal)
- Modify: `docs/features/agent-capabilities/watchdog/design.md` (v1 behavior notes)

**Interfaces:**
- Produces: `func watchdogChecksFromForm(f *form.Form) []string` (in `settings_build.go`) — walks the live form and returns the currently-on check names in `knownWatchdogChecks` order.

- [ ] **Step 1: Write the failing test** in `settings_build_test.go`:

```go
func TestWatchdogChecksFromForm(t *testing.T) {
	cfg := &agentclient.Config{WatchdogChecks: []string{"debug-loop", "plain-english"}}
	f := form.New([]form.Section{
		{Title: "Development Tools", Groups: []form.Group{
			{Title: "Watchdog", Fields: buildDevFields(cfg)},
		}},
	})
	got := watchdogChecksFromForm(f)
	if strings.Join(got, ",") != "debug-loop,plain-english" {
		t.Fatalf("live derivation: %v", got)
	}
}
```

(Add `strings` and `form` imports back to the test file.)

- [ ] **Step 2: Run; fail.** `cd source/clients/cli && go test ./internal/ui/ -run WatchdogChecksFromForm -v` — `undefined: watchdogChecksFromForm`.

- [ ] **Step 3: Implement** in `settings_build.go`:

```go
// watchdogChecksFromForm reads the current watchdog-check toggle states from
// the live form — the source of truth at commit time, immune to a stale or
// nil cached config. Order follows knownWatchdogChecks. ToggleField renders
// "on"/"off" via Display().
func watchdogChecksFromForm(f *form.Form) []string {
	on := map[string]bool{}
	collect := func(fields []form.Field) {
		for _, fld := range fields {
			if name, ok := strings.CutPrefix(fld.Key(), "watchdog-check-"); ok {
				on[name] = fld.Display() == "on"
			}
		}
	}
	for _, s := range f.Sections {
		collect(s.Fields)
		for _, g := range s.Groups {
			collect(g.Fields)
		}
	}
	out := []string{}
	for _, c := range knownWatchdogChecks {
		if on[c] {
			out = append(out, c)
		}
	}
	return out
}
```

(Ensure `form` is imported in `settings_build.go` — it is.)

- [ ] **Step 4: Wire the call site.** In `settings_page.go` `onCommit` (~line 208-213), replace the cached-config derivation:

```go
	var currentChecks []string
	if sp.cfg != nil {
		currentChecks = sp.cfg.WatchdogChecks
	}
	action := classifyCommit(key, value, currentChecks)
```

with:

```go
	// Derive the current check set from the live form fields, not the cached
	// config — immune to a stale/nil sp.cfg after a failed re-fetch. The
	// just-committed toggle has already flipped its state, and toggleCheck
	// sets membership explicitly, so double-application is idempotent.
	var currentChecks []string
	if sp.form != nil {
		currentChecks = watchdogChecksFromForm(sp.form)
	} else if sp.cfg != nil {
		currentChecks = sp.cfg.WatchdogChecks
	}
	action := classifyCommit(key, value, currentChecks)
```

VERIFY the idempotency claim while here: read `toggle_field.go` — if `ToggleField.Update` flips `f.on` before returning committed=true, the live read already includes the new state and `toggleCheck` re-applying `value` is a no-op; if it does NOT flip until after commit, the live read gives the pre-toggle state and `toggleCheck` applies the change — correct either way because membership is set explicitly, never flipped. State which case holds in your report.

- [ ] **Step 5: Run; pass.** `go test ./internal/ui/ -run 'WatchdogChecksFromForm|ClassifyCommit_Watchdog' -v` — PASS.

- [ ] **Step 6: Tighten the render test + remove scaffolding.** In `watchdog_render_test.go`: change the block-test assertion from `strings.Contains(out, "blocked")` to asserting the full phrase the renderer emits — read `chat_view.go`'s watchdog render code for the exact string (increment-2 used "(blocked — no override)"); assert that exact substring. Delete the `md := render.NewMarkdown(...)` / `_ = md` lines (and the `render` import if now unused).

- [ ] **Step 7: Reword the stale comment.** In `settings_build.go`, change:

```go
// knownWatchdogChecks must stay in sync with the check-map in
// internal/server/watchdog_wire.go.
```

to:

```go
// knownWatchdogChecks must stay in sync with the check switch in the server's
// buildWatchdogFrom (source/server/internal/server/watchdog_wire.go) and the
// default checks list in pkg/config.
```

- [ ] **Step 8: Design-doc note.** In `docs/features/agent-capabilities/watchdog/design.md`, append at the end of the "Intervention flow (challenge / justify / escalate)" section:

```markdown
**v1 behavior notes (turn_end):** at turn boundaries, `escalate` is graceful —
the loop emits the escalate event and returns the reply rather than prompting
the human (the confirm-prompt described above applies to the tool-call path).
The turn_end repeat counter keys on the exact reply text, so only
verbatim-repeated output reaches `escalate_after`; for varied output the
loop's iteration cap is the backstop.
```

- [ ] **Step 9: Verify + commit.** `cd source/clients/cli && go build ./... && go test ./... -count=1` green; `gofmt -l internal/ui/` shows none of the touched files.

```bash
git -C <worktree> commit -am "fix(cli): derive watchdog check toggles from live form state; test + doc hygiene"
```

---

## Self-Review

- **Spec coverage:** §1 robustness — parseVerdict prefix (T1 S1-4), echo race (T1 S7), live-toggle (T3 S1-5). §2 tests — debugloop nil (T1 S8), echo block/escalate (T1 S5-6), turn_end block/escalate (T2 S1), agentclient e2e (T2 S3), block phrase (T3 S6), verdict challenge content (T1 S1). §3 hygiene — editAction2 (T1 S9), `_ = md` (T3 S6), comment (T3 S7), doc note (T3 S8). All spec items mapped.
- **Placeholder scan:** two deliberate adapt-to-reality instructions (T2 S3 method name; T3 S6 exact render phrase) — each says exactly what to read and what the invariant assertion is; not TBDs. No other soft spots.
- **Type consistency:** `watchdogChecksFromForm(f *form.Form) []string` defined T3 S3, consumed T3 S4, tested T3 S1. `SetEcho`/`emitEcho` signatures unchanged (T1 S7). Test helpers (`editAction`, `fakeCheck`, `scriptedProvider`, `emptyRegistry`) are existing symbols verified this session.
- **Risk note:** T1 S7's locking change carries the one deadlock hazard; the step includes the call-site audit instruction and T1 S10 runs the package under `-race`.
