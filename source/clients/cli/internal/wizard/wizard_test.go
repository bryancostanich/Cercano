package wizard

import (
	"os"
	"path/filepath"
	"testing"
)

func useTempState(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wizard_state.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", path)
	return path
}

// runToEnd drives the machine to completion, filling the cloud step's answers,
// and returns the visited step sequence.
func runToEnd(t *testing.T, s State) []Step {
	t.Helper()
	steps := []Step{s.Step}
	for !s.Complete() {
		if s.Step == StepCloud {
			s.CloudProvider, s.AuthMethod = "anthropic", "meridian"
		}
		if err := s.Advance(); err != nil {
			t.Fatalf("advance from %s: %v", steps[len(steps)-1], err)
		}
		steps = append(steps, s.Step)
	}
	return steps
}

func eqSteps(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCloudPrimaryVisitsBothMiddleSteps(t *testing.T) {
	s := New()
	s.LocusMode = "cloud_primary" // uses cloud AND open
	got := runToEnd(t, s)
	want := []Step{StepLocus, StepCloud, StepOpen, StepDone}
	if !eqSteps(got, want) {
		t.Fatalf("cloud_primary: want %v, got %v", want, got)
	}
}

func TestCloudOnlySkipsOpenStep(t *testing.T) {
	s := New()
	s.LocusMode = "cloud_only"
	got := runToEnd(t, s)
	want := []Step{StepLocus, StepCloud, StepDone}
	if !eqSteps(got, want) {
		t.Fatalf("cloud_only: want %v (no open step), got %v", want, got)
	}
}

func TestOpenPathsSkipCloudStep(t *testing.T) {
	for _, mode := range []string{"open_only", "open_primary"} {
		s := New()
		s.LocusMode = mode
		got := runToEnd(t, s)
		want := []Step{StepLocus, StepOpen, StepDone}
		if !eqSteps(got, want) {
			t.Fatalf("%s: want %v (no cloud step), got %v", mode, want, got)
		}
	}
}

func TestPrevBranchesOnLocus(t *testing.T) {
	// From the open step, Prev goes back to cloud only when the locus used
	// cloud; otherwise straight to locus.
	openAfterCloud := State{Step: StepOpen, LocusMode: "cloud_primary"}
	if openAfterCloud.Prev() != StepCloud {
		t.Errorf("cloud_primary: open.Prev = %s, want %s", openAfterCloud.Prev(), StepCloud)
	}
	openOnly := State{Step: StepOpen, LocusMode: "open_only"}
	if openOnly.Prev() != StepLocus {
		t.Errorf("open_only: open.Prev = %s, want %s (skips cloud)", openOnly.Prev(), StepLocus)
	}
}

func TestAdvanceRejectsMissingAnswers(t *testing.T) {
	s := New()
	if err := s.Advance(); err == nil {
		t.Error("locus without mode: want error")
	}
	s.LocusMode = "cloud_primary"
	if err := s.Advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if s.Step != StepCloud {
		t.Fatalf("after locus=cloud_primary: want %s, got %s", StepCloud, s.Step)
	}
	if err := s.Advance(); err == nil {
		t.Error("cloud without provider/auth: want error")
	}
	s.CloudProvider = "openai"
	if err := s.Advance(); err == nil {
		t.Error("cloud without auth method: want error")
	}
}

func TestDefaultProvider(t *testing.T) {
	cases := map[string]string{
		"cloud_only":    SideCloud,
		"cloud_primary": SideCloud,
		"open_primary":  SideOpen,
		"open_only":     SideOpen,
	}
	for mode, want := range cases {
		if got := (State{LocusMode: mode}).DefaultProvider(); got != want {
			t.Errorf("%s: DefaultProvider = %q, want %q", mode, got, want)
		}
	}
}

func TestResumeRoundTrip(t *testing.T) {
	useTempState(t)
	if _, ok := Load(); ok {
		t.Fatal("empty dir: want nothing to resume")
	}
	s := New()
	s.LocusMode = "cloud_primary"
	if err := s.Advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := Load()
	if !ok {
		t.Fatal("want resumable state")
	}
	if got.Step != StepCloud || got.LocusMode != "cloud_primary" {
		t.Errorf("resume: want step=%s mode=cloud_primary, got step=%s mode=%s", StepCloud, got.Step, got.LocusMode)
	}
}

func TestDoneStepStillResumes(t *testing.T) {
	// Reaching the summary screen is not completion — only a successful
	// apply clears the file. Quitting there must resume to the summary.
	useTempState(t)
	if err := Save(State{Step: StepDone, LocusMode: "open_only"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := Load()
	if !ok {
		t.Fatal("done-step state: want resume")
	}
	if got.Step != StepDone {
		t.Errorf("resume: want %s, got %s", StepDone, got.Step)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	// The baseline must survive persistence so a resumed (or crashed) run
	// can still be abandoned back to the pre-wizard configuration.
	useTempState(t)
	s := New()
	s.Baseline = &Baseline{
		ActiveProfile: "default",
		Profiles: []ProfileSnapshot{
			{Name: "default", Flavor: "messages", BaseURL: "http://127.0.0.1:3456", Model: "claude-fable-5", Route: "meridian"},
		},
	}
	if err := Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := Load()
	if !ok {
		t.Fatal("want resumable state")
	}
	if got.Baseline == nil {
		t.Fatal("resume: baseline lost")
	}
	if got.Baseline.ActiveProfile != "default" || len(got.Baseline.Profiles) != 1 {
		t.Fatalf("resume: baseline %+v", got.Baseline)
	}
	if p := got.Baseline.Profiles[0]; p.Route != "meridian" || p.BaseURL != "http://127.0.0.1:3456" {
		t.Errorf("resume: profile snapshot %+v", p)
	}
}

func TestClear(t *testing.T) {
	path := useTempState(t)
	if err := Clear(); err != nil {
		t.Fatalf("clear with no file: %v", err)
	}
	if err := Save(New()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("clear: state file still present")
	}
}
