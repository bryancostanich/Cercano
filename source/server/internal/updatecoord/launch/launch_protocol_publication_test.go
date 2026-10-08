package launch

// Concurrent-reader stress and native candidate tests for the fixture
// protocol's WRITE-ONCE publication (see publishFixtureFile).
//
// History. CI run 37701866968 failed TestOneShotLaunch_DiscardedOutputNeverBlocks
// with "owned-echo harness failed: " and an EMPTY reason: the protocol
// analysis of the owned-echo harness (launch_harness_windows_test.go)
// showed every harness path publishes its result before exiting, so the
// empty result was the fixture's polling reader observing the file
// BETWEEN the writer's create/truncate and its content write. The first
// fix published via temp+rename (replace). CI run 37706670877 then
// proved the replace primitive itself is the wrong shape for this
// protocol: on Windows the publication stress still hit ACCESS_DENIED
// replacing the very destination the concurrent readers polled, even
// though the fixture readers open with FILE_SHARE_DELETE.
//
// The call-site audit resolved the contract BEFORE the fix: every
// publishFixtureFile call site (every marker, pidfile, terminal result
// and release path) publishes its protocol path ONCE — the fixtures
// publish immutable terminal values, and NO call site ever replaces a
// published value. Only this file's own stress had rewritten one path
// 300 times, which was a test artifact, not a protocol value. So the
// publication is now create-if-absent: the COMPLETE temp file is
// hard-linked into place, the link fails if a destination already
// exists (link(2) EEXIST on APFS/ext4, CreateHardLinkW
// ERROR_FILE_EXISTS on NTFS), identical republish is an idempotent
// replay, and a conflicting republish is refused. Nothing ever opens,
// replaces or deletes an existing destination, so the ACCESS_DENIED
// replace window cannot exist at all.
//
// The tests below pin that contract natively:
//   - the stress: concurrent readers of MANY write-once publications,
//     every observed read complete-or-absent (byte-exact, never a
//     len>0-only check — the exact CI 37701866968 partial-read failure
//     mode is what the byte-exact comparison catches);
//   - exclusive publication: racing identical publishers produce
//     exactly one publication, every racer succeeds idempotently;
//   - conflicting-content refusal: a different value over an existing
//     one is refused and the existing value is left unchanged;
//   - conflicting race: racing DIFFERENT publishers produce exactly
//     one complete winner, every loser is refused, and the survivor is
//     byte-exactly one racer's payload.
//
// TEST FIXTURE PROTOCOL ONLY. This write-once publication is the test
// fixture protocol; it is distinct from the eventual production file
// activation (atomic replacement of a production file, e.g. on
// Windows), which this package does NOT implement and which nothing
// here weakens or promises. No global policy is involved on any
// platform.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestFixtureProtocolPublicationIsWriteOnceToPollingReader stresses the
// real protocol shape: MANY distinct write-once publications, each
// published once (plus one identical idempotent replay to exercise the
// replay path under the readers), hammered by concurrent polling
// readers. Every successful read must be the COMPLETE publication —
// byte-exact equality, not a length check — because the failure mode
// being pinned (CI 37701866968) was an empty/partial observation, and a
// skipped or weakened comparison would skip exactly that check. A read
// error means absent, the other legal state.
func TestFixtureProtocolPublicationIsWriteOnceToPollingReader(t *testing.T) {
	dir := t.TempDir()
	const publications = 64
	const readers = 4

	paths := make([]string, publications)
	payloads := make([][]byte, publications)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("result-%03d", i))
		// Path-unique payload (256 KiB, far larger than any real protocol
		// result) so any observed byte can be attributed to exactly one
		// publication.
		buf := bytes.Repeat([]byte{byte('a' + i%26)}, 1<<18)
		copy(buf, []byte(fmt.Sprintf("publication-%03d\n", i)))
		payloads[i] = buf
	}

	var writerWG sync.WaitGroup
	writerErr := make(chan error, publications)
	for i := range paths {
		writerWG.Add(1)
		go func(i int) {
			defer writerWG.Done()
			if err := publishFixtureFile(paths[i], payloads[i]); err != nil {
				writerErr <- fmt.Errorf("publication %d: %w", i, err)
				return
			}
			// Identical republish is part of the protocol surface
			// (idempotent replay): the existing value must be left
			// untouched and the call must succeed.
			if err := publishFixtureFile(paths[i], payloads[i]); err != nil {
				writerErr <- fmt.Errorf("identical replay %d: %w", i, err)
			}
		}(i)
	}

	// Concurrent polling readers — the fixture-side access pattern that
	// read the empty owned-echo result in CI run 37701866968. Reads run
	// without sleeping to hammer any create-to-write window; every
	// successful read must be the complete payload of the path it read.
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	var polls, completes int64
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func(seed int) {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				i := (seed + int(atomic.AddInt64(&polls, 1))) % publications
				data, err := readFixtureFile(paths[i])
				if err != nil {
					// Absent: the other legal state (the publication has
					// not landed yet). Keep polling.
					continue
				}
				if !bytes.Equal(data, payloads[i]) {
					t.Errorf("polling reader observed a non-complete protocol file %s: %d bytes (empty or partial publication — the exact CI 37701866968 failure mode)", paths[i], len(data))
					return
				}
				atomic.AddInt64(&completes, 1)
			}
		}(r)
	}

	writerWG.Wait()
	// Let the readers drain the final state before stopping them.
	time.Sleep(10 * time.Millisecond)
	close(stop)
	readerWG.Wait()
	select {
	case err := <-writerErr:
		t.Fatalf("publication writer: %v", err)
	default:
	}

	// Every publication landed COMPLETE — none may be missing.
	for i := range paths {
		data, err := readFixtureFile(paths[i])
		if err != nil {
			t.Fatalf("read final publication %s: %v", paths[i], err)
		}
		if !bytes.Equal(data, payloads[i]) {
			t.Fatalf("final publication %s = %d bytes, want the complete payload (%d bytes)", paths[i], len(data), len(payloads[i]))
		}
	}
	// How much of the concurrent window this run exercised. The readers
	// poll without sleeping, so the counts make the exercised window
	// visible in test output instead of guessed.
	t.Logf("write-once publication observed: %d polls, %d complete reads across %d publications", polls, completes, publications)

	// Every published destination keeps its protocol properties: regular
	// file, owner-only permissions (the temp sibling is created by the
	// same process in the same directory and the link preserves it).
	// No temp siblings may linger beside the destinations either.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != publications {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("leaked or missing publications beside the destinations: %v", names)
	}
	for i := range paths {
		info, err := os.Stat(paths[i])
		if err != nil {
			t.Fatalf("stat published file %s: %v", paths[i], err)
		}
		if !info.Mode().IsRegular() {
			t.Errorf("published file %s mode = %v, want a regular file", paths[i], info.Mode())
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("published file %s mode = %o, want group/other bits clear (owner-only)", paths[i], perm)
		}
	}
}

