package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/updatecoord/operation"
)

// journalFor builds a minimal valid prepared-intent journal bound to the
// given current operation, with an explicit "no prior selection" block.
func journalFor(opID int64, target string) ActivationJournal {
	return ActivationJournal{
		SchemaVersion:            ActivationJournalSchemaVersion,
		InstallID:                "test-install",
		OpID:                     opID,
		TargetVersion:            target,
		StagedVersionDir:         "staged-9.9.9",
		VerifiedArtifactSHA256:   strings.Repeat("a", 64),
		PriorSelectedVersion:     "",
		PriorSelectionGeneration: 0,
		PriorSelectionDigest:     "",
		Checkpoint:               JournalPrepared,
	}
}

func startCurrentOperation(t *testing.T, s *Store, target string) operation.Snapshot {
	t.Helper()
	snap, err := NewAdapter(s).Start(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestActivationJournalRoundTripAndProgression(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(snap.ID, "9.9.9")
	j.PriorSelectedVersion = "8.8.8"
	j.PriorSelectionGeneration = 3
	j.PriorSelectionDigest = strings.Repeat("b", 64)

	rev, err := s.SaveActivationJournal(ctx, 0, j)
	if err != nil || rev != 1 {
		t.Fatalf("save %d %v", rev, err)
	}
	loaded, rev, err := s.LoadActivationJournal(ctx, snap.ID)
	if err != nil || loaded != j || rev != 1 {
		t.Fatalf("load mismatch: %+v %d %v", loaded, rev, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// New store handle reload: the journal survives with identical facts.
	s, err = Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if loaded, rev, err = s.LoadActivationJournal(ctx, snap.ID); err != nil || loaded != j || rev != 1 {
		t.Fatalf("reload mismatch: %+v %d %v", loaded, rev, err)
	}

	// Strictly-forward checkpoint progression; only the checkpoint and
	// revision change, the immutable facts never do.
	for i, cp := range []JournalCheckpoint{
		JournalSwitchIntent, JournalSelected, JournalHealthVerified,
		JournalCleanupPending, JournalComplete,
	} {
		next := j
		next.Checkpoint = cp
		rev, err = s.SaveActivationJournal(ctx, rev, next)
		if err != nil || rev != int64(i+2) {
			t.Fatalf("progress to %q: rev %d err %v", cp, rev, err)
		}
	}
	loaded, rev, err = s.LoadActivationJournal(ctx, snap.ID)
	if err != nil || rev != 6 || loaded.Checkpoint != JournalComplete {
		t.Fatalf("final checkpoint: %+v rev %d err %v", loaded, rev, err)
	}
	if loaded.TargetVersion != j.TargetVersion || loaded.StagedVersionDir != j.StagedVersionDir ||
		loaded.VerifiedArtifactSHA256 != j.VerifiedArtifactSHA256 ||
		loaded.PriorSelectedVersion != "8.8.8" || loaded.PriorSelectionGeneration != 3 ||
		loaded.PriorSelectionDigest != j.PriorSelectionDigest {
		t.Fatalf("immutable facts changed: %+v", loaded)
	}
}

func TestActivationJournalCASRaces(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s1, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	snap := startCurrentOperation(t, s1, "9.9.9")
	j := journalFor(snap.ID, "9.9.9")

	if _, err = s1.SaveActivationJournal(ctx, 0, j); err != nil {
		t.Fatal(err)
	}
	// A second create of the one-per-operation journal is stale.
	if _, err = s2.SaveActivationJournal(ctx, 0, j); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("duplicate create %v", err)
	}
	// Both handles race the same revision; exactly one wins.
	next := j
	next.Checkpoint = JournalSwitchIntent
	var wg sync.WaitGroup
	wins, stale := 0, 0
	var mu sync.Mutex
	for _, store := range []*Store{s1, s2} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			_, err := store.SaveActivationJournal(ctx, 1, next)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrStaleRevision):
				stale++
			default:
				t.Errorf("unexpected race error %v", err)
			}
		}(store)
	}
	wg.Wait()
	if wins != 1 || stale != 1 {
		t.Fatalf("race outcome wins=%d stale=%d, want 1 and 1", wins, stale)
	}
	if _, rev, err := s2.LoadActivationJournal(ctx, snap.ID); err != nil || rev != 2 {
		t.Fatalf("post-race revision %d err %v", rev, err)
	}
	// A revision that never matches is refused without writing.
	if _, err = s2.SaveActivationJournal(ctx, 7, next); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("wrong expected revision %v", err)
	}
	if _, rev, err := s2.LoadActivationJournal(ctx, snap.ID); err != nil || rev != 2 {
		t.Fatalf("stale write mutated journal: rev %d err %v", rev, err)
	}
}

