# Compaction 2b-3b — Footer Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make compaction visible in the always-on footer meter: an honest sent/raw size with a savings badge, and a live "compacting…" animation while a background pass runs. Plus a server perf fix so the now-polled `raw` size is cheap.

**Architecture:** The server computes `raw` via a fast byte estimate (not tiktoken over full history). The CLI footer polls `GetContextUsage` during the compaction window (kicked after each turn, sustained while compacting), stores `raw`/`compacting`, and `renderContextMeter` shows a `▣ N%↓` savings badge when compacted, or an animated `compacting…` sweep while a pass runs (reusing `animateSpinnerGlyph`/`animateLimeSweep`).

**Tech Stack:** Go; Bubble Tea (`charm.land/bubbletea/v2`); the 2b-3a RPCs/agentclient.

## Global Constraints

- The footer meter already reflects `TokensUsed` (now the sent size, from 2b-3a) — this plan adds the raw/savings + the animation; it must not regress the existing meter for un-compacted conversations (when sent==raw, no badge, no animation).
- The compacting animation reuses the existing `animateSpinnerGlyph` / `animateLimeSweep` and the `progressAnimTick` 50ms loop.
- Build + test: server `cd source/server && go build ./... && go test ./... -count=1`; CLI `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/internal/server/server.go` — `raw` via a fast byte estimate in `GetContextUsage` + `GetCompactionState`.
- `source/clients/cli/internal/ui/model.go` — `ctxUsageMsg` Raw/Compacting; model fields; the poll loop; `renderContextMeter` badge + animation; `progressAnimTick` re-schedule while compacting.
- Tests: `source/clients/cli/internal/ui/*_test.go`.

---

## Task 1: Server — cheap `raw` estimate (hot-path fix)

**Files:**
- Modify: `source/server/internal/server/server.go` (`GetContextUsage`, `GetCompactionState`)
- Test: `source/server/internal/server/compaction_rpc_test.go`

**Interfaces:**
- Produces: a package-private `estimateRawTokens(turns []conversation.Turn) int` — a fast `len/4` byte estimate over the turns' content (no tiktoken). Used for `raw_tokens` in both handlers; `sent` stays a precise tiktoken count of the small send-view.

- [ ] **Step 1: Write the failing test**

Append to `source/server/internal/server/compaction_rpc_test.go`:

```go
func TestGetContextUsage_RawIsCheapEstimateNotZero(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "c1", Role: "user",
		Content: "a reasonably long user message that should estimate to several tokens"})

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetRawTokens() <= 0 {
		t.Errorf("raw_tokens should be a positive estimate, got %d", resp.GetRawTokens())
	}
	// No compaction state → sent == raw still holds (both derived from the same turns).
	if resp.GetTokensUsed() != resp.GetRawTokens() {
		t.Errorf("no compaction → sent==raw: sent=%d raw=%d", resp.GetTokensUsed(), resp.GetRawTokens())
	}
}
```

NOTE: this keeps the **sent==raw** invariant by also switching `sent`'s computation to the same estimate **when there is no compaction state** — see Step 3. (When compacted, sent is the precise tiktoken count of the small view; raw is the estimate. The invariant only needs to hold for the un-compacted case, which the meter/badge logic relies on to decide "not compacted → no badge.")

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/server/ -run TestGetContextUsage_RawIsCheapEstimate -count=1`
Expected: with the current precise tiktoken `raw`, `sent != raw` is possible (tiktoken vs tiktoken would actually match today) — the test pins the new contract; it FAILS only once `estimateRawTokens` is referenced and undefined. (If it passes pre-change because both use tiktoken, that's fine — Step 3 makes raw the cheap estimate while preserving the invariant.)

- [ ] **Step 3: Add the estimator + use it**

Add to `server.go`:

```go
// estimateRawTokens is a fast len/4 token estimate over the turns' text — used
// for the displayed raw/savings figure so the footer's frequent GetContextUsage
// poll never tokenizes the full uncompacted history with tiktoken.
func estimateRawTokens(turns []conversation.Turn) int {
	n := 0
	for _, t := range turns {
		n += len(t.Content) + len(t.BlocksJSON)
	}
	return (n + 3) / 4
}
```

In `GetContextUsage`, compute `raw` via the estimate, and keep the sent==raw invariant for the un-compacted case by deriving `sent` from the same estimate when there's no compaction state:

```go
	sent, raw := 0, 0
	if store := s.agent.PersistentStore(); store != nil && convID != "" {
		if turns, err := store.GetTurns(ctx, convID); err == nil {
			raw = estimateRawTokens(turns)
			state, _ := store.GetCompaction(ctx, convID)
			if state.ConsolidatedJSON == "" {
				sent = raw // no compaction → sent is the full history
			} else {
				view, _ := compactor.BuildSendView(turns, state)
				sent = compaction.TotalTokens(contextmeter.Default(), view)
			}
		}
	}
