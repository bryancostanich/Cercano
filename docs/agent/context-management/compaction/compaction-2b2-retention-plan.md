# Compaction 2b-2 — Retention Enforcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound DB growth with an age-based retention sweep — stub frozen raw bodies past 90 days, collapse conversations dead >180 days to a title+recap identity stub — driven by a 12h background sweeper, with a keep-forever escape hatch.

**Architecture:** Two idempotent `conversation.Store` ops (`PruneRawBodies`, `CollapseConversation`), a pure policy (`retention.Sweeper.Sweep(ctx, now)`) testable with a fake store + injected clock, and a background `Start` loop wired in `main.go`. A `Retention` config block holds the knobs.

**Tech Stack:** Go; the `conversation` store; the recap/compaction generator pattern.

## Global Constraints

- All server-side. The sweep runs off the request path; failures are swallowed per conversation and never block a turn.
- Only **frozen** turns (`created_at <= frozen_through`) are eligible for the 90-day raw stub — un-summarized/recent raw is never touched.
- Both store ops are **idempotent** (re-running prunes/deletes nothing new).
- The `conversations` row (title + recap) is NEVER deleted by retention — collapse removes only its turns + compaction row.
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/pkg/config/config.go` — `RetentionConfig` + `CompactionConfig.Retention` + defaults.
- `source/server/internal/conversation/store.go` — `PrunedBodyStub` const; `PruneRawBodies`; `CollapseConversation`; interface entries.
- `source/server/internal/retention/retention.go` — `Config`, `Store`, `Sweeper` (`New`/`Sweep`/`Start`).
- `source/server/internal/retention/retention_test.go`.
- `source/server/cmd/cercano/main.go` — construct + start the sweeper.

---

## Task 1: Config — `Retention` block

**Files:**
- Modify: `source/server/pkg/config/config.go`
- Test: `source/server/pkg/config/config_test.go`

**Interfaces:**
- Produces: `config.RetentionConfig{ RawRetentionDays, CompactedRetentionDays int; KeepForever bool }` and `CompactionConfig.Retention RetentionConfig`, defaulted to `{90, 180, false}`.

- [ ] **Step 1: Write the failing test**

Append to `config_test.go`:

```go
func TestDefaults_Retention(t *testing.T) {
	r := Defaults().Compaction.Retention
	if r.RawRetentionDays != 90 || r.CompactedRetentionDays != 180 || r.KeepForever {
		t.Errorf("retention defaults = %+v, want {90,180,false}", r)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./pkg/config/ -run TestDefaults_Retention -count=1`
Expected: FAIL — `Retention` undefined.

- [ ] **Step 3: Add the struct + field + defaults**

Add the struct (near `CompactionConfig`):

```go
// RetentionConfig bounds how long raw turn bodies and the compacted layer are
// kept. CompactedRetentionDays should be >= RawRetentionDays.
type RetentionConfig struct {
	RawRetentionDays       int  `yaml:"raw_retention_days"`
	CompactedRetentionDays int  `yaml:"compacted_retention_days"`
	KeepForever            bool `yaml:"keep_forever"`
}
```

Add to `CompactionConfig`:

```go
	Retention RetentionConfig `yaml:"retention"`
```

In `Defaults()`, inside the `Compaction: CompactionConfig{...}` literal:

```go
			Retention: RetentionConfig{
				RawRetentionDays:       90,
				CompactedRetentionDays: 180,
				KeepForever:            false,
			},
```

- [ ] **Step 4: Run to verify pass**

Run: `cd source/server && go test ./pkg/config/ -run TestDefaults_Retention -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(server): RetentionConfig (raw 90d / compacted 180d / keep-forever)"
```

---

## Task 2: Store — `PruneRawBodies` + `CollapseConversation`

**Files:**
- Modify: `source/server/internal/conversation/store.go`
- Test: `source/server/internal/conversation/retention_store_test.go`

**Interfaces:**
- Produces:
  - `conversation.PrunedBodyStub` (exported const).
  - `Store.PruneRawBodies(ctx, conversationID string, beforeUnix, frozenThrough int64) (int, error)` — stubs the `content`/`content_json` of frozen turns older than the cutoff; returns rows changed.
  - `Store.CollapseConversation(ctx, conversationID string) error` — deletes the compaction row + all turns, keeps the `conversations` row.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/conversation/retention_store_test.go`:

```go
package conversation

import (
	"context"
	"testing"
	"time"
)

func appendAt(t *testing.T, s Store, conv, id, content string, at int64) {
	if err := s.Append(context.Background(), Turn{
		ID: id, ConversationID: conv, Role: "user", Content: content,
		BlocksJSON: `[{"type":"text"}]`, CreatedAt: time.Unix(at, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRawBodies_OnlyFrozenAndOld(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "/p", "m")
	appendAt(t, s, "c1", "old", "OLD BIG BODY", 100)    // frozen + old → stub
	appendAt(t, s, "c1", "recentFrozen", "KEEP-A", 250) // frozen but NOT old → keep
	appendAt(t, s, "c1", "live", "KEEP-B", 400)         // not frozen → keep

	// frozenThrough=300 (old+recentFrozen frozen), cutoff before=200 (only old).
	n, err := s.PruneRawBodies(ctx, "c1", 200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	turns, _ := s.GetTurns(ctx, "c1")
	got := map[string]Turn{}
	for _, tn := range turns {
		got[tn.ID] = tn
	}
	if got["old"].Content != PrunedBodyStub || got["old"].BlocksJSON != "" {
		t.Errorf("old turn not stubbed: %+v", got["old"])
	}
	if got["recentFrozen"].Content != "KEEP-A" || got["live"].Content != "KEEP-B" {
		t.Error("recent/un-frozen turns must be kept verbatim")
	}
	// Idempotent: a second run prunes nothing.
	if n2, _ := s.PruneRawBodies(ctx, "c1", 200, 300); n2 != 0 {
		t.Errorf("second prune should be a no-op, got %d", n2)
	}
}

func TestCollapseConversation_KeepsIdentityDropsRest(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "/p", "m")
	appendAt(t, s, "c1", "t1", "body", 100)
	_ = s.UpdateRecap(ctx, "c1", "the recap")
	_ = s.SaveCompaction(ctx, Compaction{ConversationID: "c1", FrozenThrough: 100, ConsolidatedJSON: `{"Goal":"g"}`})

	if err := s.CollapseConversation(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	turns, _ := s.GetTurns(ctx, "c1")
	if len(turns) != 0 {
		t.Errorf("collapse should delete all turns, got %d", len(turns))
	}
	comp, _ := s.GetCompaction(ctx, "c1")
	if comp.ConsolidatedJSON != "" {
		t.Error("collapse should delete the compaction row")
	}
	info, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("conversation row must survive: %v", err)
	}
	if info.Recap != "the recap" {
		t.Errorf("identity recap must survive, got %q", info.Recap)
	}
	// Idempotent.
	if err := s.CollapseConversation(ctx, "c1"); err != nil {
		t.Errorf("second collapse should be a no-op, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/conversation/ -run 'TestPruneRawBodies|TestCollapseConversation' -count=1`
Expected: FAIL — `PrunedBodyStub` / methods undefined.

- [ ] **Step 3: Add the const + interface entries + implementations**

Add the const near the top of `store.go`:

```go
// PrunedBodyStub replaces a frozen turn's content when it ages out of raw
// retention; the consolidated summary carries the substance.
const PrunedBodyStub = "[pruned after 90 days — see summary]"
```

Add to the `Store` interface (near `DeleteTurns`):

```go
	// PruneRawBodies stubs the content of frozen turns (created_at <=
	// frozenThrough) older than beforeUnix; returns the number changed.
	PruneRawBodies(ctx context.Context, conversationID string, beforeUnix, frozenThrough int64) (int, error)
	// CollapseConversation deletes the compaction row and all turns, keeping the
	// conversations identity row (title + recap).
	CollapseConversation(ctx context.Context, conversationID string) error
```

Implement on `sqliteStore` (near `DeleteTurns`):

```go
func (s *sqliteStore) PruneRawBodies(ctx context.Context, conversationID string, beforeUnix, frozenThrough int64) (int, error) {
	if conversationID == "" {
		return 0, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		UPDATE turns SET content = ?, content_json = ''
		WHERE conversation_id = ? AND created_at <= ? AND created_at < ? AND content != ?`,
		PrunedBodyStub, conversationID, frozenThrough, beforeUnix, PrunedBodyStub)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *sqliteStore) CollapseConversation(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_compaction WHERE conversation_id = ?`, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM turns WHERE conversation_id = ?`, conversationID); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run the tests + full build**

