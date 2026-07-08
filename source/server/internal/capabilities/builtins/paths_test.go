package builtins

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestResolvePath(t *testing.T) {
	cases := []struct{ name, workDir, p, want string }{
		{"relative joins workdir", "/repo", "internal/x.go", "/repo/internal/x.go"},
		{"absolute unchanged", "/repo", "/etc/hosts", "/etc/hosts"},
		{"empty workdir unchanged", "", "internal/x.go", "internal/x.go"},
		{"empty path unchanged", "/repo", "", ""},
		{"dot resolves to workdir", "/repo", ".", "/repo"},
	}
	for _, c := range cases {
		if got := resolvePath(c.workDir, c.p); got != c.want {
			t.Errorf("%s: resolvePath(%q,%q)=%q want %q", c.name, c.workDir, c.p, got, c.want)
		}
	}
}

// TestConcurrentWrites_NoWorkDirCrossTalk is the standing invariant guard:
// two concurrent writeFileCap executes with different WorkDirs and relative
// paths must each land under their own WorkDir. Fails if they share a cwd.
func TestConcurrentWrites_NoWorkDirCrossTalk(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	run := func(dir, name string) error {
		call := &capabilities.Call{
			WorkDir: dir,
			Args:    []byte(`{"path":"` + name + `","content":"x"}`),
			Emit:    func(string) {},
		}
		_, err := writeFileCap{}.Execute(context.Background(), call)
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = run(dirA, "a.txt") }()
	go func() { defer wg.Done(); errs[1] = run(dirB, "b.txt") }()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("writes errored: %v %v", errs[0], errs[1])
	}
	// Each file must be under its OWN workdir — impossible if they shared a cwd.
	if _, err := os.Stat(filepath.Join(dirA, "a.txt")); err != nil {
		t.Errorf("a.txt not under dirA: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "b.txt")); err != nil {
		t.Errorf("b.txt not under dirB: %v", err)
	}
}