func TestActivationJournalWrongInstallOpAndMissing(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// No operation exists at all: a journal cannot be orphaned onto one.
	orphan := journalFor(1, "9.9.9")
	if _, err = s.SaveActivationJournal(ctx, 0, orphan); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("journal without an operation %v", err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, 1); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("load missing journal %v", err)
	}

	snap := startCurrentOperation(t, s, "9.9.9")
	// Wrong installation binding.
	wrong := journalFor(snap.ID, "9.9.9")
	wrong.InstallID = "other-install"
	if _, err = s.SaveActivationJournal(ctx, 0, wrong); !errors.Is(err, ErrInstallIDMismatch) {
		t.Fatalf("cross-install journal %v", err)
	}
	// Nonexistent operation id.
	missing := journalFor(snap.ID+10, "9.9.9")
	if _, err = s.SaveActivationJournal(ctx, 0, missing); !errors.Is(err, ErrNotCurrentOperation) {
		t.Fatalf("journal for nonexistent op %v", err)
	}
	// Target contradicting the owning operation record.
	offTarget := journalFor(snap.ID, "1.0.0")
	if _, err = s.SaveActivationJournal(ctx, 0, offTarget); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("journal contradicting operation target %v", err)
	}

	// Create op 1's journal, then a NEW current operation supersedes op 1:
	// the stale writer for op 1 is refused.
	j := journalFor(snap.ID, "9.9.9")
	if _, err = s.SaveActivationJournal(ctx, 0, j); err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter(s)
	if _, err = adapter.Apply(ctx, operation.Input{Event: operation.EventCancel, OperationID: snap.ID}); err != nil {
		t.Fatal(err)
	}
	next, err := adapter.Start(ctx, "10.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == snap.ID {
		t.Fatal("expected a new current operation")
	}
	staleUpdate := j
	staleUpdate.Checkpoint = JournalSwitchIntent
	if _, err = s.SaveActivationJournal(ctx, 1, staleUpdate); !errors.Is(err, ErrNotCurrentOperation) {
		t.Fatalf("stale writer for superseded op %v", err)
	}
	if _, err = s.SaveActivationJournal(ctx, 0, journalFor(snap.ID, "9.9.9")); !errors.Is(err, ErrNotCurrentOperation) {
		t.Fatalf("create for superseded op %v", err)
	}
	// The journal for the new current operation is still attachable, and
	// the superseded journal remains readable for reconciliation.
	if _, err = s.SaveActivationJournal(ctx, 0, journalFor(next.ID, "10.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, snap.ID); err != nil {
		t.Fatalf("superseded journal must stay readable: %v", err)
	}
}