Run: `cd source/server && go test ./internal/conversation/ -run 'TestPruneRawBodies|TestCollapseConversation' -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS (agent/server tests use the real store; the widened interface is satisfied — no hand-written mock).

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/conversation/store.go internal/conversation/retention_store_test.go
git commit -m "feat(server): PruneRawBodies + CollapseConversation store ops (idempotent)"
```

---

## Task 3: The sweeper + policy

**Files:**
- Create: `source/server/internal/retention/retention.go`
- Test: `source/server/internal/retention/retention_test.go`

**Interfaces:**
- Produces:
  - `Config{ RawRetentionDays, CompactedRetentionDays int; KeepForever bool }`.
  - `Store` interface: `List(ctx, projectDir string, limit int) ([]conversation.Info, error)`, `GetCompaction(ctx, convID string) (conversation.Compaction, error)`, `PruneRawBodies(ctx, convID string, beforeUnix, frozenThrough int64) (int, error)`, `CollapseConversation(ctx, convID string) error`.
  - `Sweeper` with `New(store Store, cfg Config, interval time.Duration) *Sweeper`, `Sweep(ctx context.Context, now time.Time)` (pure policy), `Start(ctx context.Context)` (initial delay + ticker).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/retention/retention_test.go`:

```go
package retention

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
)

