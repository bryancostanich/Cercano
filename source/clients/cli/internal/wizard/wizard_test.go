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

func TestCloudPathVisitsEveryStep(t *testing.T) {
	s := New()
	s.PrimarySide = SideCloud
	steps := []Step{s.Step}
	answers := func() {
		switch s.Step {
		case StepCloud:
			s.CloudProvider, s.AuthMethod = "anthropic", "meridian"
		case StepLocus:
			s.LocusMode = s.RecommendedLocus()
		}
	}
	for !s.Complete() {
		answers()
		if err := s.Advance(); err != nil {
			t.Fatalf("advance from %s: %v", steps[len(steps)-1], err)
		}
		steps = append(steps, s.Step)
	}
	want := []Step{StepPrimary, StepCloud, StepLocus, StepTiers, StepDone}
	if len(steps) != len(want) {
		t.Fatalf("cloud path: want %v, got %v", want, steps)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("cloud path: want %v, got %v", want, steps)
		}
	}
	if s.RecommendedLocus() != "cloud_primary" {
		t.Errorf("cloud primary: want cloud_primary recommendation, got %s", s.RecommendedLocus())
	}
}

func TestOpenPathSkipsCloudStep(t *testing.T) {
	s := New()
	s.PrimarySide = SideOpen
	if err := s.Advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if s.Step != StepLocus {
		t.Fatalf("open primary: want %s after primary, got %s", StepLocus, s.Step)
	}
	if s.Prev() != StepPrimary {
		t.Errorf("open primary: Prev from locus should skip cloud, got %s", s.Prev())
	}
	if s.RecommendedLocus() != "open_primary" {
		t.Errorf("open primary: want open_primary recommendation, got %s", s.RecommendedLocus())
	}
}

func TestAdvanceRejectsMissingAnswers(t *testing.T) {
	s := New()
	if err := s.Advance(); err == nil {
		t.Error("primary without side: want error")
	}
	s.PrimarySide = SideCloud
	if err := s.Advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := s.Advance(); err == nil {
		t.Error("cloud without provider/auth: want error")
	}
	s.CloudProvider = "openai"
	if err := s.Advance(); err == nil {
		t.Error("cloud without auth method: want error")
	}
}

func TestResumeRoundTrip(t *testing.T) {
	useTempState(t)
	if _, ok := Load(); ok {
		t.Fatal("empty dir: want nothing to resume")
	}
	s := New()
	s.PrimarySide = SideCloud
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
	if got.Step != StepCloud || got.PrimarySide != SideCloud {
		t.Errorf("resume: want step=%s side=%s, got step=%s side=%s", StepCloud, SideCloud, got.Step, got.PrimarySide)
	}
}

func TestCompletedRunDoesNotResume(t *testing.T) {
	useTempState(t)
	if err := Save(State{Step: StepDone}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := Load(); ok {
		t.Error("terminal state: want no resume")
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
