# Recap Upgrade + Auto-Title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Widen the living recap to 160 chars and auto-generate a local-model session title (derived from the recap) for untitled sessions, all in the agent layer.

**Architecture:** Both changes are server-side. The conversation store gains a `title_source` column ('auto' | 'user') so a user-renamed title is never clobbered by generation. The recap generator (already debounced, off the request path) bumps its cap and, after writing a recap, derives a short title from it via the same local `CompleteFunc` and writes it through a new `SetGeneratedTitle` store method that updates only non-user titles. Clients keep rendering `Info.Title` / `Info.Recap` unchanged — no client work.

**Tech Stack:** Go; `modernc.org/sqlite` (pure-Go, no cgo); the existing `recap` and `conversation` packages.

## Global Constraints

- All logic lives in `source/server/internal/` (agent layer). Clients only render `Info.Title` / `Info.Recap` via existing RPCs — **no client changes**.
- A **user-renamed** title is NEVER overwritten by generation. Legacy titles (predating this change) are treated as user titles (protected), because they cannot be reclassified post-hoc.
- SQLite `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS`; migrations probe or ignore the "duplicate column" error (follow the existing pattern in `store.go`).
- Generation failures are swallowed (never block a turn or a recap), mirroring the existing recap behavior.
- Recap stays a single line. Title is a single short line.
- Build both: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain the word "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/internal/conversation/schema.sql` — add `title_source` column to the `conversations` CREATE TABLE.
- `source/server/internal/conversation/store.go` — migration for the new column; `EnsureConversation` writes `title_source='auto'`; `Rename` sets `title_source='user'`; new `SetGeneratedTitle` method; add `SetGeneratedTitle` to the `Store` interface.
- `source/server/internal/conversation/store_test.go` — tests for the column/method behavior.
- `source/server/internal/recap/recap.go` — `maxRecapChars` 80→160; new `maxTitleChars`; `Store` interface gains `SetGeneratedTitle`; `regenerate` derives + writes a title; `buildTitlePrompt`.
- `source/server/internal/recap/recap_test.go` — update `fakeStore` to implement `SetGeneratedTitle`; tests for title generation + the 160 cap.

No `main.go` change: `recap.New(persistentStore, …)` already passes the concrete store, which will satisfy the widened interfaces once Task 1 lands.

---

## Task 1: Store — `title_source` column + `SetGeneratedTitle`

**Files:**
- Modify: `source/server/internal/conversation/schema.sql` (conversations CREATE TABLE)
- Modify: `source/server/internal/conversation/store.go` (migration list ~line 144; `EnsureConversation` ~line 211; `Rename` ~line 378; `Store` interface ~line 78; add `SetGeneratedTitle`)
- Test: `source/server/internal/conversation/store_test.go`

**Interfaces:**
- Produces: `Store.SetGeneratedTitle(ctx context.Context, conversationID, title string) error` — sets `title` (and `title_source='auto'`) only when `title_source != 'user'`; a no-op on user-renamed conversations. Used by the recap generator in Task 2.

- [ ] **Step 1: Write the failing test**

Add to `source/server/internal/conversation/store_test.go`:

```go
func TestSetGeneratedTitle_SetsAutoTitle(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/proj", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGeneratedTitle(ctx, "c1", "Fix The Scrollbar"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Fix The Scrollbar" {
		t.Errorf("title = %q, want %q", info.Title, "Fix The Scrollbar")
	}
}

func TestSetGeneratedTitle_NeverOverwritesUserRename(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/proj", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "c1", "My Title"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGeneratedTitle(ctx, "c1", "Generated Title"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Get(ctx, "c1")
	if info.Title != "My Title" {
		t.Errorf("user title was overwritten: got %q, want %q", info.Title, "My Title")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd source/server && go test ./internal/conversation/ -run TestSetGeneratedTitle -count=1`
Expected: FAIL — `s.SetGeneratedTitle undefined` (build error).

- [ ] **Step 3: Add the column to `schema.sql`**

In `source/server/internal/conversation/schema.sql`, add the column to the `conversations` table (after `model`):

```sql
    model        TEXT NOT NULL DEFAULT '',
    title_source TEXT NOT NULL DEFAULT 'user',
    started_at   INTEGER NOT NULL,
```

(Default `'user'` is conservative: on a fresh table the default never applies to real rows because `EnsureConversation` writes `'auto'` explicitly; the default exists only so legacy rows added by migration are protected.)

- [ ] **Step 4: Add the migration in `store.go`**

