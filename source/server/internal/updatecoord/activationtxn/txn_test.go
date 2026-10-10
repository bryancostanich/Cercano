package activationtxn

// Switch transaction tests run ONLY against temporary directories this
// test process owns (a real tmp SQLite state store, a real privdir-
// verified private publication directory, and a real exclusion lease),
// with no live default location, no process stop, no activation-version
// rename, and no production keys. The staged version identifier is only
// a recorded string: nothing installs, moves, or renames anything for it,
// and the tests assert exactly that.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/privdir"
	"cercano/source/server/internal/updatecoord/selection"
	"cercano/source/server/internal/updatecoord/state"
)

const (
	testInstall  = "test-install"
	testTarget   = "2.0.0"
	testStagedID = "staged-2.0.0"
	// testArtifact is 64 lowercase hex characters.
	testArtifact    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testPriorVer    = "1.0.0"
	testPriorStaged = "staged-1.0.0"
	testPriorGen    = int64(4)
)

func digestChar(c byte) string { return strings.Repeat(string(rune(c)), 64) }

func rawDigest(t *testing.T, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(t *testing.T, s activation.Selection) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal selection: %v", err)
	}
	return b
}

// fixture is the real test world: a tmp SQLite store with one current
// operation (and, unless noted, one PREPARED journal), a privdir-verified
// private publication directory, and a held exclusive Update lease for
// that directory.
type fixture struct {
	t     *testing.T
	root  string
	dir   string
	store *state.Store
	lock  *exclusion.Handle
	opID  int64
}