```

Apply the same `raw = estimateRawTokens(turns)` change in `GetCompactionState` (replace the `TotalTokens(tok, BuildLLMHistory(turns))` for raw; keep `sent` precise as it is). Remove any now-unused `BuildLLMHistory`/`tok` references if they become unused in that handler (keep them where `sent` still needs them).

- [ ] **Step 4: Run the tests + suite**

Run: `cd source/server && go test ./internal/server/ -count=1`
Expected: PASS (incl. the prior `sent==raw` test and the new one).
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/server/server.go internal/server/compaction_rpc_test.go
git commit -m "perf(server): estimate raw context tokens cheaply (footer polls GetContextUsage)"
```

---

## Task 2: CLI — usage data + the compaction-window poll

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go`
- Test: `source/clients/cli/internal/ui/model_test.go` (or an existing UI test file)

**Interfaces:**
- Consumes: `agentclient.ContextUsage.{RawTokens, Compacting}` (2b-3a).
- Produces: `ctxUsageMsg` gains `Raw int` + `Compacting bool`; model gains `ctxRaw int`, `compacting bool`, `ctxPollTicks int`; a `ctxUsageTickMsg` + `ctxUsageTick()` poll (~2s) that re-schedules while `ctxPollTicks > 0` or `m.compacting`.

- [ ] **Step 1: Write the failing test**

Add to a UI test file:

```go
func TestCtxUsageMsg_StoresRawAndCompacting(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), convID: "c1"}
	m2, _ := m.Update(ctxUsageMsg{Used: 18000, Max: 200000, Raw: 340000, Compacting: true})
	mm := m2.(Model)
	if mm.cumIn != 18000 || mm.ctxRaw != 340000 || !mm.compacting {
		t.Errorf("ctxUsageMsg not stored: cumIn=%d ctxRaw=%d compacting=%v", mm.cumIn, mm.ctxRaw, mm.compacting)
	}
}
```

(UI tests construct `Model{...}` literals directly — see `confirm_test.go` / `context_view_route_test.go`. `theme` is imported in those test files; add it if your test file lacks it.)

- [ ] **Step 2: Run to verify failure**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestCtxUsageMsg_StoresRawAndCompacting -count=1`
Expected: FAIL — `Raw`/`Compacting` fields and `ctxRaw`/`compacting` undefined.

- [ ] **Step 3: Extend the message, the fetch, and the model fields**

`ctxUsageMsg`:

```go
type ctxUsageMsg struct {
	Used, Max  int
	Percent    float64
	Raw        int
	Compacting bool
}
```

`fetchContextUsage` return:

```go
		return ctxUsageMsg{
			Used: u.TokensUsed, Max: u.ModelMax, Percent: u.Percent,
			Raw: u.RawTokens, Compacting: u.Compacting,
		}
```

Model fields (beside `cumIn, cumOut`):

```go
	ctxRaw       int
	compacting   bool
	ctxPollTicks int
```

In the `ctxUsageMsg` handler (the existing `case ctxUsageMsg:`), after the existing `cumIn`/`modelMaxTokens` sets, add:

```go
		m.ctxRaw = msg.Raw
		wasCompacting := m.compacting
		m.compacting = msg.Compacting
		// Kick the per-frame animation loop when a pass starts.
		var cmd tea.Cmd
		if m.compacting && !wasCompacting {
			cmd = progressAnimTick()
		}
		return m, cmd
```

(Replace the existing `return m, nil` in that case.)

- [ ] **Step 4: Add the poll loop**

```go
type ctxUsageTickMsg struct{}

// ctxUsageTick polls the context meter on a slow cadence so the footer catches
// background compaction (which fires ~debounce seconds after a turn, off the
// request path).
func ctxUsageTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ctxUsageTickMsg{} })
}
```

Handler:

```go
	case ctxUsageTickMsg:
		if m.convID == "" {
			return m, nil
		}
		if m.ctxPollTicks > 0 {
			m.ctxPollTicks--
		}
		// Keep polling while we're in a warm window after a turn, or actively
		// compacting; otherwise let the loop go idle until the next turn.
		if m.ctxPollTicks > 0 || m.compacting {
			return m, tea.Batch(fetchContextUsage(m.agent, m.convID), ctxUsageTick())
		}
		return m, fetchContextUsage(m.agent, m.convID) // one final settle, no re-tick
```