In `Open`, extend the existing migration loop (the one adding the recap columns, ~line 144) to also add `title_source`:

```go
	for _, alter := range []string{
		`ALTER TABLE conversations ADD COLUMN recap TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN recap_updated_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE conversations ADD COLUMN title_source TEXT NOT NULL DEFAULT 'user'`,
	} {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate conversation columns: %w", err)
		}
	}
```

- [ ] **Step 5: `EnsureConversation` writes `title_source='auto'`**

Update the INSERT in `EnsureConversation` (~line 211) so new conversations are generation-eligible:

```go
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, project_dir, model, title_source, started_at, last_turn_at)
		VALUES (?, ?, ?, 'auto', ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, projectDir, model, now, now)
	return err
```

- [ ] **Step 6: `Rename` sets `title_source='user'`**

Update `Rename` (~line 378):

```go
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ?, title_source = 'user' WHERE id = ?`,
		title, conversationID)
	return err
```

- [ ] **Step 7: Add `SetGeneratedTitle` to the `Store` interface**

In the `Store` interface (~line 78, near `Rename`):

```go
	// SetGeneratedTitle sets an auto-generated title, but only when the
	// conversation's title was not user-chosen (title_source != 'user').
	// A no-op for user-renamed conversations. Idempotent.
	SetGeneratedTitle(ctx context.Context, conversationID, title string) error
```

- [ ] **Step 8: Implement `SetGeneratedTitle` on `sqliteStore`**

Add near `Rename` in `store.go`:

```go
func (s *sqliteStore) SetGeneratedTitle(ctx context.Context, conversationID, title string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ? WHERE id = ? AND title_source != 'user'`,
		title, conversationID)
	return err
}
```

- [ ] **Step 9: Run the tests + full build**

Run: `cd source/server && go test ./internal/conversation/ -run TestSetGeneratedTitle -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS (no mock implements `conversation.Store` by hand — the agent/server tests use the real `Open(":memory:")` store — but this confirms it).

- [ ] **Step 10: Commit**

```bash
cd source/server
git add internal/conversation/schema.sql internal/conversation/store.go internal/conversation/store_test.go
git commit -m "feat(server): conversation title_source + SetGeneratedTitle

Adds a title_source column ('auto' | 'user') so an auto-generated title
never overwrites a user rename. New conversations are 'auto'; Rename sets
'user'; legacy rows default to 'user' (protected). SetGeneratedTitle
updates the title only when title_source != 'user'."
```

---

## Task 2: Recap — 160-char cap + title generation from the recap

**Files:**
- Modify: `source/server/internal/recap/recap.go` (`maxRecapChars`; add `maxTitleChars`; `Store` interface; `regenerate`; add `buildTitlePrompt`)
- Test: `source/server/internal/recap/recap_test.go` (update `fakeStore`; add tests)

**Interfaces:**
- Consumes: `Store.SetGeneratedTitle(ctx, conversationID, title string) error` (Task 1).
- Produces: nothing for later tasks (terminal task).

- [ ] **Step 1: Write the failing tests**

First extend the existing `fakeStore` in `source/server/internal/recap/recap_test.go` to implement the new interface method and capture generated titles. Add the field and method:

```go
// add to fakeStore struct:
//   titles  []string
//   titleCh chan string

func (f *fakeStore) SetGeneratedTitle(ctx context.Context, id, title string) error {
	f.mu.Lock()
	f.titles = append(f.titles, title)
	f.mu.Unlock()
	if f.titleCh != nil {
		f.titleCh <- title
	}
	return nil
}
```

Then add the test:

```go
func TestGeneratesTitleFromRecap(t *testing.T) {
	// The completion returns the recap line first, then the title on the
	// second call (recap pass, then title pass).
	var n int
	var cmu sync.Mutex
	complete := func(ctx context.Context, prompt string) (string, error) {
		cmu.Lock()
		defer cmu.Unlock()
		n++
		if n == 1 {
			return "implemented the context viewer and fixed the scrollbar", nil
		}
		return "Context Viewer Scrollbar Fix", nil
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}},
		updated: make(chan string, 4),
		titleCh: make(chan string, 4),
	}
	g := New(fs, complete, 10*time.Millisecond, 10)
	g.Schedule("c1")

	select {
	case got := <-fs.titleCh:
		if got != "Context Viewer Scrollbar Fix" {
			t.Errorf("title = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no title generated")
	}
}