// newFixtureNoJournal provisions the world WITHOUT a journal: the
// transaction creates journals nowhere, so tests needing one save it
// themselves.
func newFixtureNoJournal(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	// Provision only this newly owned fixture tree before SQLite opens it.
	// Existing unsafe ACLs are never repaired by production verification.
	org := "Cercano"
	if runtime.GOOS == "linux" {
		org = "cercano"
	}
	parent := root
	for _, part := range []string{org, "updater", testInstall} {
		parent = filepath.Join(parent, part)
		if _, err := privdir.Ensure(parent); err != nil {
			t.Fatal(err)
		}
	}
	store, err := state.Open(root, testInstall)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// The publication directory is the STORE's own directory — the same
	// strict store==lease==publication identity the oneshot architecture
	// acquires its lease on. The transaction refuses any other directory.
	dir := store.Directory()
	if _, err := privdir.Ensure(dir); err != nil {
		t.Fatalf("privdir.Ensure: %v", err)
	}
	snap, err := state.NewAdapter(store).Start(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	lock, err := exclusion.Acquire(context.Background(), dir, exclusion.Update)
	if err != nil {
		t.Fatalf("acquire exclusion lease: %v", err)
	}
	f := &fixture{t: t, root: root, dir: dir, store: store, lock: lock, opID: snap.ID}
	t.Cleanup(func() {
		_ = lock.Close()
		_ = store.Close()
	})
	return f
}

func newFixture(t *testing.T, withPrior bool) *fixture {
	t.Helper()
	f := newFixtureNoJournal(t)
	j := preparedJournal(f.opID)
	if withPrior {
		j.PriorSelectedVersion = testPriorVer
		j.PriorSelectionGeneration = testPriorGen
		j.PriorSelectionDigest = digestChar('b')
	}
	if _, err := f.store.SaveActivationJournal(context.Background(), 0, j); err != nil {
		t.Fatalf("save prepared journal: %v", err)
	}
	return f
}

func preparedJournal(opID int64) state.ActivationJournal {
	return state.ActivationJournal{
		SchemaVersion:          state.ActivationJournalSchemaVersion,
		InstallID:              testInstall,
		OpID:                   opID,
		TargetVersion:          testTarget,
		StagedVersionDir:       testStagedID,
		VerifiedArtifactSHA256: testArtifact,
		Checkpoint:             state.JournalPrepared,
	}
}

// priorSelection is the COMPLETE prior descriptor the journal's
// prior-selection block identifies (its artifact digest is the journal's
// recorded prior digest); its staged identifier is deliberately NOT a
// journal fact — only the receipt carries it.
func priorSelection() activation.Selection {
	return activation.Selection{
		SchemaVersion:          activation.SelectionSchemaVersion,
		InstallID:              testInstall,
		Generation:             testPriorGen,
		SelectedVersion:        testPriorVer,
		StagedVersionDir:       testPriorStaged,
		VerifiedArtifactSHA256: digestChar('b'),
	}
}

// target derives the expected target descriptor from the journal's
// immutable facts, the same way the transaction itself does.
func (f *fixture) target() activation.Selection {
	f.t.Helper()
	j, _ := f.journal()
	return activation.Selection{
		SchemaVersion:          activation.SelectionSchemaVersion,
		InstallID:              j.InstallID,
		Generation:             j.PriorSelectionGeneration + 1,
		SelectedVersion:        j.TargetVersion,
		StagedVersionDir:       j.StagedVersionDir,
		VerifiedArtifactSHA256: j.VerifiedArtifactSHA256,
	}
}

func (f *fixture) request() Request {
	return Request{Store: f.store, Directory: f.dir, Lock: f.lock, OpID: f.opID}
}

func (f *fixture) requestWithReceipt(sel activation.Selection, rawDigest string) Request {
	r := f.request()
	r.Prior = PriorReceipt{Selection: sel, Digest: rawDigest}
	return r
}

func (f *fixture) selectionPath() string { return filepath.Join(f.dir, selection.FileName) }

func (f *fixture) journal() (state.ActivationJournal, int64) {
	f.t.Helper()
	j, rev, err := f.store.LoadActivationJournal(context.Background(), f.opID)
	if err != nil {
		f.t.Fatalf("load journal: %v", err)
	}
	return j, rev
}

func (f *fixture) requireJournal(checkpoint state.JournalCheckpoint, revision int64) {
	f.t.Helper()
	j, rev := f.journal()
	if j.Checkpoint != checkpoint || rev != revision {
		f.t.Fatalf("journal checkpoint %q rev %d, want %q rev %d", j.Checkpoint, rev, checkpoint, revision)
	}
}

func (f *fixture) advanceJournal(checkpoint state.JournalCheckpoint) {
	f.t.Helper()
	j, rev := f.journal()
	j.Checkpoint = checkpoint
	if _, err := f.store.SaveActivationJournal(context.Background(), rev, j); err != nil {
		f.t.Fatalf("advance journal to %q: %v", checkpoint, err)
	}
}

func (f *fixture) rawSelection() []byte {
	f.t.Helper()
	b, err := os.ReadFile(f.selectionPath())
	if err != nil {
		f.t.Fatalf("read selection file: %v", err)
	}
	return b
}

func (f *fixture) readSelection() activation.Selection {
	f.t.Helper()
	sel, err := activation.ReadSelection(f.selectionPath())
	if err != nil {
		f.t.Fatalf("read selection: %v", err)
	}
	return sel
}

func (f *fixture) requireSelectionAbsent() {
	f.t.Helper()
	if _, err := os.Lstat(f.selectionPath()); !errors.Is(err, fs.ErrNotExist) {
		f.t.Fatalf("selection file exists or read error: %v", err)
	}
}

// publishSelection arranges a selection file through the real guarded
// publisher (outside this transaction), returning the canonical raw
// digest the NEXT publication must expect.
func (f *fixture) publishSelection(expected selection.Expected, next activation.Selection) string {
	f.t.Helper()
	res, err := selection.Publish(context.Background(), f.dir, f.lock, expected, next)
	if err != nil {
		f.t.Fatalf("arrange selection publish: %v", err)
	}
	if res.State != selection.CommitConfirmed {
		f.t.Fatalf("arrange publish state %q", res.State)
	}
	return res.Digest
}

// setHooks installs the package-private test hooks and clears them when
// the test ends.
func setHooks(t *testing.T, loaded func(j state.ActivationJournal, revision int64) error, afterPublish func() error) {
	t.Helper()
	testHookJournalLoaded = loaded
	testHookAfterPublish = afterPublish
	t.Cleanup(func() {
		testHookJournalLoaded = nil
		testHookAfterPublish = nil
	})
}

func TestSwitchFirstInstallPublishesThenAcknowledges(t *testing.T) {
	f := newFixture(t, false)
	res, err := Switch(context.Background(), f.request())
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	target := f.target()
	if !res.Published || !res.Acknowledged || !res.AwaitHealth {
		t.Fatalf("result flags: published=%v acknowledged=%v awaitHealth=%v", res.Published, res.Acknowledged, res.AwaitHealth)
	}
	if res.Target != target || target.Generation != 1 {
		t.Fatalf("target %+v, want %+v", res.Target, target)
	}
	if res.Checkpoint != state.JournalSelected || res.JournalRevision != 3 {
		t.Fatalf("checkpoint %q rev %d", res.Checkpoint, res.JournalRevision)
	}
	if got := f.readSelection(); got != target {
		t.Fatalf("published selection %+v, want %+v", got, target)
	}
	if raw := f.rawSelection(); rawDigest(t, raw) != res.SelectionDigest {
		t.Fatalf("reported digest does not match the published bytes")
	}
	// The staged version directory is a recorded IDENTIFIER only: the
	// switch never installs, creates, moves, or renames anything for it.
	// (The store's own state database and the exclusion lease's own
	// update.lock also live in this directory.)
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatalf("read publication directory: %v", err)
	}
	for _, e := range entries {
		switch {
		case e.Name() == selection.FileName, e.Name() == "update.lock",
			e.Name() == "state.db", e.Name() == "state.db-wal", e.Name() == "state.db-shm":
		default:
			t.Fatalf("unexpected publication directory entry %q", e.Name())
		}
	}
	for _, staged := range []string{filepath.Join(f.root, testStagedID), filepath.Join(f.dir, testStagedID)} {
		if _, err := os.Lstat(staged); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("staged identifier %q exists on disk: %v", staged, err)
		}
	}
	f.requireJournal(state.JournalSelected, 3)

	// Reentry at selected with the target active is idempotent: no
	// second write, no second acknowledgment, health verification named
	// as the next step — and never a boolean health answer.
	before := f.rawSelection()
	res2, err := Switch(context.Background(), f.request())
	if err != nil {
		t.Fatalf("reentry Switch: %v", err)
	}
	if res2.Published || res2.Acknowledged || !res2.AwaitHealth {
		t.Fatalf("reentry flags: published=%v acknowledged=%v awaitHealth=%v", res2.Published, res2.Acknowledged, res2.AwaitHealth)
	}
	if res2.JournalRevision != 3 {
		t.Fatalf("reentry advanced revision to %d", res2.JournalRevision)
	}
	if after := f.rawSelection(); string(after) != string(before) {
		t.Fatalf("reentry rewrote the selection file")
	}
}

