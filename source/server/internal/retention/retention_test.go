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
