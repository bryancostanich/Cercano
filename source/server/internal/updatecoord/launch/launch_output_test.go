package launch

// Output-stream validation tests: a log path that is relative, or that
// points at a symlink or other non-regular file, must be refused BEFORE
// any child starts. The child's output must land only in a caller-owned
// regular file, so no misconfiguration can hand the update utility a
// pipe it would block on or a link someone else owns.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A relative output path must be refused: it would resolve against the
// parent's working directory, not against a caller-chosen trusted
// location.
func TestOneShotLaunch_RejectsRelativeOutputPath(t *testing.T) {
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	dir := t.TempDir()
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		Dir:        dir,
		StdoutPath: "relative-out.log",
	})
	if !errors.Is(err, ErrInvalidOutputPath) {
		t.Fatalf("want ErrInvalidOutputPath, got err=%v proc=%v", err, proc)
	}
	if proc != nil {
		t.Fatalf("process returned alongside error: %v", proc)
	}
	if _, err := os.Stat(filepath.Join(dir, "relative-out.log")); !os.IsNotExist(err) {
		t.Errorf("relative-out.log was created under Dir: stat err = %v", err)
	}
}

// A symlink at the log path must be refused even when its target is a
// perfectly fine regular file: the child would write through a link the
// caller never vetted, and the primitive never resolves links itself.
func TestOneShotLaunch_RejectsSymlinkOutputPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.log")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		// Creating symlinks is a privilege on Windows; without it this
		// case cannot be exercised here.
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: link,
	})
	if !errors.Is(err, ErrInvalidOutputPath) {
		t.Fatalf("want ErrInvalidOutputPath, got err=%v proc=%v", err, proc)
	}
	if proc != nil {
		t.Fatalf("process returned alongside error: %v", proc)
	}
	// Nothing may have been written through the link.
	data, err := readFixtureFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("child wrote %d bytes through the rejected symlink", len(data))
	}
}

// An output path whose parent directory does not exist fails with
// ErrOutputSetup, before any child starts.
func TestOneShotLaunch_OutputLogOpenFailureFailsClosed(t *testing.T) {
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	missingDir := filepath.Join(t.TempDir(), "no", "such", "dir")
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: filepath.Join(missingDir, "out.log"),
	})
	if !errors.Is(err, ErrOutputSetup) {
		t.Fatalf("want ErrOutputSetup, got err=%v proc=%v", err, proc)
	}
	if proc != nil {
		t.Fatalf("process returned alongside error: %v", proc)
	}
}

// A directory at the log path is a non-regular file like any other and
// must be refused by type, not by a failed open.
func TestOneShotLaunch_RejectsDirectoryOutputPath(t *testing.T) {
	dir := t.TempDir()
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: dir,
	})
	if !errors.Is(err, ErrInvalidOutputPath) {
		t.Fatalf("want ErrInvalidOutputPath, got err=%v proc=%v", err, proc)
	}
	if proc != nil {
		t.Fatalf("process returned alongside error: %v", proc)
	}
}