type fakeStore struct {
	infos     []conversation.Info
	comp      map[string]conversation.Compaction
	pruned    map[string][2]int64 // convID -> {beforeUnix, frozenThrough}
	collapsed []string
}

func (f *fakeStore) List(context.Context, string, int) ([]conversation.Info, error) {
	return f.infos, nil
}
func (f *fakeStore) GetCompaction(_ context.Context, id string) (conversation.Compaction, error) {
	return f.comp[id], nil
}
func (f *fakeStore) PruneRawBodies(_ context.Context, id string, before, frozen int64) (int, error) {
	if f.pruned == nil {
		f.pruned = map[string][2]int64{}
	}
	f.pruned[id] = [2]int64{before, frozen}
	return 1, nil
}
func (f *fakeStore) CollapseConversation(_ context.Context, id string) error {
	f.collapsed = append(f.collapsed, id)
	return nil
}

func TestSweep_CollapsesDeadStubsAged(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	day := int64(86400)
	fs := &fakeStore{
		infos: []conversation.Info{
			{ID: "dead", LastTurnAt: now.Add(-200 * 24 * time.Hour)},     // >180d → collapse
			{ID: "aging", LastTurnAt: now.Add(-100 * 24 * time.Hour)},    // 100d, compacted → prune
			{ID: "fresh", LastTurnAt: now.Add(-3 * 24 * time.Hour)},      // fresh → neither
			{ID: "small", LastTurnAt: now.Add(-100 * 24 * time.Hour)},    // old but never compacted → neither (prune)
		},
		comp: map[string]conversation.Compaction{
			"aging": {ConversationID: "aging", FrozenThrough: 555},
			// "small" has no compaction row → zero value, FrozenThrough 0.
		},
	}
	cfg := Config{RawRetentionDays: 90, CompactedRetentionDays: 180}
	New(fs, cfg, time.Hour).Sweep(context.Background(), now)

	if len(fs.collapsed) != 1 || fs.collapsed[0] != "dead" {
		t.Errorf("expected only 'dead' collapsed, got %v", fs.collapsed)
	}
	if _, ok := fs.pruned["aging"]; !ok {
		t.Error("expected 'aging' raw-pruned")
	}
	got := fs.pruned["aging"]
	if got[1] != 555 {
		t.Errorf("prune frozenThrough = %d, want 555", got[1])
	}
	if got[0] != now.Add(-90*24*time.Hour).Unix() {
		t.Errorf("prune cutoff = %d, want now-90d", got[0])
	}
	if _, ok := fs.pruned["small"]; ok {
		t.Error("'small' (never compacted) must not be pruned")
	}
	if _, ok := fs.pruned["fresh"]; ok {
		t.Error("'fresh' must not be touched")
	}
	_ = day
}