func TestSwitchRecordedPriorReceiptBindsAndPublishes(t *testing.T) {
	f := newFixture(t, true)
	prior := priorSelection()
	rawDigest := f.publishSelection(selection.Expected{Absent: true}, prior)
	res, err := Switch(context.Background(), f.requestWithReceipt(prior, rawDigest))
	if err != nil {
		t.Fatalf("Switch with recorded prior: %v", err)
	}
	target := f.target()
	if target.Generation != testPriorGen+1 {
		t.Fatalf("target generation %d, want %d", target.Generation, testPriorGen+1)
	}
	if res.Target != target || !res.Published || !res.Acknowledged || !res.AwaitHealth {
		t.Fatalf("result %+v", res)
	}
	if got := f.readSelection(); got != target {
		t.Fatalf("published selection %+v, want %+v", got, target)
	}
	f.requireJournal(state.JournalSelected, 3)
}

func TestSwitchRefusesWithoutPriorReceiptBeforeIntent(t *testing.T) {
	f := newFixture(t, true)
	prior := priorSelection()
	f.publishSelection(selection.Expected{Absent: true}, prior)
	before := f.rawSelection()

	// The journal records an explicit prior selection but no complete
	// prior receipt binds to it: refusal BEFORE any intent is written.
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrPriorReceiptMismatch) {
		t.Fatalf("err %v, want ErrPriorReceiptMismatch", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("prior selection changed on refusal")
	}

	// A receipt that does not bind to the journal's prior block is
	// refused the same way, still before any write.
	wrong := prior
	wrong.Generation = testPriorGen + 1
	_, err = Switch(context.Background(), f.requestWithReceipt(wrong, digestChar('c')))
	if !errors.Is(err, ErrPriorReceiptMismatch) {
		t.Fatalf("err %v, want ErrPriorReceiptMismatch", err)
	}
	f.requireJournal(state.JournalPrepared, 1)

	// A first-install journal (no prior recorded) must not be handed a
	// prior receipt either.
	f2 := newFixture(t, false)
	_, err = Switch(context.Background(), f2.requestWithReceipt(prior, digestChar('c')))
	if !errors.Is(err, ErrPriorReceiptMismatch) {
		t.Fatalf("err %v, want ErrPriorReceiptMismatch", err)
	}
	f2.requireJournal(state.JournalPrepared, 1)
	f2.requireSelectionAbsent()
}