func TestActivationJournalImmutableFacts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(snap.ID, "9.9.9")
	if _, err = s.SaveActivationJournal(ctx, 0, j); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ActivationJournal){
		"target":          func(x *ActivationJournal) { x.TargetVersion = "1.0.0" },
		"staged dir":      func(x *ActivationJournal) { x.StagedVersionDir = "staged-1.0.0" },
		"artifact digest": func(x *ActivationJournal) { x.VerifiedArtifactSHA256 = strings.Repeat("c", 64) },
		"prior version": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionGeneration = 1
			x.PriorSelectionDigest = strings.Repeat("d", 64)
		},
		"installation":   func(x *ActivationJournal) { x.InstallID = "other-install" },
		"operation":      func(x *ActivationJournal) { x.OpID = snap.ID + 1 },
		"schema version": func(x *ActivationJournal) { x.SchemaVersion = 99 },
	}
	for name, mutate := range mutations {
		next := j
		next.Checkpoint = JournalSwitchIntent
		mutate(&next)
		_, err := s.SaveActivationJournal(ctx, 1, next)
		if name == "installation" || name == "operation" {
			if !errors.Is(err, ErrNotCurrentOperation) && !errors.Is(err, ErrInstallIDMismatch) {
				t.Fatalf("%s mutation allowed: %v", name, err)
			}
		} else if !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("%s mutation allowed: %v", name, err)
		}
	}
	// Nothing was written: the journal is unchanged at revision 1.
	loaded, rev, err := s.LoadActivationJournal(ctx, snap.ID)
	if err != nil || rev != 1 || loaded != j {
		t.Fatalf("journal mutated by refused saves: %+v rev %d err %v", loaded, rev, err)
	}
}

func TestActivationJournalMalformedStoredRowRefusedUntouched(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(snap.ID, "9.9.9")
	if _, err = s.SaveActivationJournal(ctx, 0, j); err != nil {
		t.Fatal(err)
	}

	// Malformed stored JSON: refused on load and on update, never reset.
	if _, err = s.db.Exec(`UPDATE activation_journals SET journal_json='null' WHERE op_id=?`, snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, snap.ID); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("malformed load %v", err)
	}
	next := j
	next.Checkpoint = JournalSwitchIntent
	if _, err = s.SaveActivationJournal(ctx, 1, next); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("malformed update %v", err)
	}
	// The malformed row is left untouched: still exactly 'null'.
	var payload string
	if err = s.db.QueryRow(`SELECT journal_json FROM activation_journals WHERE op_id=?`, snap.ID).Scan(&payload); err != nil || payload != "null" {
		t.Fatalf("refused save mutated stored row: %q %v", payload, err)
	}

	// Stored JSON with an unknown checkpoint or an unknown field is
	// corrupt and refused both ways too.
	if _, err = s.db.Exec(`UPDATE activation_journals SET journal_json=? WHERE op_id=?`,
		`{"schema_version":1,"install_id":"test-install","op_id":`+strconv.FormatInt(snap.ID, 10)+
			`,"target_version":"9.9.9","staged_version_dir":"s","verified_artifact_sha256":"`+
			strings.Repeat("a", 64)+`","prior_selected_version":"","prior_selection_generation":0,`+
			`"prior_selection_digest":"","checkpoint":"time-travel"}`, snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, snap.ID); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("unsupported stored checkpoint %v", err)
	}
	if _, err = s.SaveActivationJournal(ctx, 1, next); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("update over unsupported checkpoint %v", err)
	}

	// An unsupported checkpoint on a NEW journal is refused before any
	// write, and no row appears. Op 1 is terminal (its journal is corrupt);
	// cancel it so a fresh current operation can start.
	if _, err = NewAdapter(s).Apply(ctx, operation.Input{Event: operation.EventCancel, OperationID: snap.ID}); err != nil {
		t.Fatal(err)
	}
	snap2 := startCurrentOperation(t, s, "1.0.0")
	bogus := journalFor(snap2.ID, "1.0.0")
	bogus.Checkpoint = JournalCheckpoint("bogus")
	if _, err = s.SaveActivationJournal(ctx, 0, bogus); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unsupported checkpoint accepted %v", err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, snap2.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("refused journal created a row %v", err)
	}
}