func TestRecapCapIs160(t *testing.T) {
	if maxRecapChars != 160 {
		t.Errorf("maxRecapChars = %d, want 160", maxRecapChars)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd source/server && go test ./internal/recap/ -run 'TestGeneratesTitleFromRecap|TestRecapCapIs160' -count=1`
Expected: FAIL — `fakeStore` does not satisfy `Store` (missing `SetGeneratedTitle` on the interface) and `maxRecapChars` is still 80. (If the build error blocks both, that is the expected red.)

- [ ] **Step 3: Bump the recap cap**

In `source/server/internal/recap/recap.go`:

```go
const (
	maxRecapChars = 160
	maxTitleChars = 48
	genTimeout    = 30 * time.Second
)
```

- [ ] **Step 4: Add `SetGeneratedTitle` to the recap `Store` interface**

```go
type Store interface {
	Get(ctx context.Context, conversationID string) (conversation.Info, error)
	GetTurns(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	UpdateRecap(ctx context.Context, conversationID, recap string) error
	SetGeneratedTitle(ctx context.Context, conversationID, title string) error
}
```

- [ ] **Step 5: Generate the title in `regenerate`**

After the recap is stored in `regenerate` (after `g.store.UpdateRecap(...)`), derive and write a title from that recap line:

```go
	_ = g.store.UpdateRecap(ctx, conversationID, line)

	// Derive a short session title from the fresh recap (next-tightest rung).
	// Best-effort: failures leave the existing title untouched.
	titleOut, err := g.complete(ctx, buildTitlePrompt(line))
	if err != nil {
		return
	}
	title := firstLine(titleOut, maxTitleChars)
	if title == "" {
		return
	}
	_ = g.store.SetGeneratedTitle(ctx, conversationID, title)
```

- [ ] **Step 6: Add `buildTitlePrompt`**

```go
// buildTitlePrompt turns a one-line recap into a short session title.
func buildTitlePrompt(recap string) string {
	return fmt.Sprintf(
		"Given this one-line summary of a coding session, write a short title "+
			"of 3 to 6 words in Title Case, with no quotes and no trailing "+
			"punctuation.\nSummary: %s\nOutput ONLY the title.",
		recap)
}
```

- [ ] **Step 7: Run the tests**

Run: `cd source/server && go test ./internal/recap/ -run 'TestGeneratesTitleFromRecap|TestRecapCapIs160' -count=1`
Expected: PASS.

- [ ] **Step 8: Run the full recap suite + module build**

Run: `cd source/server && go test ./internal/recap/ -count=1`
Expected: PASS (the existing debounce/recap tests still pass; their `complete` returns a constant, which now also serves as the title — harmless).
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
cd source/server
git add internal/recap/recap.go internal/recap/recap_test.go
git commit -m "feat(server): widen recap to 160 chars + auto-generate session titles

Bumps the living-recap cap 80->160 and, after each recap pass, derives a
short Title-Case session title from the recap via the same local model,
writing it through SetGeneratedTitle (which never clobbers a user rename).
Best-effort: title-generation failures leave the existing title intact."
```

---

## Self-Review

**Spec coverage** (against `recap-title-design.md`):
- §1 Recap 80→160 → Task 2 Steps 3, 7 (`TestRecapCapIs160`). ✓
- §2 Auto-title from the recap, off the request path, piggybacking recap regeneration → Task 2 Step 5. ✓
- §2 User title never overwritten; auto/user distinction → Task 1 (`title_source`, `SetGeneratedTitle` guard, `Rename`). ✓
- §2 Skip when no recap → Task 2: `regenerate` already returns early when `line == ""` before the title block. ✓
- §2 Display in `-r` → already wired (`history_view.go` renders `c.Title`); no task needed (Global Constraints: no client changes). ✓
- §4 edge cases (no recap / model fail / user renamed / empty output) → Task 2 Step 5 best-effort returns + Task 1 guard. ✓
- §5 testing (fake `CompleteFunc`; width; never-overwrite; display) → Tasks 1–2 tests; display covered by existing CLI behavior. ✓
- Layering (agent-owned) → both tasks server-side; no client work. ✓

**Placeholder scan:** none — every step has concrete code/commands.

**Type consistency:** `SetGeneratedTitle(ctx, conversationID, title string) error` is identical in the `conversation.Store` interface (Task 1 Step 7), the `sqliteStore` impl (Task 1 Step 8), the `recap.Store` interface (Task 2 Step 4), and the `fakeStore` impl (Task 2 Step 1). `maxRecapChars`/`maxTitleChars`/`firstLine`/`buildTitlePrompt` all exist in `recap.go`. ✓
