package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DispatchEvent is one automatically appended dispatch-evidence record: a
// durable, append-only trace of what a dispatch loop did, written as it
// happens instead of reconstructed from a one-shot file after the fact.
//
// Seq is caller-assigned and monotonic within a conversation; the store
// rejects a duplicate (conversation_id, seq) rather than overwriting the
// earlier snapshot, so history is immutable once written. PayloadJSON stays
// opaque to the store — event producers own its structure.
type DispatchEvent struct {
	ConversationID string
	Seq            int64 // monotonic per-conversation dispatch sequence, >= 1
	Kind           string
	Iteration      int
	Timestamp      time.Time
	PayloadJSON    string
}

// ErrDispatchEventDuplicate is returned (wrapped) by AppendDispatchEvent when
// an event with the same (conversation_id, seq) already exists. Callers can
// errors.Is against it to distinguish a replay from a storage failure.
var ErrDispatchEventDuplicate = errors.New("dispatch event already exists")

// DispatchEventStore is the narrow seam for automatic dispatch evidence. It is
// deliberately separate from Store so hosts and fakes can implement just this
// slice without carrying the full conversation surface. sqliteStore implements
// it; the compile-time assertion below fails the build on signature drift.
type DispatchEventStore interface {
	// AppendDispatchEvent appends one dispatch event. Validates conversation
	// id, kind, seq, and payload JSON; returns ErrDispatchEventDuplicate
	// (wrapped) if (conversation_id, seq) already exists — earlier snapshots
	// are never overwritten.
	AppendDispatchEvent(ctx context.Context, ev DispatchEvent) error
	// ListDispatchEvents returns the conversation's dispatch events ordered by
	// seq ascending. Unknown conversations yield an empty slice, not an error.
	ListDispatchEvents(ctx context.Context, conversationID string) ([]DispatchEvent, error)
}

var _ DispatchEventStore = (*sqliteStore)(nil)

// AppendDispatchEvent validates and appends one dispatch event. Validation
// errors, duplicate-key conflicts, foreign-key violations, and storage errors
// are all returned to the caller — never swallowed — so evidence loss is
// visible at the call site instead of silently degrading the ledger.
func (s *sqliteStore) AppendDispatchEvent(ctx context.Context, ev DispatchEvent) error {
	if ev.ConversationID == "" {
		return errors.New("conversation id required")
	}
	if ev.Iteration < 0 {
		return errors.New("event iteration must not be negative")
	}
	if ev.Kind == "" {
		return errors.New("event kind required")
	}
	if ev.Seq <= 0 {
		return fmt.Errorf("dispatch event seq must be positive, got %d", ev.Seq)
	}
	if !json.Valid([]byte(ev.PayloadJSON)) {
		return errors.New("dispatch event payload is not valid JSON")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := ev.Timestamp.Unix()
	if ev.Timestamp.IsZero() {
		createdAt = time.Now().Unix()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO dispatch_events (conversation_id, seq, kind, iteration, created_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)`,
		ev.ConversationID, ev.Seq, ev.Kind, ev.Iteration, createdAt, ev.PayloadJSON,
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: conversation %q seq %d", ErrDispatchEventDuplicate, ev.ConversationID, ev.Seq)
		}
		return fmt.Errorf("insert dispatch event: %w", err)
	}
	return nil
}

// ListDispatchEvents returns the conversation's dispatch events ordered by seq
// ascending, the order in which the dispatch produced them.
func (s *sqliteStore) ListDispatchEvents(ctx context.Context, conversationID string) ([]DispatchEvent, error) {
	if conversationID == "" {
		return nil, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT conversation_id, seq, kind, iteration, created_at, payload
		FROM dispatch_events
		WHERE conversation_id = ?
		ORDER BY seq ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DispatchEvent
	for rows.Next() {
		var ev DispatchEvent
		var createdAt int64
		if err := rows.Scan(&ev.ConversationID, &ev.Seq, &ev.Kind, &ev.Iteration, &createdAt, &ev.PayloadJSON); err != nil {
			return nil, err
		}
		ev.Timestamp = time.Unix(createdAt, 0)
		out = append(out, ev)
	}
	return out, rows.Err()
}
