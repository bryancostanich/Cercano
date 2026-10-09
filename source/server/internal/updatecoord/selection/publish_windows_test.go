//go:build windows

package selection

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Windows-native identity fixtures, run by the native CI job only.
//
// Background (the CI 37987544934 failure mechanism): on Windows, a
// FileInfo returned by os.Stat/os.Lstat for a PATH does not carry the
// file's native ID. The ID (volume serial + file index) is resolved
// lazily, by RE-OPENING THE SAVED PATH the first time os.SameFile needs
// it (os/types_windows.go: fileStat.loadFileId). An "identity" captured
// by path BEFORE a replacement therefore silently resolves to the
// REPLACEMENT at comparison time — a false same-inode verdict that
// masks exactly the staging-name reuse the production guard exists to
// catch, and it is why a fixture that pinned its before-identity via
// os.Stat compared "same inode" against the swapped-in file and failed.
//
// The identity this package's PRODUCTION code pins never has that
// hazard: publishGuarded pins it with tmp.Stat() on the STILL-OPEN
// staging handle (publish.go), whose FileInfo already carries the
// native file ID (newFileStatFromGetFileInformationByHandle sets
// fileStat.path to "" precisely so os.SameFile never re-resolves it),
// before the handle is closed. The fixtures below prove both halves
// with a fully owned staging fixture.

// TestWindowsPathStatIdentityResolvesLazilyByPath reproduces the actual
// failure mechanism with an owned fixture: a path-captured os.Stat
// identity from BEFORE the replacement compares same-inode with the
// replacement (lazily resolved by the current name), while the
// production-faithful handle-pinned identity detects the replacement.
func TestWindowsPathStatIdentityResolvesLazilyByPath(t *testing.T) {
	dir := newPrivateDir(t)
	owned, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.Write([]byte("staged")); err != nil {
		t.Fatal(err)
	}
	// The production-faithful pin: the open creation handle's identity
	// (native file ID, never re-resolved by path).
	pinned, err := owned.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// The broken-by-construction pin: a path-derived FileInfo captured
	// BEFORE the replacement — exactly like the fixture that failed CI.
	byPath, err := os.Stat(owned.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	// Replace the staging name with a byte-identical, different file
	// (created first, then renamed over, so it cannot be the original).
	swapPath := filepath.Join(dir, "fixture-owned-swap")
	if err := os.WriteFile(swapPath, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swapPath, owned.Name()); err != nil {
		t.Fatal(err)
	}
	now, err := os.Stat(owned.Name())
	if err != nil {
		t.Fatal(err)
	}
	// The actual Windows behavior: the path-captured identity resolves
	// lazily to whatever holds the NAME now, so it matches the
	// replacement — an untrustworthy pin that must never be used for
	// ownership proof.
	if !os.SameFile(byPath, now) {
		t.Fatal("expected a path-captured os.Stat identity to lazily resolve to the replacement (the CI failure mechanism); if Windows now resolves eagerly, revisit the fixtures")
	}
	// The handle-pinned identity stays the ORIGINAL file's native ID and
	// correctly detects the replacement.
	if os.SameFile(pinned, now) {
		t.Fatal("handle-pinned identity matched the replacement; the pinned native file ID is not truthful")
	}
}

// TestWindowsPinnedIdentityGuardRetainsReplacement proves the production
// staging-removal guard stays truthful on Windows with the handle-pinned
// identity: a byte-identical replacement at the staging name (same size,
// same mode, different native file ID — only identity can catch it) is
// retained and reported, never removed.
func TestWindowsPinnedIdentityGuardRetainsReplacement(t *testing.T) {
	dir := newPrivateDir(t)
	owned, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		t.Fatal(err)
	}
	staged := []byte("staged")
	if _, err := owned.Write(staged); err != nil {
		t.Fatal(err)
	}
	identity, err := owned.Stat() // pinned from the open handle, before close
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	// Byte-identical replacement: size and mode alone prove nothing;
	// only the pinned native file ID can catch the swap.
	swapPath := filepath.Join(dir, "fixture-owned-swap")
	if err := os.WriteFile(swapPath, staged, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swapPath, owned.Name()); err != nil {
		t.Fatal(err)
	}
	if err := updateStagingAfterCommit(dir, owned.Name(), identity); !errors.Is(err, ErrStagingRetained) {
		t.Fatalf("updateStagingAfterCommit(swap) err = %v; want ErrStagingRetained (a byte-identical replacement must not be removed)", err)
	}
	got, rerr := os.ReadFile(owned.Name())
	if rerr != nil || !bytes.Equal(got, staged) {
		t.Fatalf("replacement not preserved: (%q, %v)", got, rerr)
	}
}
