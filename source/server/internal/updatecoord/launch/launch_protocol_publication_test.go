package launch

// Backed test for the fixture protocol's atomic result publication (see
// publishFixtureFile).
//
// CI run 37701866968 failed TestOneShotLaunch_DiscardedOutputNeverBlocks
// with "owned-echo harness failed: " and an EMPTY reason. The protocol
// analysis of the owned-echo harness (launch_harness_windows_test.go)
// showed every harness path — owned-job entry failure, launch refusal,
// launch success — publishes its result before exiting, so an empty
// result could not be an unreported harness failure: the fixture's
// polling reader had observed the result file BETWEEN the writer's
// create/truncate and its content write. The publication is now
// temp+rename (complete or absent), and this test pins that exact
// contract: a concurrent polling reader must NEVER observe an empty or
// partially written protocol file, only absent or complete content.
//
// The payload is deliberately large (256 KiB per publication, far larger
// than any real protocol result) and the reader polls without sleeping,
// so the pre-fix publication (direct os.WriteFile) is hammered through
// its create-to-write window hundreds of times per run; the fixed
// publication is guaranteed complete on every supported platform
// (rename-replace is atomic on POSIX and MoveFileEx(REPLACE_EXISTING)
// on Windows).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFixtureProtocolPublicationIsAtomicToPollingReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "echo-result")

	payloadA := bytes.Repeat([]byte("A"), 1<<18)
	payloadB := bytes.Repeat([]byte("B"), 1<<18)

	const publications = 300
	writerDone := make(chan error, 1)
	go func() {
		for i := 0; i < publications; i++ {
			payload := payloadA
			if i%2 == 1 {
				payload = payloadB
			}
			if err := publishFixtureFile(path, payload); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	// The polling reader — exactly the fixture-side access pattern that
	// read the empty owned-echo result in CI run 37701866968. Every
	// successful read must be a COMPLETE publication; an empty or partial
	// read is the failure mode being pinned, so it fails the test with
	// the observed byte count (real data, never a bare assertion).
	reads, completeA, completeB := 0, false, false
	writerErr := error(nil)
	finished := false
	deadline := time.Now().Add(60 * time.Second)
	for !finished {
		select {
		case writerErr = <-writerDone:
			finished = true
		default:
			if time.Now().After(deadline) {
				t.Fatalf("publication writer did not finish within 60s")
			}
		}
		if data, err := readFixtureFile(path); err == nil {
			reads++
			switch {
			case bytes.Equal(data, payloadA):
				completeA = true
			case bytes.Equal(data, payloadB):
				completeB = true
			default:
				t.Fatalf("polling reader observed a non-complete protocol file: %d bytes (empty or partial publication — the exact CI 37701866968 failure mode)", len(data))
			}
		}
	}
	if writerErr != nil {
		t.Fatalf("publication writer: %v", writerErr)
	}
	// One read after the writer finished: the final, fully published
	// state must be the last payload, complete.
	reads++
	if data, err := readFixtureFile(path); err != nil {
		t.Fatalf("read final publication: %v", err)
	} else if !bytes.Equal(data, payloadB) {
		t.Fatalf("final publication = %d bytes, want the complete last payload (%d bytes)", len(data), len(payloadB))
	}
	// How much of the concurrent window this run exercised. The reader
	// polls without sleeping and the writer publishes %d times, so both
	// payload states are normally observed; reporting the counts makes
	// the exercised window visible in test output instead of guessed.
	t.Logf("atomic publication observed: %d reads, complete-A=%v complete-B=%v", reads, completeA, completeB)

	// The published destination keeps its protocol properties: regular
	// file, owner-only permissions (same owner as the publisher — the
	// temp sibling is created by the same process in the same directory).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat published file: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("published file mode = %v, want a regular file", info.Mode())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("published file mode = %o, want group/other bits clear (owner-only)", perm)
	}
	// No temp siblings may linger beside the destination after
	// publication.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp publish siblings leaked beside the destination: %v", names)
	}
}