func TestActivationJournalNoDowngradeOfCompletedJournal(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(snap.ID, "9.9.9")
	rev, err := s.SaveActivationJournal(ctx, 0, j)
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range []JournalCheckpoint{JournalSwitchIntent, JournalSelected, JournalHealthVerified, JournalCleanupPending, JournalComplete} {
		j.Checkpoint = cp
		if rev, err = s.SaveActivationJournal(ctx, rev, j); err != nil {
			t.Fatal(err)
		}
	}
	// A downgrade of the completed journal is refused and writes nothing.
	for _, cp := range []JournalCheckpoint{JournalSelected, JournalHealthVerified, JournalComplete} {
		down := j
		down.Checkpoint = cp
		if _, err = s.SaveActivationJournal(ctx, rev, down); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("downgrade to %q allowed: %v", cp, err)
		}
	}
	loaded, finalRev, err := s.LoadActivationJournal(ctx, snap.ID)
	if err != nil || finalRev != 6 || loaded.Checkpoint != JournalComplete {
		t.Fatalf("completed journal downgraded: %+v rev %d err %v", loaded, finalRev, err)
	}
	if finalRev != rev {
		t.Fatalf("refused downgrades moved the revision: %d != %d", finalRev, rev)
	}
}

func TestActivationJournalExplicitPriorSelectionAndIdentifiers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")

	// A prior selection must be explicit and complete: partial or guessed
	// combinations are refused before any write.
	bad := map[string]func(*ActivationJournal){
		"prior version without generation": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionDigest = strings.Repeat("e", 64)
		},
		"prior version without digest": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionGeneration = 2
		},
		"prior generation without version": func(x *ActivationJournal) { x.PriorSelectionGeneration = 2 },
		"prior digest without version":     func(x *ActivationJournal) { x.PriorSelectionDigest = strings.Repeat("e", 64) },
		"prior digest not hex": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionGeneration = 2
			x.PriorSelectionDigest = strings.Repeat("z", 64)
		},
		"prior digest uppercase": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionGeneration = 2
			x.PriorSelectionDigest = strings.Repeat("A", 64)
		},
		"prior generation zero": func(x *ActivationJournal) {
			x.PriorSelectedVersion = "8.8.8"
			x.PriorSelectionGeneration = 0
			x.PriorSelectionDigest = strings.Repeat("e", 64)
		},
		"artifact digest not sha256": func(x *ActivationJournal) { x.VerifiedArtifactSHA256 = strings.Repeat("a", 32) },
		"artifact digest uppercase":  func(x *ActivationJournal) { x.VerifiedArtifactSHA256 = strings.Repeat("A", 64) },
		"staged dir absolute":        func(x *ActivationJournal) { x.StagedVersionDir = "/opt/cercano/versions/9.9.9" },
		"staged dir windows drive":   func(x *ActivationJournal) { x.StagedVersionDir = "C:\\cercano\\9.9.9" },
		"staged dir backslash":       func(x *ActivationJournal) { x.StagedVersionDir = `versions\9.9.9` },
		"staged dir parent hop":      func(x *ActivationJournal) { x.StagedVersionDir = "a/../9.9.9" },
		"staged dir dot component":   func(x *ActivationJournal) { x.StagedVersionDir = "./9.9.9" },
		"staged dir empty component": func(x *ActivationJournal) { x.StagedVersionDir = "versions//9.9.9" },
		"staged dir trailing slash":  func(x *ActivationJournal) { x.StagedVersionDir = "versions/9.9.9/" },
		"staged dir empty":           func(x *ActivationJournal) { x.StagedVersionDir = "" },
		"target empty":               func(x *ActivationJournal) { x.TargetVersion = " " },
		"schema version wrong":       func(x *ActivationJournal) { x.SchemaVersion = 2 },
	}
	for name, mutate := range bad {
		j := journalFor(snap.ID, "9.9.9")
		mutate(&j)
		if _, err = s.SaveActivationJournal(ctx, 0, j); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("%s accepted: %v", name, err)
		}
		if _, _, err = s.LoadActivationJournal(ctx, snap.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("%s created a row: %v", name, err)
		}
	}

	// The two legal forms: explicit none, and an explicit complete prior
	// selection with a slash-separated relative staged identifier.
	none := journalFor(snap.ID, "9.9.9")
	if rev, err := s.SaveActivationJournal(ctx, 0, none); err != nil || rev != 1 {
		t.Fatalf("explicit-none journal refused: %d %v", rev, err)
	}
	// A complete prior selection is legal on the NEXT revision, with the
	// rest of the immutable facts unchanged.
	none.PriorSelectedVersion = "8.8.8"
	none.PriorSelectionGeneration = 4
	none.PriorSelectionDigest = strings.Repeat("e", 64)
	if _, err = s.SaveActivationJournal(ctx, 1, none); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("post-hoc prior selection allowed: %v", err)
	}
}