func TestSwitchPublishFailureAfterIntentLeavesPriorUnchanged(t *testing.T) {
	f := newFixture(t, true)
	prior := priorSelection()
	f.publishSelection(selection.Expected{Absent: true}, prior)
	before := f.rawSelection()

	// The receipt binds to the journal but its RAW digest is wrong: the
	// durable switch-intent is recorded first (revisable, reconcilable)
	// and the publisher's re-read then refuses the stale expectation.
	_, err := Switch(context.Background(), f.requestWithReceipt(prior, digestChar('c')))
	if !errors.Is(err, selection.ErrConflict) {
		t.Fatalf("err %v, want selection.ErrConflict", err)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("prior selection changed after refused publish")
	}

	// Reentry with the correct complete receipt retries from the
	// durable intent and completes.
	res, err := Switch(context.Background(), f.requestWithReceipt(prior, rawDigest(t, before)))
	if err != nil {
		t.Fatalf("retry Switch: %v", err)
	}
	if !res.Published || !res.Acknowledged {
		t.Fatalf("retry result %+v", res)
	}
	if got := f.readSelection(); got != f.target() {
		t.Fatalf("published selection %+v, want %+v", got, f.target())
	}
	f.requireJournal(state.JournalSelected, 3)
}

func TestSwitchAckRecoversAfterPublishWithoutSecondWrite(t *testing.T) {
	f := newFixture(t, false)
	setHooks(t, nil, func() error { return errors.New("injected: lost after publish") })

	// The publication commits, the acknowledgement is lost: the journal
	// stays at the durable switch-intent.
	_, err := Switch(context.Background(), f.request())
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("err %v, want injected post-publish failure", err)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	published := f.rawSelection()
	if got := f.readSelection(); got != f.target() {
		t.Fatalf("published selection %+v, want target", got)
	}

	// Reentry acknowledges WITHOUT a second file write.
	setHooks(t, nil, nil)
	res, err := Switch(context.Background(), f.request())
	if err != nil {
		t.Fatalf("recovery Switch: %v", err)
	}
	if res.Published || !res.Acknowledged || !res.AwaitHealth {
		t.Fatalf("recovery result %+v", res)
	}
	if after := f.rawSelection(); string(after) != string(published) {
		t.Fatalf("recovery rewrote the selection file")
	}
	f.requireJournal(state.JournalSelected, 3)
}