// TestFixtureProtocolPublicationIsExclusive races many IDENTICAL
// publishers against one path: the link is create-if-absent, so
// exactly one publication can land and every other racer must observe
// the identical existing value as an idempotent replay — all racers
// succeed, and the directory holds exactly one complete file.
func TestFixtureProtocolPublicationIsExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result")
	payload := []byte("exclusive-publication-value")

	const racers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for r := 0; r < racers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- publishFixtureFile(path, payload)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical racing publisher failed: %v", err)
		}
	}
	data, err := readFixtureFile(path)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("published file = %q, want %q", data, payload)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("racing identical publishers left %d entries, want exactly the one destination: %v", len(entries), names)
	}
}

// TestFixtureProtocolPublicationRefusesConflictingValue proves the
// never-overwrite half of the contract: a DIFFERENT value republished
// over an existing protocol value is refused, and the existing value is
// left byte-identical with its permissions untouched.
func TestFixtureProtocolPublicationRefusesConflictingValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result")
	if err := publishFixtureFile(path, []byte("first-value")); err != nil {
		t.Fatalf("first publication: %v", err)
	}
	err := publishFixtureFile(path, []byte("different-value"))
	if err == nil {
		t.Fatalf("conflicting republication succeeded, want refusal")
	}
	if want := "refusing to republish"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Errorf("refusal error = %q, want it to state %q", err.Error(), want)
	}
	// The existing value is unchanged — never overwritten.
	data, err := readFixtureFile(path)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if !bytes.Equal(data, []byte("first-value")) {
		t.Fatalf("published file = %q after refused republication, want the untouched first value", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat published file: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("published file mode = %o, want group/other bits clear (owner-only)", perm)
	}
	// No temp sibling leaked from the refused attempt.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("refused republication leaked temp siblings: %v", names)
	}
}

// TestFixtureProtocolPublicationConflictingRaceKeepsOneCompleteValue
// races DIFFERENT publishers against one path: at most one publication
// can land (create-if-absent), so the survivor must be byte-exactly one
// racer's complete payload — never a mix, never a partial — and every
// loser must be refused with the existing value left as the winner's.
func TestFixtureProtocolPublicationConflictingRaceKeepsOneCompleteValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result")

	const racers = 16
	payloads := make([][]byte, racers)
	for r := range payloads {
		// 64 KiB payloads: any torn or mixed observation cannot equal
		// ANY racer's payload, so the byte-exact final check below
		// catches it.
		payloads[r] = bytes.Repeat([]byte{byte('A' + r)}, 1<<16)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for r := 0; r < racers; r++ {
		wg.Add(1)
		go func(payload []byte) {
			defer wg.Done()
			<-start
			errs <- publishFixtureFile(path, payload)
		}(payloads[r])
	}
	close(start)
	wg.Wait()
	close(errs)
	refused := 0
	for err := range errs {
		if err == nil {
			continue
		}
		if want := "refusing to republish"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("losing racer error = %q, want the refusal %q", err.Error(), want)
		}
		refused++
	}
	if refused > racers-1 {
		t.Fatalf("all %d racers refused, want exactly one publication to land", racers)
	}
	// The survivor is ONE racer's COMPLETE payload.
	data, err := readFixtureFile(path)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	for r := range payloads {
		if bytes.Equal(data, payloads[r]) {
			// Exactly one publication landed; the directory holds only
			// the destination.
			entries, lerr := os.ReadDir(dir)
			if lerr != nil {
				t.Fatalf("read dir: %v", lerr)
			}
			if len(entries) != 1 {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("conflicting race left %d entries, want exactly the one destination: %v", len(entries), names)
			}
			return
		}
	}
	t.Fatalf("published file = %d bytes matching no racer's complete payload (torn or mixed publication)", len(data))
}