func TestActivationJournalInsertRequiresPreparedIntent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")
	for _, cp := range []JournalCheckpoint{JournalSelected, JournalComplete, JournalRestored} {
		j := journalFor(snap.ID, "9.9.9")
		j.Checkpoint = cp
		if _, err = s.SaveActivationJournal(ctx, 0, j); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("journal created at %q: %v", cp, err)
		}
		if _, _, err = s.LoadActivationJournal(ctx, snap.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("refused creation left a row (%q): %v", cp, err)
		}
	}
}

// journalWithPrior returns a prepared journal carrying an explicit complete
// prior selection — the form every rollback branch requires.
func journalWithPrior(opID int64, target string) ActivationJournal {
	j := journalFor(opID, target)
	j.PriorSelectedVersion = "8.8.8"
	j.PriorSelectionGeneration = 3
	j.PriorSelectionDigest = strings.Repeat("b", 64)
	return j
}

// rawJournalRow returns the stored journal payload bytes and revision.
func rawJournalRow(t *testing.T, s *Store, opID int64) (string, int64) {
	t.Helper()
	var payload string
	var rev int64
	if err := s.db.QueryRow(`SELECT journal_json, revision FROM activation_journals WHERE op_id=?`, opID).Scan(&payload, &rev); err != nil {
		t.Fatal(err)
	}
	return payload, rev
}

// journalAt creates a fresh current operation and its journal, then legally
// advances the journal to cp (JournalPrepared needs no advance).
func journalAt(t *testing.T, s *Store, cp JournalCheckpoint) (ActivationJournal, int64, int64) {
	t.Helper()
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalWithPrior(snap.ID, "9.9.9")
	rev, err := s.SaveActivationJournal(context.Background(), 0, j)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range map[JournalCheckpoint][]JournalCheckpoint{
		JournalSwitchIntent:   {JournalSwitchIntent},
		JournalSelected:       {JournalSwitchIntent, JournalSelected},
		JournalHealthVerified: {JournalSwitchIntent, JournalSelected, JournalHealthVerified},
		JournalCleanupPending: {JournalSwitchIntent, JournalSelected, JournalHealthVerified, JournalCleanupPending},
		JournalComplete:       {JournalSwitchIntent, JournalSelected, JournalHealthVerified, JournalCleanupPending, JournalComplete},
		JournalRollbackIntent: {JournalRollbackIntent},
		JournalRestored:       {JournalRollbackIntent, JournalRestored},
		JournalPrepared:       nil,
	}[cp] {
		j.Checkpoint = step
		if rev, err = s.SaveActivationJournal(context.Background(), rev, j); err != nil {
			t.Fatalf("advance to %q: %v", step, err)
		}
	}
	return j, rev, snap.ID
}

