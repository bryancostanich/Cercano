package state

import (
	"context"
	"errors"
	"testing"
)

func TestJournalAbsenceRestorationRoundtrip(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	op := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(op.ID, "9.9.9")
	rev, e := s.SaveActivationJournal(context.Background(), 0, j)
	if e != nil {
		t.Fatal(e)
	}
	for _, cp := range []JournalCheckpoint{JournalSwitchIntent, JournalSelected, JournalAbsenceIntent, JournalAbsenceRestored} {
		j.Checkpoint = cp
		rev, e = s.SaveActivationJournal(context.Background(), rev, j)
		if e != nil {
			t.Fatal(cp, e)
		}
		got, r, e := s.LoadActivationJournal(context.Background(), op.ID)
		if e != nil || got != j || r != rev {
			t.Fatal("readback", got, r, e)
		}
	}
	for _, cp := range []JournalCheckpoint{JournalComplete, JournalSwitchIntent, JournalRestored, JournalAbsenceIntent, JournalAbsenceRestored} {
		changed := j
		changed.Checkpoint = cp
		if _, e = s.SaveActivationJournal(context.Background(), rev, changed); !errors.Is(e, ErrInvalidRecord) {
			t.Fatalf("terminal write %s: %v", cp, e)
		}
	}
	got, r, e := s.LoadActivationJournal(context.Background(), op.ID)
	if e != nil || got != j || r != rev {
		t.Fatal("terminal record changed")
	}
}
func TestJournalAbsenceRequiresNoPriorAndPreHealth(t *testing.T) {
	j := journalFor(1, "9.9.9")
	for _, cp := range []JournalCheckpoint{JournalAbsenceIntent, JournalAbsenceRestored} {
		j.Checkpoint = cp
		if e := ValidateActivationJournal(j, "test-install"); e != nil {
			t.Fatal(e)
		}
		prior := j
		prior.PriorSelectedVersion = "9.9.8"
		prior.PriorSelectionGeneration = 1
		prior.PriorSelectionDigest = prior.VerifiedArtifactSHA256
		if e := ValidateActivationJournal(prior, "test-install"); !errors.Is(e, ErrInvalidRecord) {
			t.Fatal("prior version accepted", e)
		}
	}
	for _, from := range []JournalCheckpoint{JournalPrepared, JournalSwitchIntent, JournalSelected} {
		if !journalCheckpointLegal(from, JournalAbsenceIntent) {
			t.Fatal("missing prehealth branch", from)
		}
	}
	for _, from := range []JournalCheckpoint{JournalHealthVerified, JournalCleanupPending, JournalComplete, JournalRestored, JournalAbsenceRestored} {
		if journalCheckpointLegal(from, JournalAbsenceIntent) {
			t.Fatal("posthealth/terminal branch", from)
		}
	}
	if journalCheckpointLegal(JournalSelected, JournalAbsenceRestored) {
		t.Fatal("absence intent skipped")
	}
}
