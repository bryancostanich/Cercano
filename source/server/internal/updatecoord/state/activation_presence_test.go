package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestActivationJournalRequiresEveryExplicitField(t *testing.T) {
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	op := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(op.ID, "9.9.9")
	if _, err = s.SaveActivationJournal(context.Background(), 0, j); err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(valid, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		for _, kind := range []string{"missing", "null"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				var changed map[string]json.RawMessage
				json.Unmarshal(valid, &changed)
				if kind == "missing" {
					delete(changed, name)
				} else {
					changed[name] = json.RawMessage(`null`)
				}
				payload, _ := json.Marshal(changed)
				if _, err := s.db.Exec(`UPDATE activation_journals SET journal_json=? WHERE op_id=?`, string(payload), op.ID); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.LoadActivationJournal(context.Background(), op.ID); !errors.Is(err, ErrCorruptDatabase) {
					t.Fatalf("missing/null field accepted: %v", err)
				}
				var after string
				var rev int64
				if err := s.db.QueryRow(`SELECT journal_json,revision FROM activation_journals WHERE op_id=?`, op.ID).Scan(&after, &rev); err != nil {
					t.Fatal(err)
				}
				if after != string(payload) || rev != 1 {
					t.Fatal("corrupt data rewritten")
				}
			})
		}
	}
	if _, err = s.db.Exec(`UPDATE activation_journals SET journal_json=? WHERE op_id=?`, string(valid), op.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.LoadActivationJournal(context.Background(), op.ID); err != nil || got != j {
		t.Fatalf("explicit none failed: %+v %v", got, err)
	}
}
func TestActivationJournalRestoredWithoutPriorRefused(t *testing.T) {
	j := journalFor(1, "9.9.9")
	j.Checkpoint = JournalRestored
	data, _ := json.Marshal(j)
	if _, err := decodeActivationJournal(data); err == nil {
		t.Fatal("restored nonexistent prior selection")
	}
}
