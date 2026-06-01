package testfixtures

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_ReturnsExistingPath(t *testing.T) {
	dir := Open(t, "_testdata/loader-smoke")
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("expected a.txt inside %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "b.txt")); err != nil {
		t.Fatalf("expected sub/b.txt inside %s: %v", dir, err)
	}
}

func TestOpen_UnknownFixtureFailsWithList(t *testing.T) {
	tt := &capturingT{T: t}
	Open(tt, "no-such-fixture-anywhere")
	if !tt.failed {
		t.Fatal("expected Open to mark test as failed for unknown fixture")
	}
	if !strings.Contains(tt.fatalMsg, "no-such-fixture-anywhere") {
		t.Errorf("fatal msg = %q, want it to name the missing fixture", tt.fatalMsg)
	}
}

func TestCopy_ProducesDistinctSandboxedCopies(t *testing.T) {
	a := Copy(t, "_testdata/loader-smoke")
	b := Copy(t, "_testdata/loader-smoke")
	if a == b {
		t.Fatalf("expected distinct sandbox paths, got %s twice", a)
	}
	for _, dir := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
			t.Errorf("a.txt missing in copy %s: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub", "b.txt")); err != nil {
			t.Errorf("sub/b.txt missing in copy %s: %v", dir, err)
		}
	}
}

func TestCopy_CleansUpAtTestEnd(t *testing.T) {
	fake := newFakeTB()
	copyPath := Copy(fake, "_testdata/loader-smoke")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("sandbox copy missing immediately after Copy: %v", err)
	}
	fake.runCleanups()
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("expected sandbox %s to be removed after cleanup, stat err = %v", copyPath, err)
	}
}

func TestCopy_KeepSandboxOnFailureWhenEnvSet(t *testing.T) {
	t.Setenv("KEEP_SANDBOX", "1")
	fake := newFakeTB()
	fake.failed = true
	copyPath := Copy(fake, "_testdata/loader-smoke")
	fake.runCleanups()
	if _, err := os.Stat(copyPath); err != nil {
		t.Errorf("expected sandbox %s preserved under KEEP_SANDBOX=1, stat err = %v", copyPath, err)
	}
	_ = os.RemoveAll(copyPath)
}

type capturingT struct {
	*testing.T
	failed   bool
	fatalMsg string
}

func (c *capturingT) Fatal(args ...any) {
	c.failed = true
	if len(args) > 0 {
		if s, ok := args[0].(string); ok {
			c.fatalMsg = s
		}
	}
}

func (c *capturingT) Fatalf(format string, args ...any) {
	c.failed = true
	c.fatalMsg = format
	for _, a := range args {
		c.fatalMsg += " " + asString(a)
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// fakeTB is a minimal testing.TB implementation that does not propagate
// failures to a parent test. Only the subset Copy/Open actually call is real;
// methods not used by the loader are stubbed.
type fakeTB struct {
	testing.TB
	failed   bool
	cleanups []func()
}

func newFakeTB() *fakeTB { return &fakeTB{} }

func (f *fakeTB) Helper()                       {}
func (f *fakeTB) Failed() bool                  { return f.failed }
func (f *fakeTB) Cleanup(fn func())             { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Fatalf(format string, a ...any) { f.failed = true }
func (f *fakeTB) Fatal(a ...any)                 { f.failed = true }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}
