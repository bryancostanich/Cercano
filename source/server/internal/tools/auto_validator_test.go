package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/projectconfig"
	"cercano/source/server/internal/testfixtures"
)

type fakeLoader struct {
	cfg projectconfig.Config
	err error
}

func (f fakeLoader) Load(_ string) (projectconfig.Config, error) { return f.cfg, f.err }

type recordingValidator struct {
	called  bool
	workDir string
	ret     Decision
	err     error
}

func (r *recordingValidator) Validate(_ context.Context, dir string) (Decision, error) {
	r.called = true
	r.workDir = dir
	return r.ret, r.err
}

func writeManifest(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAutoValidator_SkipTrueShortCircuits(t *testing.T) {
	rec := &recordingValidator{ret: Passed}
	av := NewAutoValidator(fakeLoader{cfg: projectconfig.Config{
		Validator: projectconfig.ValidatorConfig{Skip: true},
	}}, KindToValidator{KindGo: rec})
	decision, err := av.Validate(context.Background(), t.TempDir())
	if decision != Skipped {
		t.Fatalf("got %s, want Skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %v", err)
	}
	if rec.called {
		t.Error("sub-validator should not be called when skip=true")
	}
}

func TestAutoValidator_CommandOverrideDispatchesCustom(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "go.mod", "module x")
	av := NewAutoValidator(fakeLoader{cfg: projectconfig.Config{
		Validator: projectconfig.ValidatorConfig{Command: "true"},
	}}, KindToValidator{})
	decision, err := av.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got %s, want Passed", decision)
	}
}

func TestAutoValidator_DetectsAndDispatches(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "go.mod", "module x")
	rec := &recordingValidator{ret: Passed}
	av := NewAutoValidator(fakeLoader{}, KindToValidator{KindGo: rec})
	decision, err := av.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got %s, want Passed", decision)
	}
	if !rec.called || rec.workDir != dir {
		t.Errorf("expected sub-validator called with dir=%s, got called=%v dir=%s", dir, rec.called, rec.workDir)
	}
}

func TestAutoValidator_NoManifestReturnsSkipped(t *testing.T) {
	dir := t.TempDir()
	av := NewAutoValidator(fakeLoader{}, KindToValidator{})
	decision, err := av.Validate(context.Background(), dir)
	if decision != Skipped {
		t.Fatalf("got %s, want Skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %v", err)
	}
	if !strings.Contains(sr.Reason, "no recognized project manifest") {
		t.Errorf("reason = %q, want it to mention 'no recognized project manifest'", sr.Reason)
	}
}

func TestAutoValidator_InvalidConfigReturnsFailed(t *testing.T) {
	av := NewAutoValidator(fakeLoader{err: errors.New("invalid .cercano/config.yaml: bad")}, KindToValidator{})
	decision, err := av.Validate(context.Background(), t.TempDir())
	if decision != Failed {
		t.Fatalf("got %s, want Failed", decision)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid .cercano/config.yaml") {
		t.Errorf("err = %v, want it to contain 'invalid .cercano/config.yaml'", err)
	}
}

func TestDefaultKindToValidator_IncludesPython(t *testing.T) {
	m := DefaultKindToValidator()
	v, ok := m[KindPython]
	if !ok || v == nil {
		t.Fatalf("expected KindPython entry in DefaultKindToValidator, got %+v", m)
	}
}

// End-to-end: AutoValidator with the real Default* wiring should detect a
// Python fixture and dispatch to PythonValidator (Pass on valid, Fail on broken).
// PATH-gated like the other Python tests.
func TestAutoValidator_DispatchesToPython_EndToEnd(t *testing.T) {
	skipIfNoPython(t)

	av := NewAutoValidator(fakeLoader{}, DefaultKindToValidator())

	t.Run("valid", func(t *testing.T) {
		dir := copyFixtureForAutoValidator(t, "python/valid")
		decision, err := av.Validate(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if decision != Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})

	t.Run("broken", func(t *testing.T) {
		dir := copyFixtureForAutoValidator(t, "python/broken")
		decision, err := av.Validate(context.Background(), dir)
		if err == nil {
			t.Fatal("expected error")
		}
		if decision != Failed {
			t.Errorf("got decision %s, want failed", decision)
		}
	})
}

// copyFixtureForAutoValidator is a thin alias for testfixtures.Copy.
func copyFixtureForAutoValidator(t *testing.T, name string) string {
	return testfixtures.Copy(t, name)
}
