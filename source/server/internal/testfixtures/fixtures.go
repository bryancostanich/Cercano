// Package testfixtures provides a shared loader for the test/fixtures/projects
// tree. Tests call Open for read-only access or Copy for a writable per-test
// sandbox under test/sandbox/.
package testfixtures

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	fixturesRel = "test/fixtures/projects"
	sandboxRel  = "test/sandbox"
)

var (
	repoRootOnce sync.Once
	repoRoot     string
	repoRootErr  error
)

// Open returns the absolute path to a read-only fixture under test/fixtures/projects.
// The name is the relative path under that directory, e.g. "go/valid".
// If the fixture is missing the test fails with a clear listing of what does exist.
func Open(t testing.TB, name string) string {
	t.Helper()
	root := mustRepoRoot(t)
	path := filepath.Join(root, fixturesRel, filepath.FromSlash(name))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		available := listAvailableFixtures(root)
		t.Fatalf("testfixtures.Open(%q): fixture not found at %s; available: %s",
			name, path, strings.Join(available, ", "))
	}
	return path
}

// Copy copies the named fixture into a fresh per-test sandbox directory under
// test/sandbox/ and returns the copy's path. The copy is removed at test end
// via t.Cleanup, unless KEEP_SANDBOX=1 is set AND the test failed.
func Copy(t testing.TB, name string) string {
	t.Helper()
	src := Open(t, name)
	root := mustRepoRoot(t)
	sandboxBase := filepath.Join(root, sandboxRel)
	if err := os.MkdirAll(sandboxBase, 0755); err != nil {
		t.Fatalf("testfixtures.Copy(%q): mkdir sandbox base %s: %v", name, sandboxBase, err)
	}
	safe := strings.ReplaceAll(name, "/", "-")
	dst, err := os.MkdirTemp(sandboxBase, safe+"-")
	if err != nil {
		t.Fatalf("testfixtures.Copy(%q): mkdir sandbox copy: %v", name, err)
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		t.Fatalf("testfixtures.Copy(%q): copy %s -> %s: %v", name, src, dst, err)
	}
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("KEEP_SANDBOX") == "1" {
			return
		}
		_ = os.RemoveAll(dst)
	})
	return dst
}

func mustRepoRoot(t testing.TB) string {
	t.Helper()
	repoRootOnce.Do(func() {
		repoRoot, repoRootErr = findRepoRoot()
	})
	if repoRootErr != nil {
		t.Fatalf("testfixtures: %v", repoRootErr)
	}
	return repoRoot
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if info, err := os.Stat(filepath.Join(dir, fixturesRel)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", &repoRootNotFoundError{startedAt: wd}
		}
		dir = parent
	}
}

type repoRootNotFoundError struct {
	startedAt string
}

func (e *repoRootNotFoundError) Error() string {
	return "could not locate " + fixturesRel + " by walking up from " + e.startedAt +
		" — are you running tests outside the Cercano repo?"
}

func listAvailableFixtures(root string) []string {
	out := []string{}
	base := filepath.Join(root, fixturesRel)
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == base {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		if strings.Count(rel, string(filepath.Separator)) == 1 {
			out = append(out, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
