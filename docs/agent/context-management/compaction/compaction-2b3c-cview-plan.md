# Compaction 2b-3c — `/c` Page Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface compaction in the `/c` page: a header showing sent/raw/savings + frozen/live counts, the consolidated summary block above the live turns (with frozen turns collapsed behind it), a `Ctrl+O` toggle to the full original, and a `Ctrl+E` export of the full uncapped context to a JSON file.

**Architecture:** `/c` already loads every raw turn. This plan loads `GetCompactionState` into the snapshot, and `turnsLines` renders the **sent view** by default (summary block + a "N compacted" marker + only the live tail, split by `FrozenTurns` count) or the **full original** when toggled. Action keys are `Ctrl`-modified to avoid the prompt-bar typing conflict (like the existing `Ctrl+R`). Export runs off the UI thread and reports the written path.

**Tech Stack:** Go; Bubble Tea; the 2b-3a `agentclient.GetCompactionState` / `ExportContext`.

## Global Constraints

- `/c` action keys MUST be `Ctrl`-modified (the prompt bar is the input; a bare letter would be typed). Follow the existing `ctrl+r` pattern.
- When there's no compaction state (frozen count 0), `/c` renders exactly as today (all turns, no summary block, no savings).
- Build + test: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

## Interfaces consumed (2b-3a, already on this branch)

- `agentclient.CompactionState{ FrozenThrough int64; FrozenTurns, LiveTurns, CompactedSegments, RawTokens, SentTokens int; ConsolidatedSummary string; Compacting bool }`
- `(*agentclient.Client).GetCompactionState(ctx, convID) (*CompactionState, error)`, `ExportContext(ctx, convID) (string, error)`
- `agentclient.ContextUsage` (header), `ContextTurn` (turns).

---

## File Structure

- `source/clients/cli/internal/ui/context_view.go` — snapshot field + load; `renderHeader`; `turnsLines` split; `showOriginal` field; notice.
- `source/clients/cli/internal/ui/model.go` — `handleContextViewKey` `ctrl+o`/`ctrl+e`; the export command + `exportDoneMsg` handler.
- Tests: `source/clients/cli/internal/ui/*_test.go`.

---

