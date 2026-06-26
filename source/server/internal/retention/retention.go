// Package retention bounds DB growth: it stubs aged frozen raw turn bodies and
// collapses long-dead conversations to their identity stub, on a background
// schedule. The policy (Sweep) is pure given an injected clock; Start drives it.
package retention

import (
	"context"
	"fmt"
	"os"
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
	var collapsed, prunedBodies int
	for _, info := range infos {
		if info.LastTurnAt.Before(collapseBefore) {
			if err := s.store.CollapseConversation(ctx, info.ID); err == nil {
				collapsed++
			}
			continue
		}
		comp, err := s.store.GetCompaction(ctx, info.ID)
		if err != nil || comp.FrozenThrough == 0 {
			continue // never compacted → nothing frozen to prune
		}
		if n, err := s.store.PruneRawBodies(ctx, info.ID, rawCutoff, comp.FrozenThrough); err == nil {
			prunedBodies += n
		}
	}
	// Visibility for a destructive operation: one line per sweep when it acted.
	if collapsed > 0 || prunedBodies > 0 {
		fmt.Fprintf(os.Stderr, "[retention] sweep: collapsed %d conversation(s), pruned %d raw body(ies)\n", collapsed, prunedBodies)
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
