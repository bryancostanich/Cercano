package conversation

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newDispatchTestStore(t *testing.T) (Store, context.Context) {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func TestDispatchEvent_AppendAndOrderedRead(t *testing.T) {
	s, ctx := newDispatchTestStore(t)

	// Insert out of order on purpose: reads must sort by seq, not insertion.
	events := []DispatchEvent{
		{ConversationID: "c1", Seq: 3, Kind: "tool_result", Iteration: 2,
			Timestamp: time.Unix(1700, 0), PayloadJSON: `{"ok":false}`},
		{ConversationID: "c1", Seq: 1, Kind: "dispatch_start", Iteration: 1,
			Timestamp: time.Unix(1600, 0), PayloadJSON: `{"tool":"read_file"}`},
		{ConversationID: "c1", Seq: 2, Kind: "tool_call", Iteration: 1,
			Timestamp: time.Unix(1650, 0), PayloadJSON: `{"path":"a.go"}`},
	}
	for _, ev := range events {
		if err := s.(DispatchEventStore).AppendDispatchEvent(ctx, ev); err != nil {
			t.Fatalf("append seq %d: %v", ev.Seq, err)
		}
	}

	got, err := s.(DispatchEventStore).ListDispatchEvents(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	for i, wantSeq := range []int64{1, 2, 3} {
		if got[i].Seq != wantSeq {
			t.Errorf("position %d: want seq %d, got %d", i, wantSeq, got[i].Seq)
		}
	}
	if got[0].Kind != "dispatch_start" || got[0].Iteration != 1 ||
		got[0].PayloadJSON != `{"tool":"read_file"}` {
		t.Errorf("seq 1 round-trip mismatch: %+v", got[0])
	}
	if !got[1].Timestamp.Equal(time.Unix(1650, 0)) {
		t.Errorf("seq 2 timestamp mismatch: %v", got[1].Timestamp)
	}

	// Unknown conversation → empty, no error.
	none, err := s.(DispatchEventStore).ListDispatchEvents(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("unknown conversation should return empty, got %d events", len(none))
	}
}

func TestDispatchEvent_DuplicateRejectedAndEarlierSnapshotPreserved(t *testing.T) {
	s, ctx := newDispatchTestStore(t)

	first := DispatchEvent{ConversationID: "c1", Seq: 1, Kind: "dispatch_start",
		Iteration: 1, Timestamp: time.Unix(1600, 0), PayloadJSON: `{"attempt":1}`}
	if err := s.(DispatchEventStore).AppendDispatchEvent(ctx, first); err != nil {
		t.Fatal(err)
	}

	// Same (conversation_id, seq), different payload: must be rejected, not
	// overwritten.
	replay := first
	replay.PayloadJSON = `{"attempt":2}`
	err := s.(DispatchEventStore).AppendDispatchEvent(ctx, replay)
	if err == nil {
		t.Fatal("duplicate seq must be rejected")
	}
	if !errors.Is(err, ErrDispatchEventDuplicate) {
		t.Errorf("want ErrDispatchEventDuplicate, got %v", err)
	}

	got, err := s.(DispatchEventStore).ListDispatchEvents(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate must not add rows, got %d", len(got))
	}
	if got[0].PayloadJSON != `{"attempt":1}` {
		t.Errorf("earlier snapshot was overwritten: %s", got[0].PayloadJSON)
	}
}

func TestDispatchEvent_Validation(t *testing.T) {
	s, ctx := newDispatchTestStore(t)
	st := s.(DispatchEventStore)

	cases := []struct {
		name string
		ev   DispatchEvent
	}{
		{"empty conversation id", DispatchEvent{Seq: 1, Kind: "k", PayloadJSON: `{}`}},
		{"empty kind", DispatchEvent{ConversationID: "c1", Seq: 1, PayloadJSON: `{}`}},
		{"zero seq", DispatchEvent{ConversationID: "c1", Kind: "k", PayloadJSON: `{}`}},
		{"negative seq", DispatchEvent{ConversationID: "c1", Seq: -2, Kind: "k", PayloadJSON: `{}`}},
		{"invalid JSON payload", DispatchEvent{ConversationID: "c1", Seq: 1, Kind: "k", PayloadJSON: `{oops`}},
		{"empty payload", DispatchEvent{ConversationID: "c1", Seq: 1, Kind: "k", PayloadJSON: ""}},
	}
	for _, tc := range cases {
		if err := st.AppendDispatchEvent(ctx, tc.ev); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}

	// A missing conversation row is a foreign-key violation surfaced to the
	// caller, not silently swallowed.
	if err := st.AppendDispatchEvent(ctx, DispatchEvent{
		ConversationID: "ghost", Seq: 1, Kind: "k", PayloadJSON: `{}`,
	}); err == nil {
		t.Error("append for nonexistent conversation must return the FK error")
	}
}

func TestDispatchEvent_CascadeDelete(t *testing.T) {
	s, ctx := newDispatchTestStore(t)
	st := s.(DispatchEventStore)

	for _, seq := range []int64{1, 2, 3} {
		if err := st.AppendDispatchEvent(ctx, DispatchEvent{
			ConversationID: "c1", Seq: seq, Kind: "k", PayloadJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// FK enforcement must be on (Open enables it) for the cascade to fire.
	if err := s.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListDispatchEvents(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("cascade delete should remove dispatch events, got %d", len(got))
	}
}

func TestDispatchEvent_OldDBUpgradeAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.db")

	// Build a pre-dispatch_events DB by hand: only the original conversations
	// and turns tables, no migration steps applied.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE conversations (
			id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '',
			project_dir TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL, last_turn_at INTEGER NOT NULL
		);
		INSERT INTO conversations (id, started_at, last_turn_at) VALUES ('old', 1, 1);`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// First Open upgrades the old DB additively (CREATE IF NOT EXISTS) and
	// preserves the pre-existing conversation row.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	if _, err := s.Get(ctx, "old"); err != nil {
		t.Fatalf("old conversation lost on upgrade: %v", err)
	}
	st := s.(DispatchEventStore)
	if err := st.AppendDispatchEvent(ctx, DispatchEvent{
		ConversationID: "old", Seq: 1, Kind: "dispatch_start",
		Timestamp: time.Unix(1600, 0), PayloadJSON: `{"a":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: events persist and remain ordered.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.(DispatchEventStore).ListDispatchEvents(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 1 || got[0].PayloadJSON != `{"a":1}` {
		t.Fatalf("events lost on reopen: %+v", got)
	}

	// Uniqueness still enforced after reopen.
	err = s2.(DispatchEventStore).AppendDispatchEvent(ctx, DispatchEvent{
		ConversationID: "old", Seq: 1, Kind: "dispatch_start", PayloadJSON: `{"b":2}`,
	})
	if err == nil || !errors.Is(err, ErrDispatchEventDuplicate) {
		t.Errorf("duplicate guard lost after reopen: %v", err)
	}
}