## Task 1: Load compaction state + header

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go` (`contextSnapshot`, `loadContextSnapshot`, `renderHeader`)
- Test: `source/clients/cli/internal/ui/*_test.go`

**Interfaces:**
- Produces: `contextSnapshot` gains `Compaction *agentclient.CompactionState`; `renderHeader` shows `· ▣ N%↓ · F compacted · L live` when compaction is present (and `compacting…` when `Compaction.Compacting`).

- [ ] **Step 1: Write the failing test**

```go
func TestContextHeader_ShowsSavingsAndCounts(t *testing.T) {
	cv := expandTestView() // existing helper (context_view_expand_test.go)
	cv.snapshot.Usage = &agentclient.ContextUsage{TokensUsed: 18000, ModelMax: 200000, Percent: 0.09}
	cv.snapshot.Compaction = &agentclient.CompactionState{
		FrozenTurns: 84, LiveTurns: 12, RawTokens: 340000, SentTokens: 18000,
	}
	out := stripAnsiCSI(cv.renderHeader())
	for _, want := range []string{"↓", "84", "12"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextHeader_ShowsSavingsAndCounts -count=1`
Expected: FAIL — `Compaction` field undefined.

- [ ] **Step 3: Add the snapshot field + load**

In `contextSnapshot`:

```go
	Compaction *agentclient.CompactionState
```

In `loadContextSnapshot`, after the Usage fetch:

```go
	snap.Compaction, _ = ag.GetCompactionState(ctx, convID) // best-effort; nil on error
```

- [ ] **Step 4: Augment `renderHeader`**

Append a compaction segment to the existing return when `c.snapshot.Compaction` is present with frozen turns:

```go
func (c *contextView) renderHeader() string {
	if c.snapshot.UsageErr != nil || c.snapshot.Usage == nil {
		return c.styles.Muted.Render("context  usage unavailable")
	}
	u := c.snapshot.Usage
	pct := int(u.Percent*100 + 0.5)
	bar := renderMeterBar(u.Percent, 10, c.styles)
	head := fmt.Sprintf("%s  %s / %s  · %d%%  %s",
		c.styles.Bright.Render("context"),
		formatThousands(u.TokensUsed), formatThousands(u.ModelMax), pct, bar)

	cs := c.snapshot.Compaction
	if cs != nil && cs.FrozenTurns > 0 {
		saved := 0
		if cs.RawTokens > 0 {
			saved = int(100 * (1 - float64(cs.SentTokens)/float64(cs.RawTokens)))
		}
		badge := c.styles.Muted.Render(fmt.Sprintf("  ·  ▣ %d%%↓  ·  %d compacted · %d live", saved, cs.FrozenTurns, cs.LiveTurns))
		if cs.Compacting {
			badge = c.styles.Accent.Render("  ·  compacting…")
		}
		head += badge
	}
	return head
}
```

- [ ] **Step 5: Run the test + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextHeader -count=1`
Expected: PASS.
Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/clients/cli
git add internal/ui/context_view.go internal/ui/*_test.go
git commit -m "feat(cli): /c header shows compaction savings + frozen/live counts"
```

---

## Task 2: Summary block + frozen/live split + original toggle

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go` (`contextView` struct, `turnsLines`)
- Modify: `source/clients/cli/internal/ui/model.go` (`handleContextViewKey` `ctrl+o`)
- Test: `source/clients/cli/internal/ui/*_test.go`

**Interfaces:**
- Produces: `contextView.showOriginal bool`; `turnsLines` renders the consolidated summary + a "N compacted turns" marker + only the live tail when `!showOriginal` and frozen turns exist; renders all turns (no summary) when `showOriginal`. `Ctrl+O` toggles it.

- [ ] **Step 1: Write the failing test**

```go
func TestContextTurns_SentViewHidesFrozenShowsSummary(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "user", Kind: "text", Preview: "FROZEN-1"},
		{ID: "b", Role: "assistant", Kind: "text", Preview: "FROZEN-2"},
		{ID: "c", Role: "user", Kind: "text", Preview: "LIVE-1"},
	}
	cv.snapshot.Compaction = &agentclient.CompactionState{
		FrozenTurns: 2, LiveTurns: 1,
		ConsolidatedSummary: "[conversation summary]\nGoal: SUMMARY-GOAL",
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("sent view should show the consolidated summary")
	}
	if !strings.Contains(out, "LIVE-1") {
		t.Error("sent view should show live turns")
	}
	if strings.Contains(out, "FROZEN-1") || strings.Contains(out, "FROZEN-2") {
		t.Error("sent view must hide frozen turns (they're in the summary)")
	}

	cv.showOriginal = true
	out = stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "FROZEN-1") || strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("original view should show all turns and no summary")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextTurns_SentView -count=1`
Expected: FAIL — `showOriginal` undefined; frozen turns shown.

- [ ] **Step 3: Add the field + the split in `turnsLines`**

Add to `contextView`:

```go
	showOriginal bool
```

Replace the `default:` arm of `turnsLines`' switch with the compaction-aware split:

```go
	default:
		turns := c.snapshot.Turns
		cs := c.snapshot.Compaction
		frozenN := 0
		if !c.showOriginal && cs != nil && cs.FrozenTurns > 0 {
			frozenN = cs.FrozenTurns
			if frozenN > len(turns) {
				frozenN = len(turns)
			}
			// Consolidated summary block, then a collapsed marker for the frozen span.
			for _, line := range strings.Split(cs.ConsolidatedSummary, "\n") {
				add(c.styles.Muted.Render(line), turnLineMeta{})
			}
			add(c.styles.Dim.Render(fmt.Sprintf("  ▣ %d turns compacted into the summary above  (Ctrl+O: show original)", frozenN)), turnLineMeta{})
			add("", turnLineMeta{})
		}
		for i := frozenN; i < len(turns); i++ {
			c.appendTurn(&lines, &meta, i, turns[i])
		}
	}
```

(Keep `appendTurn`'s `i` as the turn's real index so focus/click hit-testing stays correct.)

- [ ] **Step 4: Add the `Ctrl+O` toggle**

In `handleContextViewKey` (model.go), beside `case "ctrl+r":`:

```go
	case "ctrl+o":
		// Toggle between the sent (compacted) view and the full original.
		cv.showOriginal = !cv.showOriginal
		cv.ScrollTo(0)
		return m, nil
```

- [ ] **Step 5: Run the test + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextTurns_SentView -count=1`
Expected: PASS.
Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/clients/cli
git add internal/ui/context_view.go internal/ui/model.go internal/ui/*_test.go
git commit -m "feat(cli): /c sent-view (summary + live tail) with Ctrl+O original toggle"
```

---

## Task 3: Export the full context (`Ctrl+E`)

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go` (`notice` field, render it)
- Modify: `source/clients/cli/internal/ui/model.go` (`ctrl+e`, `exportContextCmd`, `exportDoneMsg`)
- Test: `source/clients/cli/internal/ui/*_test.go`

**Interfaces:**
- Produces: `Ctrl+E` runs `exportContextCmd(agent, convID)` → writes the `ExportContext` JSON to `cercano-context-<conv8>.json` in the CWD → `exportDoneMsg{path, err}`; the handler sets `cv.notice`, shown in the header.

- [ ] **Step 1: Write the failing test (the write helper)**

```go
func TestWriteExport_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path, err := writeExport(dir, "abcdef0123456789", `[{"role":"user"}]`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("export not written: %v", err)
	}
	if !strings.Contains(string(b), `"role":"user"`) {
		t.Errorf("export content wrong: %s", b)
	}
	if !strings.Contains(path, "abcdef01") {
		t.Errorf("filename should include the conv id prefix: %s", path)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestWriteExport -count=1`
Expected: FAIL — `writeExport` undefined.

- [ ] **Step 3: Add `writeExport` + the command + the message**

In `model.go` (or `context_view.go`):

```go
// writeExport writes the export JSON to <dir>/cercano-context-<conv8>.json and
// returns the absolute path.
func writeExport(dir, convID, jsonBody string) (string, error) {
	id := convID
	if len(id) > 8 {
		id = id[:8]
	}
	path := filepath.Join(dir, "cercano-context-"+id+".json")
	if err := os.WriteFile(path, []byte(jsonBody), 0o644); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs, nil
	}
	return path, nil
}

type exportDoneMsg struct {
	path string
	err  error
}

func exportContextCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		body, err := ag.ExportContext(ctx, convID)
		if err != nil {
			return exportDoneMsg{err: err}
		}
		dir, _ := os.Getwd()
		path, err := writeExport(dir, convID, body)
		return exportDoneMsg{path: path, err: err}
	}
}
```

(Add imports as needed: `os`, `path/filepath` in the file you place these in.)

- [ ] **Step 4: Wire the keybind + the handler + the notice**

In `handleContextViewKey`, beside `ctrl+o`:

```go
	case "ctrl+e":
		return m, exportContextCmd(cv.agent, cv.convID)
```

Add a model-level `case exportDoneMsg:` in `Update` that sets the notice on the active `contextView`:

```go
	case exportDoneMsg:
		if cv, ok := m.content.(*contextView); ok {
			if msg.err != nil {
				cv.notice = "export failed: " + msg.err.Error()
			} else {
				cv.notice = "exported full context → " + msg.path
			}
		}
		return m, nil
```

Add `notice string` to `contextView`, and render it in `renderHeader` (append when non-empty):

```go
	if c.notice != "" {
		head += "\n" + c.styles.Accent.Render(c.notice)
	}
```

- [ ] **Step 5: Run the test + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestWriteExport -count=1`
Expected: PASS.
Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd source/clients/cli
git add internal/ui/context_view.go internal/ui/model.go internal/ui/*_test.go
git commit -m "feat(cli): /c Ctrl+E exports the full uncapped context to a JSON file"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b3-visibility-design.md` §2 `/c`):
- Status line (sent/raw/savings/frozen/live) → Task 1 (`renderHeader`). ✓
- Consolidated summary block → Task 2. ✓
- Frozen collapsed / live verbatim split → Task 2 (by `FrozenTurns` count). ✓
- Toggle to full original → Task 2 (`Ctrl+O`). ✓
- Export to JSON → Task 3 (`Ctrl+E`). ✓
- No-compaction renders as today → Task 2 (`frozenN` stays 0 → all turns, no summary). ✓
- Ctrl-modified keys (no prompt-bar conflict) → Tasks 2, 3. ✓

**Placeholder scan:** none — concrete code throughout. Tests reuse `expandTestView()` (existing) + `stripAnsiCSI`.

**Type consistency:** `agentclient.CompactionState` fields (`FrozenTurns`/`LiveTurns`/`RawTokens`/`SentTokens`/`ConsolidatedSummary`/`Compacting`) used identically in `renderHeader` (Task 1) and `turnsLines` (Task 2). `showOriginal` set by `Ctrl+O` (Task 2), read in `turnsLines` (Task 2). `exportDoneMsg{path,err}` produced by `exportContextCmd` (Task 3) and consumed by the `Update` handler (Task 3). `notice` set in Task 3, rendered in Task 1's `renderHeader`.

**Deferred:** plain-text export (JSON now); an explicit "compact now" keybind (background handles it).
