// Package wizard implements the setup wizard's state machine: step
// sequencing, collected answers, and resume persistence. Rendering lives
// in the ui package; nothing here knows about Bubble Tea.
// Design: docs/features/setup-wizard/README.md.
//
// The flow is locus-first: the organizing question is how the user wants to
// run Cercano (the locus mode), and that answer decides which of the two
// middle steps run — cloud-profile setup when the locus uses cloud, the
// curated open-model set when it uses open.
package wizard

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Step names one wizard screen. Values are persisted in the state file, so
// they are stable identifiers, not display strings.
type Step string

const (
	StepLocus Step = "locus" // FIRST — how you want to use Cercano (organizing question)
	StepCloud Step = "cloud" // provider + auth; only when the locus uses cloud
	StepOpen  Step = "open"  // curated open-model set; only when the locus uses open
	StepDone  Step = "done"  // terminal: config applied, state cleared
)

// Sides of the taxonomy, mirroring config.Provider values. Used as TierPicks
// key suffixes.
const (
	SideCloud = "cloud"
	SideOpen  = "open"
)

// ModeUsesCloud reports whether a locus mode routes any work to the cloud (so
// the wizard runs the cloud-profile step).
func ModeUsesCloud(mode string) bool {
	return mode == "cloud_only" || mode == "cloud_primary"
}

// ModeUsesOpen reports whether a locus mode runs any work on open models (so
// the wizard runs the open-model-set step).
func ModeUsesOpen(mode string) bool {
	return mode == "open_only" || mode == "open_primary" || mode == "cloud_primary"
}

// State is every answer collected so far plus the current step. It is
// persisted after every transition so quitting mid-wizard resumes in place;
// a completed run clears the file.
type State struct {
	Step          Step              `yaml:"step"`
	LocusMode     string            `yaml:"locus_mode,omitempty"`     // locus.Mode string value (the organizing answer)
	CloudProvider string            `yaml:"cloud_provider,omitempty"` // cloud preset ID
	AuthMethod    string            `yaml:"auth_method,omitempty"`    // meridian | chatgpt | device_code | api_key
	TierPicks     map[string]string `yaml:"tier_picks,omitempty"`     // "<tier>.<side>" → model id
	// Baseline is the cloud-profile configuration captured when the run
	// started, before any eager commits. Abandoning the wizard restores it.
	// Persisted with the run so a resumed (or crashed) run can still be
	// abandoned back to the pre-wizard state. Contains no secrets — API keys
	// live in the OS keychain, never here.
	Baseline *Baseline `yaml:"baseline,omitempty"`
}

// Baseline is the pre-wizard cloud configuration used to undo eager commits
// when the user abandons the run.
type Baseline struct {
	ActiveProfile string            `yaml:"active_profile"`
	Profiles      []ProfileSnapshot `yaml:"profiles,omitempty"`
}

// ProfileSnapshot is one cloud profile's metadata as it stood at wizard
// start. Field set mirrors agentclient.CloudProfileInfo minus HasKey (keys
// are not snapshottable, by design).
type ProfileSnapshot struct {
	Name    string `yaml:"name"`
	Flavor  string `yaml:"flavor"`
	Backend string `yaml:"backend,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`
	Model   string `yaml:"model,omitempty"`
	Route   string `yaml:"route,omitempty"`
}

// New returns a fresh run positioned at the first step (locus).
func New() State { return State{Step: StepLocus} }

// next returns the step after s. The two middle steps are conditional on the
// locus mode: cloud-profile setup only when the locus uses cloud, the
// open-model set only when it uses open.
func (s State) next() Step {
	switch s.Step {
	case StepLocus:
		if ModeUsesCloud(s.LocusMode) {
			return StepCloud
		}
		if ModeUsesOpen(s.LocusMode) {
			return StepOpen
		}
		return StepDone
	case StepCloud:
		if ModeUsesOpen(s.LocusMode) {
			return StepOpen
		}
		return StepDone
	case StepOpen:
		return StepDone
	}
	return StepDone
}

// Prev returns the step before s (for back-navigation), branching the same
// way next does. The first step returns itself.
func (s State) Prev() Step {
	switch s.Step {
	case StepCloud:
		return StepLocus
	case StepOpen:
		if ModeUsesCloud(s.LocusMode) {
			return StepCloud
		}
		return StepLocus
	case StepDone:
		if ModeUsesOpen(s.LocusMode) {
			return StepOpen
		}
		if ModeUsesCloud(s.LocusMode) {
			return StepCloud
		}
		return StepLocus
	}
	return StepLocus
}

// Advance validates that the current step's answer is present, then moves
// to the next step. The caller persists via Save.
func (s *State) Advance() error {
	switch s.Step {
	case StepLocus:
		if s.LocusMode == "" {
			return fmt.Errorf("locus step: no mode selected")
		}
	case StepCloud:
		if s.CloudProvider == "" {
			return fmt.Errorf("cloud step: no provider selected")
		}
		if s.AuthMethod == "" {
			return fmt.Errorf("cloud step: no auth method selected")
		}
	case StepOpen:
		// The open-model set is pre-filled from the curated catalog and
		// editable; sparse picks are legitimate, so nothing to validate here.
	case StepDone:
		return fmt.Errorf("wizard already complete")
	}
	s.Step = s.next()
	return nil
}

// Complete reports whether the run reached the terminal step.
func (s State) Complete() bool { return s.Step == StepDone }

// DefaultProvider returns the taxonomy side the locus mode makes primary —
// what the finish step writes as models.default_provider.
func (s State) DefaultProvider() string {
	if ModeUsesCloud(s.LocusMode) && !ModeUsesOpen(s.LocusMode) {
		return SideCloud
	}
	if s.LocusMode == "cloud_primary" {
		return SideCloud
	}
	return SideOpen
}

// StatePath resolves the resume file. CERCANO_WIZARD_STATE overrides for
// tests, mirroring the uiconfig env-override convention.
func StatePath() string {
	if p := os.Getenv("CERCANO_WIZARD_STATE"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cercano", "wizard_state.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cercano", "wizard_state.yaml")
}

// Load reads a persisted in-progress run. ok is false when there is
// nothing to resume (no file or unreadable). A file at StepDone still
// resumes — the run is only complete once the answers were applied, and
// Clear removes the file at that point; quitting on the summary screen
// must come back to it.
func Load() (s State, ok bool) {
	data, err := os.ReadFile(StatePath())
	if err != nil {
		return State{}, false
	}
	if yaml.Unmarshal(data, &s) != nil || s.Step == "" {
		return State{}, false
	}
	return s, true
}

// Save persists the run for resume. Called after every transition.
func Save(s State) error {
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Clear removes the resume file; a missing file is not an error.
func Clear() error {
	err := os.Remove(StatePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