func TestSwitchUncertainReadbackRefusesThenAmbiguousOnReentry(t *testing.T) {
	f := newFixture(t, false)
	wrong := f.target()
	wrong.StagedVersionDir = "other-staged"

	// The committed file is replaced before the acknowledgement readback:
	// the selected checkpoint must NOT be acknowledged.
	setHooks(t, nil, func() error {
		if err := os.WriteFile(f.selectionPath(), canonicalJSON(t, wrong), 0o600); err != nil {
			t.Fatalf("hook write: %v", err)
		}
		return nil
	})
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrReadbackMismatch) {
		t.Fatalf("err %v, want ErrReadbackMismatch", err)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if got := f.readSelection(); got != wrong {
		t.Fatalf("readback %+v, want the hook-written selection", got)
	}

	// Reentry classifies the mismatched selection against the durable
	// intent: ambiguity, and nothing is written (no fake completion, no
	// rewrite of the file, no rollback).
	setHooks(t, nil, nil)
	_, err = Switch(context.Background(), f.request())
	if !errors.Is(err, ErrAmbiguousState) {
		t.Fatalf("err %v, want ErrAmbiguousState", err)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if got := f.readSelection(); got != wrong {
		t.Fatalf("mismatched selection changed on refusal")
	}
}

func TestSwitchEffectWithoutIntentRefuses(t *testing.T) {
	f := newFixture(t, false)
	// A target selection exists while the journal is still at prepared:
	// an effect without recorded intent is refused — never the health or
	// completion path.
	f.publishSelection(selection.Expected{Absent: true}, f.target())
	before := f.rawSelection()
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrAmbiguousState) || !strings.Contains(err.Error(), "target-selection-without-switch-intent") {
		t.Fatalf("err %v, want ambiguity naming the missing switch intent", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("selection changed on refusal")
	}
}

func TestSwitchUnrelatedSelectionRefuses(t *testing.T) {
	f := newFixture(t, false)
	unrelated := activation.Selection{
		SchemaVersion:          activation.SelectionSchemaVersion,
		InstallID:              testInstall,
		Generation:             1,
		SelectedVersion:        "0.0.1",
		StagedVersionDir:       "staged-0.0.1",
		VerifiedArtifactSHA256: digestChar('f'),
	}
	if err := os.WriteFile(f.selectionPath(), canonicalJSON(t, unrelated), 0o600); err != nil {
		t.Fatalf("write unrelated selection: %v", err)
	}
	before := f.rawSelection()
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrAmbiguousState) {
		t.Fatalf("err %v, want ErrAmbiguousState", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("unrelated selection changed on refusal")
	}
}

func TestSwitchSelectedWithPriorActiveRefusesNoFakeCompletion(t *testing.T) {
	f := newFixture(t, true)
	prior := priorSelection()
	rawDigest := f.publishSelection(selection.Expected{Absent: true}, prior)
	if _, err := Switch(context.Background(), f.requestWithReceipt(prior, rawDigest)); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	f.requireJournal(state.JournalSelected, 3)

	// An uncooperative writer reverts the file to the recorded prior
	// while the journal claims selected: the claim is not effective and
	// no automatic change is proven — refusal with nothing written.
	if err := os.WriteFile(f.selectionPath(), canonicalJSON(t, prior), 0o600); err != nil {
		t.Fatalf("revert selection: %v", err)
	}
	before := f.rawSelection()
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrAmbiguousState) {
		t.Fatalf("err %v, want ErrAmbiguousState", err)
	}
	f.requireJournal(state.JournalSelected, 3)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("selection changed on refusal")
	}
}

func TestSwitchStaleOperationRefuses(t *testing.T) {
	f := newFixture(t, false)
	// Cancel the operation and start a successor: the named operation is
	// no longer current, so nothing may attach to its journal.
	adapter := state.NewAdapter(f.store)
	if _, err := adapter.Apply(context.Background(), operation.Input{Event: operation.EventCancel, OperationID: f.opID}); err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	snap2, err := adapter.Start(context.Background(), "3.0.0")
	if err != nil {
		t.Fatalf("start successor: %v", err)
	}
	if snap2.ID == f.opID {
		t.Fatalf("successor re-used the operation id")
	}
	_, err = Switch(context.Background(), f.request())
	if !errors.Is(err, ErrStaleOperation) {
		t.Fatalf("err %v, want ErrStaleOperation", err)
	}
	if j, rev, lerr := f.store.LoadActivationJournal(context.Background(), f.opID); lerr != nil ||
		j.Checkpoint != state.JournalPrepared || rev != 1 {
		t.Fatalf("journal %q rev %d lerr %v: stale request must write nothing", j.Checkpoint, rev, lerr)
	}
	f.requireSelectionAbsent()
}