Kick the warm window when a turn finishes — at the existing turn-done site (~line 990 where `fetchContextUsage` is already batched), set `m.ctxPollTicks = 20` and add `ctxUsageTick()` to the batch:

```go
		m.ctxPollTicks = 20 // ~40s warm window covers the compaction debounce
		done := tea.Batch(fetchContextUsage(m.agent, m.convID), fetchRecap(m.agent, m.convID), ctxUsageTick())
```

- [ ] **Step 5: Re-schedule the animation while compacting**

In the `progressAnimTickMsg` handler, before the final `return m, nil`, add a compacting branch:

```go
		if m.compacting {
			return m, progressAnimTick()
		}
```

- [ ] **Step 6: Run the test + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestCtxUsageMsg -count=1`
Expected: PASS.
Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd source/clients/cli
git add internal/ui/model.go internal/ui/*_test.go
git commit -m "feat(cli): footer polls context usage during the compaction window (raw + compacting)"
```

---

## Task 3: CLI — savings badge + compacting animation in the meter

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (`renderContextMeter`)
- Test: `source/clients/cli/internal/ui/model_test.go`

**Interfaces:**
- Consumes: `m.cumIn` (sent), `m.ctxRaw` (raw), `m.compacting`, `m.modelMaxTokens`.
- Produces: `renderContextMeter` shows `… · ▣ N%↓` when `ctxRaw > used` (compacted), and replaces the bar with `<spinner> compacting…` (lime-swept) when `m.compacting`.

- [ ] **Step 1: Write the failing tests**

```go
func TestContextMeter_SavingsBadgeWhenCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "↓") {
		t.Errorf("expected a savings badge when raw > sent:\n%s", out)
	}
}

func TestContextMeter_NoBadgeWhenNotCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 18000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if strings.Contains(out, "↓") {
		t.Errorf("no badge when sent==raw:\n%s", out)
	}
}

func TestContextMeter_CompactingOverlay(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000, compacting: true}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "compacting") {
		t.Errorf("expected a compacting overlay while a pass runs:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextMeter -count=1`
Expected: FAIL.

- [ ] **Step 3: Update `renderContextMeter`**

At the top of `renderContextMeter`, handle the compacting state first, then add the savings badge to the normal path. Insert after the `used`/`max`/`pct` are computed and before the final return:

```go
	// While a background compaction pass runs, the meter shows an animated
	// "compacting…" sweep in place of the bar.
	if m.compacting {
		return m.styles.Bright.Render("context") + "  " +
			animateSpinnerGlyph() + " " + animateLimeSweep("compacting…")
	}
```

And where the meter line is assembled, append a savings badge when compacted:

```go
	badge := ""
	if m.ctxRaw > used && used > 0 {
		saved := int(100 * (1 - float64(used)/float64(m.ctxRaw)))
		badge = m.styles.Muted.Render(fmt.Sprintf("  ·  ▣ %d%%↓", saved))
	}
```

Append `badge` to the returned meter string (after the bar/percent). (Match the existing return composition in `renderContextMeter` — the badge is the only addition to the normal path.)

- [ ] **Step 4: Run the tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextMeter -count=1`
Expected: PASS.
Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/clients/cli
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "feat(cli): footer savings badge + animated compacting overlay in the context meter"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b3-visibility-design.md` §1 footer):
- Honest sent size (already from 2b-3a) + savings badge → Task 3. ✓
- Live "compacting…" animation (sprite + lime-sweep) while a pass runs → Tasks 2 (flag + anim kick + re-tick) + 3 (overlay render). ✓
- Catching background compaction (the poll window) → Task 2 (`ctxUsageTick`). ✓
- Hot-path raw tokenization fix (the 2b-3a deferred Minor) → Task 1. ✓
- No regression when un-compacted (sent==raw → no badge, no animation) → Task 1 invariant + Task 3 `NoBadge` test. ✓

**Placeholder scan:** none — every step has concrete code. Tests build a `Model{styles: theme.NewStyles(theme.Cracker()), …}` literal (matching `confirm_test.go` / `context_view_route_test.go`), not a helper.

**Type consistency:** `ctxUsageMsg.{Raw,Compacting}` (Task 2) map from `agentclient.ContextUsage.{RawTokens,Compacting}` (2b-3a). Model fields `ctxRaw`/`compacting`/`ctxPollTicks` set in Task 2's handlers, read in Task 3's `renderContextMeter`. `animateSpinnerGlyph`/`animateLimeSweep`/`progressAnimTick` are existing. `estimateRawTokens` (Task 1) is server-only.

**Deferred to 2b-3c:** the `/c` page (summary block, frozen/live split, original toggle, export keybind).