// TestActivationJournalTransitionLegalityTable is the regression for the
// explicit transition contract. Only the listed successors are legal; every
// skip, downgrade, noop re-save, terminal move, or rollback after health
// (HealthVerified/Complete have no rollback successor) is refused with
// ErrInvalidRecord and leaves the stored bytes and revision untouched.
func TestActivationJournalTransitionLegalityTable(t *testing.T) {
	ctx := context.Background()
	legal := map[JournalCheckpoint][]JournalCheckpoint{
		JournalPrepared:        {JournalSwitchIntent, JournalRollbackIntent},
		JournalSwitchIntent:   {JournalSelected, JournalRollbackIntent},
		JournalSelected:       {JournalHealthVerified, JournalRollbackIntent},
		JournalHealthVerified: {JournalCleanupPending, JournalComplete},
		JournalCleanupPending: {JournalComplete},
		JournalRollbackIntent: {JournalRestored},
		JournalComplete:       {},
		JournalRestored:       {},
	}
	all := []JournalCheckpoint{
		JournalPrepared, JournalSwitchIntent, JournalSelected,
		JournalHealthVerified, JournalCleanupPending, JournalComplete,
		JournalRollbackIntent, JournalRestored,
	}
	for _, from := range all {
		for _, to := range all {
			t.Run(fmt.Sprintf("%s/%s", from, to), func(t *testing.T) {
				s, err := Open(t.TempDir(), "test-install")
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				j, rev, opID := journalAt(t, s, from)
				wantBytes, wantRev := rawJournalRow(t, s, opID)
				if wantRev != rev {
					t.Fatalf("helper revision drift: %d != %d", wantRev, rev)
				}
				next := j
				next.Checkpoint = to
				isLegal := false
				for _, succ := range legal[from] {
					if succ == to {
						isLegal = true
					}
				}
				_, err = s.SaveActivationJournal(ctx, rev, next)
				if isLegal {
					if err != nil {
						t.Fatalf("legal transition %q -> %q refused: %v", from, to, err)
					}
					loaded, gotRev, err := s.LoadActivationJournal(ctx, opID)
					if err != nil || gotRev != rev+1 || loaded.Checkpoint != to {
						t.Fatalf("legal save not durable: %+v rev %d err %v", loaded, gotRev, err)
					}
				} else {
					if !errors.Is(err, ErrInvalidRecord) {
						t.Fatalf("illegal transition %q -> %q accepted: %v", from, to, err)
					}
					gotBytes, gotRev := rawJournalRow(t, s, opID)
					if gotBytes != wantBytes || gotRev != wantRev {
						t.Fatalf("refused save mutated row: rev %d -> %d", wantRev, gotRev)
					}
				}
			})
		}
	}
}

// TestActivationJournalRollbackIntentRequiresPriorSelection: a first
// activation (explicit prior-none) must not enter the rollback branch —
// there is no prior version to restore and the model defines no separate
// first-install cancellation checkpoint, so the save is rejected with
// ErrInvalidRecord rather than pretending a prior selection exists. A
// stored row of that impossible shape is corrupt on load, never reset.
func TestActivationJournalRollbackIntentRequiresPriorSelection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := startCurrentOperation(t, s, "9.9.9")
	j := journalFor(snap.ID, "9.9.9") // explicit prior-none
	if _, err = s.SaveActivationJournal(ctx, 0, j); err != nil {
		t.Fatal(err)
	}
	wantBytes, wantRev := rawJournalRow(t, s, snap.ID)
	rb := j
	rb.Checkpoint = JournalRollbackIntent
	if _, err = s.SaveActivationJournal(ctx, 1, rb); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("prior-none rollback allowed: %v", err)
	}
	gotBytes, gotRev := rawJournalRow(t, s, snap.ID)
	if gotBytes != wantBytes || gotRev != wantRev {
		t.Fatalf("refused prior-none rollback mutated row: rev %d -> %d", wantRev, gotRev)
	}
	// The impossible stored shape is refused on load, never treated as empty.
	if _, err = s.db.Exec(`UPDATE activation_journals SET journal_json=? WHERE op_id=?`,
		strings.Replace(gotBytes, `"checkpoint":"prepared"`, `"checkpoint":"rollback-intent"`, 1), snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LoadActivationJournal(ctx, snap.ID); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("prior-none rollback-intent row load %v", err)
	}
}