func TestSwitchForeignLeaseAndForeignDirectoryRefuse(t *testing.T) {
	f := newFixture(t, false)
	otherDir := filepath.Join(f.root, "other-publication")
	if _, err := privdir.Ensure(otherDir); err != nil {
		t.Fatalf("privdir.Ensure: %v", err)
	}
	otherLock, err := exclusion.Acquire(context.Background(), otherDir, exclusion.Update)
	if err != nil {
		t.Fatalf("acquire foreign lease: %v", err)
	}
	defer otherLock.Close() //nolint:errcheck // test cleanup

	// A lease held for a DIFFERENT directory cannot guard this
	// directory's transaction.
	req := f.request()
	req.Lock = otherLock
	_, err = Switch(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrForeignDirectory) {
		t.Fatalf("err %v, want ErrInvalidRequest wrapping ErrForeignDirectory", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	f.requireSelectionAbsent()

	// A directory that is not the STORE's own is refused by the strict
	// store/directory association check (here it is also not the lease's
	// own, so the association refusal fires first — with nothing
	// written either way).
	req = f.request()
	req.Directory = otherDir
	_, err = Switch(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err %v, want ErrInvalidRequest for the non-store directory", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	f.requireSelectionAbsent()
}

// TestSwitchWrongStoreDirectoryAssociationRefusesBeforeJournalWrite is the
// regression for the store-versus-directory association hole: TWO REAL
// installations. Store A holds the operation and journal; the request
// instead names store B's own private directory with a lease legitimately
// held for exactly that directory. Both the lease and the directory are
// individually valid, so before the fix the transaction happily published
// store A's operation into store B's installation directory. The
// transaction must refuse BEFORE any journal write, with nothing published
// into either directory.
func TestSwitchWrongStoreDirectoryAssociationRefusesBeforeJournalWrite(t *testing.T) {
	f := newFixture(t, false)

	// A second, REAL installation: its own store, its own private
	// managed directory, and a legitimately held exclusive lease for it.
	rootB := t.TempDir()
	storeB, err := state.Open(rootB, "other-install")
	if err != nil {
		t.Fatalf("state.Open(second install): %v", err)
	}
	defer storeB.Close() //nolint:errcheck // test cleanup
	dirB := storeB.Directory()
	lockB, err := exclusion.Acquire(context.Background(), dirB, exclusion.Update)
	if err != nil {
		t.Fatalf("acquire second install lease: %v", err)
	}
	defer lockB.Close() //nolint:errcheck // test cleanup

	// Store A's journal + operation, store B's directory + lease: the
	// wrong association must refuse before any journal write.
	req := Request{Store: f.store, Directory: dirB, Lock: lockB, OpID: f.opID}
	_, err = Switch(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err %v, want ErrInvalidRequest for the wrong store/directory association", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	if _, lerr := os.Lstat(filepath.Join(dirB, selection.FileName)); !errors.Is(lerr, fs.ErrNotExist) {
		t.Fatalf("selection appeared in the foreign store's directory: %v", lerr)
	}
	f.requireSelectionAbsent()
}

func TestSwitchUnsafePublicationDirectoryRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory-permission policy; windows DACL policy is covered by the privdir package")
	}
	f := newFixture(t, true)
	prior := priorSelection()
	rawDigest := f.publishSelection(selection.Expected{Absent: true}, prior)
	// The durable intent is already recorded, so an unsafe publication
	// directory is refused by the publisher with NOTHING further written
	// by this transaction.
	f.advanceJournal(state.JournalSwitchIntent)
	if err := os.Chmod(f.dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	before := f.rawSelection()
	_, err := Switch(context.Background(), f.requestWithReceipt(prior, rawDigest))
	if !errors.Is(err, selection.ErrUnsafeDirectory) {
		t.Fatalf("err %v, want selection.ErrUnsafeDirectory", err)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("prior selection changed on refusal")
	}
	// Restoring the private mode lets the same durable intent retry.
	if err := os.Chmod(f.dir, 0o700); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	res, err := Switch(context.Background(), f.requestWithReceipt(prior, rawDigest))
	if err != nil {
		t.Fatalf("retry Switch: %v", err)
	}
	if !res.Published || !res.Acknowledged {
		t.Fatalf("retry result %+v", res)
	}
	f.requireJournal(state.JournalSelected, 3)
}

func TestSwitchContextCancellationLeavesReconcilableJournal(t *testing.T) {
	f := newFixture(t, false)

	// Cancellation after the journal load but before the intent save:
	// nothing is written and no file appears.
	ctx, cancel := context.WithCancel(context.Background())
	setHooks(t, func(state.ActivationJournal, int64) error { cancel(); return nil }, nil)
	_, err := Switch(ctx, f.request())
	if err == nil {
		t.Fatalf("canceled Switch must fail")
	}
	f.requireJournal(state.JournalPrepared, 1)
	f.requireSelectionAbsent()

	// A pre-canceled context is refused with no writes at all.
	setHooks(t, nil, nil)
	canceled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	_, err = Switch(canceled, f.request())
	if err == nil {
		t.Fatalf("pre-canceled Switch must fail")
	}
	f.requireJournal(state.JournalPrepared, 1)
	f.requireSelectionAbsent()

	// Cancellation with a durable intent at switch-intent and a target
	// selection already committed leaves a reconcilable journal; the
	// reentry acknowledges without a second write.
	f.advanceJournal(state.JournalSwitchIntent)
	f.publishSelection(selection.Expected{Absent: true}, f.target())
	before := f.rawSelection()
	hookCtx, hookCancel := context.WithCancel(context.Background())
	setHooks(t, func(state.ActivationJournal, int64) error { hookCancel(); return nil }, nil)
	_, err = Switch(hookCtx, f.request())
	if err == nil {
		t.Fatalf("post-intent cancellation must fail rather than fake completion")
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if string(f.rawSelection()) != string(before) {
		t.Fatalf("committed selection changed by a canceled transaction")
	}
	setHooks(t, nil, nil)
	res, err := Switch(context.Background(), f.request())
	if err != nil {
		t.Fatalf("recovery Switch: %v", err)
	}
	if res.Published || !res.Acknowledged {
		t.Fatalf("recovery result %+v", res)
	}
	f.requireJournal(state.JournalSelected, 3)
}

func TestSwitchMissingJournalRefuses(t *testing.T) {
	// The transaction creates journals nowhere: an operation without a
	// prepared journal is refused before any file effect.
	f := newFixtureNoJournal(t)
	_, err := Switch(context.Background(), f.request())
	if !errors.Is(err, ErrJournalMissing) {
		t.Fatalf("err %v, want ErrJournalMissing", err)
	}
	f.requireSelectionAbsent()
}

func TestSwitchMalformedRequestsRefuse(t *testing.T) {
	f := newFixture(t, false)
	req := f.request()

	nilStore := req
	nilStore.Store = nil
	if _, err := Switch(context.Background(), nilStore); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil store: err %v", err)
	}
	nilLock := req
	nilLock.Lock = nil
	if _, err := Switch(context.Background(), nilLock); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil lease: err %v", err)
	}
	zeroOp := req
	zeroOp.OpID = 0
	if _, err := Switch(context.Background(), zeroOp); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero op id: err %v", err)
	}
	dirtyDir := req
	dirtyDir.Directory = "relative/directory"
	if _, err := Switch(context.Background(), dirtyDir); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unclean directory: err %v", err)
	}
	if _, err := Switch(nil, req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context: err %v", err)
	}
	f.requireJournal(state.JournalPrepared, 1)
	f.requireSelectionAbsent()
}

func TestSwitchConcurrentReentriesSerializeOnOneGuard(t *testing.T) {
	f := newFixture(t, false)
	const workers = 4
	var inside, peak atomic.Int64
	setHooks(t, func(state.ActivationJournal, int64) error {
		c := inside.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond) // widen the race window
		inside.Add(-1)
		return nil
	}, nil)

	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine reuses its own copy of the request.
			r := Request{Store: f.store, Directory: f.dir, Lock: f.lock, OpID: f.opID}
			_, err := Switch(context.Background(), r)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Switch: %v", err)
		}
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrent guarded sections %d, want 1", got)
	}
	f.requireJournal(state.JournalSelected, 3)
	if got := f.readSelection(); got != f.target() {
		t.Fatalf("final selection %+v, want %+v", got, f.target())
	}
}
