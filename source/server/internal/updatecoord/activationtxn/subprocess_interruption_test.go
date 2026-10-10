package activationtxn

// Subprocess interruption tests: the child half of this fixture re-runs
// THIS test binary (the exclusion package's established worker pattern)
// and hard-exits with os.Exit exactly at one of the transaction's two
// durable boundaries — after the switch-intent checkpoint is recorded
// (testHookAfterIntent) and after the publication commit but before the
// selected acknowledgement (testHookAfterPublish). A real process death
// there releases the OS-backed exclusion lease and the SQLite world is
// left exactly as an abrupt interruption leaves it; the parent then
// reaps the child and proves recovery with a FRESH store and a FRESH
// lease. Everything lives in the test's own temporary root: no live
// agent, no default location, no production state is touched, and the
// hooks are the package's existing private test hooks — no new
// production API, no rollback, no health.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/selection"
	"cercano/source/server/internal/updatecoord/state"
)

// Subprocess fixture environment: the parent names the temporary root,
// the current operation, and which boundary the child must die at.
const (
	swFixtureEnv     = "CERCANO_SWITCH_FIXTURE"
	swRootEnv        = "CERCANO_SWITCH_FIXTURE_ROOT"
	swOpIDEnv        = "CERCANO_SWITCH_FIXTURE_OPID"
	swPhaseEnv       = "CERCANO_SWITCH_FIXTURE_PHASE"
	swPhaseIntent    = "intent"    // die right after the durable switch-intent checkpoint
	swPhasePublished = "published" // die right after the publication commit, before the ack
	// Distinct exit codes prove the child reached the named boundary
	// (they are only reachable from inside the corresponding hook).
	swExitAfterIntent   = 42
	swExitAfterPublish  = 43
	swChildTimeout      = 50 * time.Second
	swParentWaitTimeout = 60 * time.Second
)

// TestSwitchSubprocessWorker is the CHILD half. It only runs when the
// parent sets the fixture environment; every other run skips. It opens
// the parent's temporary state root as a fresh process would, acquires
// the exclusive update lease on the store's own directory, and runs one
// Switch transaction that never returns: the installed hook exits the
// process at the requested boundary. The exit is the interruption being
// tested — no defers, no Close, no cleanup.
func TestSwitchSubprocessWorker(t *testing.T) {
	if os.Getenv(swFixtureEnv) != "yes" {
		t.Skip("subprocess helper")
	}
	root := os.Getenv(swRootEnv)
	if root == "" {
		t.Fatal("fixture root not named")
	}
	opID, err := strconv.ParseInt(os.Getenv(swOpIDEnv), 10, 64)
	if err != nil || opID <= 0 {
		t.Fatalf("fixture op id %q", os.Getenv(swOpIDEnv))
	}
	phase := os.Getenv(swPhaseEnv)
	store, err := state.Open(root, testInstall)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	dir := store.Directory()
	ctx, cancel := context.WithTimeout(context.Background(), swChildTimeout)
	defer cancel()
	lock, err := exclusion.Acquire(ctx, dir, exclusion.Update)
	if err != nil {
		t.Fatalf("acquire exclusion lease: %v", err)
	}
	switch phase {
	case swPhaseIntent:
		testHookAfterIntent = func() error { os.Exit(swExitAfterIntent); return nil }
	case swPhasePublished:
		testHookAfterPublish = func() error { os.Exit(swExitAfterPublish); return nil }
	default:
		t.Fatalf("unknown fixture phase %q", phase)
	}
	// Reaching the return means the hook never fired: the boundary was
	// not the one this fixture names, and the child must not silently
	// complete the transaction.
	res, err := Switch(ctx, Request{Store: store, Directory: dir, Lock: lock, OpID: opID, Images: fixtureImages(dir)})
	t.Fatalf("subprocess fixture returned from Switch at phase %q (result %+v, err %v) instead of exiting at the boundary", phase, res, err)
}

// runInterruptingChild starts the child fixture for the given phase and
// returns after a BOUNDED wait and reap, proving the child died exactly
// at the named boundary.
func runInterruptingChild(t *testing.T, root string, opID int64, phase string, wantExit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), swParentWaitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSwitchSubprocessWorker$", "-test.timeout="+swChildTimeout.String())
	cmd.Env = append(os.Environ(),
		swFixtureEnv+"=yes",
		swRootEnv+"="+root,
		swOpIDEnv+"="+strconv.FormatInt(opID, 10),
		swPhaseEnv+"="+phase,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess fixture: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("subprocess fixture did not exit at the %q boundary within %v; output:\n%s", phase, swParentWaitTimeout, out.String())
	}
	var ee *exec.ExitError
	if !errors.As(waitErr, &ee) || ee.ExitCode() != wantExit {
		t.Fatalf("subprocess fixture exit %v, want exit code %d at the %q boundary; output:\n%s", waitErr, wantExit, phase, out.String())
	}
}