func TestSweep_KeepForeverDoesNothing(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	fs := &fakeStore{infos: []conversation.Info{{ID: "dead", LastTurnAt: now.Add(-500 * 24 * time.Hour)}}}
	New(fs, Config{RawRetentionDays: 90, CompactedRetentionDays: 180, KeepForever: true}, time.Hour).
		Sweep(context.Background(), now)
	if len(fs.collapsed) != 0 || len(fs.pruned) != 0 {
		t.Error("keep_forever must disable all aging")
	}
}

func TestSweep_ZeroHorizonDoesNothing(t *testing.T) {
	// A misconfigured 0-day horizon must NOT nuke everything — it disables aging.
	now := time.Unix(1_000_000_000, 0)
	fs := &fakeStore{infos: []conversation.Info{{ID: "dead", LastTurnAt: now.Add(-500 * 24 * time.Hour)}}}
	New(fs, Config{RawRetentionDays: 0, CompactedRetentionDays: 0}, time.Hour).Sweep(context.Background(), now)
	if len(fs.collapsed) != 0 || len(fs.pruned) != 0 {
		t.Error("a non-positive retention horizon must disable aging, not prune everything")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/retention/ -count=1`
Expected: FAIL — package/`New`/`Sweep` undefined.

- [ ] **Step 3: Create `retention.go`**

```go
// Package retention bounds DB growth: it stubs aged frozen raw turn bodies and
// collapses long-dead conversations to their identity stub, on a background
// schedule. The policy (Sweep) is pure given an injected clock; Start drives it.
package retention

import (
	"context"
	"time"

	"cercano/source/server/internal/conversation"
)

// Config holds the retention horizons.
type Config struct {
	RawRetentionDays       int
	CompactedRetentionDays int
	KeepForever            bool
}

// Store is the subset of conversation.Store the sweeper needs.
type Store interface {
	List(ctx context.Context, projectDir string, limit int) ([]conversation.Info, error)
	GetCompaction(ctx context.Context, conversationID string) (conversation.Compaction, error)
	PruneRawBodies(ctx context.Context, conversationID string, beforeUnix, frozenThrough int64) (int, error)
	CollapseConversation(ctx context.Context, conversationID string) error
}

// Sweeper applies the retention policy on a schedule.
type Sweeper struct {
	store    Store
	cfg      Config
	interval time.Duration
}

func New(store Store, cfg Config, interval time.Duration) *Sweeper {
	return &Sweeper{store: store, cfg: cfg, interval: interval}
}

// Sweep applies the policy once, as of now. Pure w.r.t. time (now injected).
// Per-conversation errors are swallowed so one bad row never aborts the sweep.
func (s *Sweeper) Sweep(ctx context.Context, now time.Time) {
	// Safety clamp: keep-forever, or any non-positive horizon (a misconfig — e.g.
	// a config that zeroed the retention block), disables ALL aging. A 0-day
	// horizon would otherwise collapse/prune everything immediately, so the safe
	// failure mode is to do nothing.
	if s.cfg.KeepForever || s.cfg.RawRetentionDays <= 0 || s.cfg.CompactedRetentionDays <= 0 {
		return
	}
	infos, err := s.store.List(ctx, "", 0)
	if err != nil {
		return
	}
	collapseBefore := now.Add(-time.Duration(s.cfg.CompactedRetentionDays) * 24 * time.Hour)
	rawCutoff := now.Add(-time.Duration(s.cfg.RawRetentionDays) * 24 * time.Hour).Unix()
	for _, info := range infos {
		if info.LastTurnAt.Before(collapseBefore) {
			_ = s.store.CollapseConversation(ctx, info.ID)
			continue
		}
		comp, err := s.store.GetCompaction(ctx, info.ID)
		if err != nil || comp.FrozenThrough == 0 {
			continue // never compacted → nothing frozen to prune
		}
		_, _ = s.store.PruneRawBodies(ctx, info.ID, rawCutoff, comp.FrozenThrough)
	}
}

// Start runs an initial sweep shortly after startup, then every interval, until
// ctx is cancelled. Non-blocking.
func (s *Sweeper) Start(ctx context.Context) {
	go func() {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return
		}
		s.Sweep(ctx, time.Now())
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.Sweep(ctx, time.Now())
			case <-ctx.Done():
				return
			}
		}
	}()
}
```

- [ ] **Step 4: Run the tests**

Run: `cd source/server && go test ./internal/retention/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd source/server
git add internal/retention/retention.go internal/retention/retention_test.go
git commit -m "feat(server): retention sweeper — age-based policy (collapse 180d / stub-raw 90d)"
```

---

## Task 4: Wire the sweeper in `main.go`

**Files:**
- Modify: `source/server/cmd/cercano/main.go`

**Interfaces:**
- Consumes: `retention.New`/`Start`, `cfg.Compaction.Retention`, `persistentStore`.

- [ ] **Step 1: Add the wiring**

Beside the compaction-generator block (`if persistentStore != nil && cfg.Compaction.Enabled { … }`), add a retention block gated only on the persistent store (retention runs regardless of whether compaction is enabled; the sweeper itself honors `keep_forever`):

```go
	if persistentStore != nil {
		sweeper := retention.New(persistentStore, retention.Config{
			RawRetentionDays:       cfg.Compaction.Retention.RawRetentionDays,
			CompactedRetentionDays: cfg.Compaction.Retention.CompactedRetentionDays,
			KeepForever:            cfg.Compaction.Retention.KeepForever,
		}, 12*time.Hour)
		sweeper.Start(ctx)
	}
```

Use the server's existing root `ctx` (the one cancelled on shutdown) for `Start`; if none is in scope at that point, use `context.Background()`. Add the import `cercano/source/server/internal/retention`.

- [ ] **Step 2: Build + vet + full suite**

Run: `cd source/server && go build ./... && go vet ./cmd/cercano/ && go test ./... -count=1`
Expected: builds clean; all green. (No unit test for `main`; the sweeper policy is tested in Task 3.)

- [ ] **Step 3: Commit**

```bash
cd source/server
git add cmd/cercano/main.go
git commit -m "feat(server): start the retention sweeper (12h cadence)"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b2-retention-design.md`):
- 12h background sweeper (startup + ticker, off request path) → Task 3 (`Start`) + Task 4 (12h). ✓
- 180d collapse → identity stub → Task 2 (`CollapseConversation`) + Task 3 (policy). ✓
- 90d frozen-raw stub → Task 2 (`PruneRawBodies`) + Task 3 (policy). ✓
- Only frozen turns pruned; never-compacted skipped → Task 3 (`FrozenThrough == 0` skip) + Task 2 (`created_at <= frozenThrough`). ✓
- keep-forever no-op → Task 3 (`Sweep` early return) + Task 1 (config). ✓
- Idempotent ops → Task 2 (`content != stub` guard; delete is naturally idempotent) + tests. ✓
- Config block → Task 1. ✓
- Identity row survives collapse → Task 2 (deletes turns + compaction row only) + test. ✓

**Placeholder scan:** none — every step has complete code. (Task 4 notes "use the root ctx if in scope, else context.Background()" — a concrete instruction to match the real main.go, not a placeholder.)

**Type consistency:** `RetentionConfig` fields (Task 1) map 1:1 into `retention.Config` (Task 4). `retention.Store` (Task 3) is a subset of `conversation.Store` satisfied by the real store (Tasks 2 adds the two methods). `PruneRawBodies`/`CollapseConversation` signatures identical across the interface (Task 2), the `retention.Store` interface (Task 3), and the fake (Task 3 test). `PrunedBodyStub` defined once (Task 2), asserted in the Task 2 test.

**Deferred:** per-conversation pin; size-based LRU.