// TestSwitchSubprocessInterruptionRecovers proves the crash-recovery
// contract end to end with real process death:
//
//   - intent: the child dies after the durable switch-intent checkpoint
//     but before any publication; the parent's fresh Switch PUBLISHES
//     and acknowledges.
//   - published: the child dies after the publication commit but before
//     the selected acknowledgement; the parent's fresh Switch
//     acknowledges WITHOUT a second write — no rewrite, no file identity
//     change.
//   - a second resume in both cases is idempotent: no write at all.
func TestSwitchSubprocessInterruptionRecovers(t *testing.T) {
	for _, phase := range []string{swPhaseIntent, swPhasePublished} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, false) // first install: prepared journal, no prior receipt

			// Hand the whole world to the child exactly as a fresh
			// writer process would receive it: no held lease, no open
			// store in this parent process.
			if err := f.lock.Close(); err != nil {
				t.Fatalf("release parent lease: %v", err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatalf("close parent store: %v", err)
			}

			wantExit := swExitAfterIntent
			if phase == swPhasePublished {
				wantExit = swExitAfterPublish
			}
			runInterruptingChild(t, f.root, f.opID, phase, wantExit)

			// Recovery world: a FRESH store and a FRESH lease, opened
			// only after the child's death was reaped.
			ctx := context.Background()
			store, err := state.Open(f.root, testInstall)
			if err != nil {
				t.Fatalf("fresh state.Open: %v", err)
			}
			defer store.Close()
			lock, err := exclusion.Acquire(ctx, f.dir, exclusion.Update)
			if err != nil {
				t.Fatalf("fresh exclusion lease: %v", err)
			}
			defer lock.Close()

			// The interruption left a reconcilable world: durable
			// switch-intent, and the selection effect present exactly
			// when the child got past the publication boundary.
			j, rev, err := store.LoadActivationJournal(ctx, f.opID)
			if err != nil {
				t.Fatalf("load journal after interruption: %v", err)
			}
			if j.Checkpoint != state.JournalSwitchIntent || rev != 2 {
				t.Fatalf("post-crash journal %q rev %d, want switch-intent rev 2", j.Checkpoint, rev)
			}
			target, err := targetSelection(j)
			if err != nil {
				t.Fatalf("derive target: %v", err)
			}
			selectionPath := filepath.Join(f.dir, selection.FileName)
			var beforeBytes []byte
			var beforeInfo os.FileInfo
			if phase == swPhasePublished {
				beforeBytes, err = os.ReadFile(selectionPath)
				if err != nil {
					t.Fatalf("read selection after interruption: %v", err)
				}
				beforeInfo, err = os.Stat(selectionPath)
				if err != nil {
					t.Fatalf("stat selection after interruption: %v", err)
				}
			} else if _, err := os.Lstat(selectionPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("selection file exists (or stat error) after intent-boundary death: %v", err)
			}

			// The recovery transaction itself.
			res, err := Switch(ctx, Request{Store: store, Directory: f.dir, Lock: lock, OpID: f.opID, Images: fixtureImages(f.dir)})
			if err != nil {
				t.Fatalf("recovery Switch: %v", err)
			}
			switch phase {
			case swPhaseIntent:
				// Durable intent was already recorded, so recovery
				// PUBLISHES the target and acknowledges it.
				if !res.Published || !res.Acknowledged || !res.AwaitHealth {
					t.Fatalf("intent recovery flags: published=%v acknowledged=%v awaitHealth=%v", res.Published, res.Acknowledged, res.AwaitHealth)
				}
			default:
				// The effect already happened under the dead process's
				// lease: recovery acknowledges WITHOUT a rewrite — the
				// file's bytes and identity are untouched.
				if res.Published || !res.Acknowledged || !res.AwaitHealth {
					t.Fatalf("published recovery flags: published=%v acknowledged=%v awaitHealth=%v", res.Published, res.Acknowledged, res.AwaitHealth)
				}
			}
			if res.Target != target || res.Checkpoint != state.JournalSelected || res.JournalRevision != 3 {
				t.Fatalf("recovery result target %+v checkpoint %q rev %d, want target %+v selected rev 3", res.Target, res.Checkpoint, res.JournalRevision, target)
			}
			got, err := activation.ReadSelection(selectionPath)
			if err != nil {
				t.Fatalf("read selection after recovery: %v", err)
			}
			if got != target {
				t.Fatalf("active selection %+v, want %+v", got, target)
			}
			afterBytes, err := os.ReadFile(selectionPath)
			if err != nil {
				t.Fatalf("read selection bytes after recovery: %v", err)
			}
			if phase == swPhasePublished {
				if string(afterBytes) != string(beforeBytes) {
					t.Fatalf("published-case recovery rewrote the selection bytes")
				}
				afterInfo, err := os.Stat(selectionPath)
				if err != nil {
					t.Fatalf("stat selection after recovery: %v", err)
				}
				if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) || afterInfo.Size() != beforeInfo.Size() {
					t.Fatalf("published-case recovery changed the selection file identity: size %d->%d, mtime %v->%v",
						beforeInfo.Size(), afterInfo.Size(), beforeInfo.ModTime(), afterInfo.ModTime())
				}
			}

			// Second resume is idempotent: nothing is written or
			// acknowledged again, and health verification stays the
			// named next step.
			resumeBytes := afterBytes
			res2, err := Switch(ctx, Request{Store: store, Directory: f.dir, Lock: lock, OpID: f.opID, Images: fixtureImages(f.dir)})
			if err != nil {
				t.Fatalf("second resume Switch: %v", err)
			}
			if res2.Published || res2.Acknowledged || !res2.AwaitHealth {
				t.Fatalf("second resume flags: published=%v acknowledged=%v awaitHealth=%v", res2.Published, res2.Acknowledged, res2.AwaitHealth)
			}
			if res2.JournalRevision != 3 {
				t.Fatalf("second resume advanced journal revision to %d", res2.JournalRevision)
			}
			finalBytes, err := os.ReadFile(selectionPath)
			if err != nil {
				t.Fatalf("read selection after second resume: %v", err)
			}
			if string(finalBytes) != string(resumeBytes) {
				t.Fatalf("second resume rewrote the selection file")
			}
		})
	}
}
