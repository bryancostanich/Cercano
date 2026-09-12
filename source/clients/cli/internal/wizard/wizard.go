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
	"cercano/source/server/pkg/setupstate"
	"fmt"
)

// Persisted types live in setupstate; navigation remains in this package.
type Step = setupstate.Step

const (
	StepLocus = setupstate.StepLocus
	StepCloud = setupstate.StepCloud
	StepOpen  = setupstate.StepOpen
	StepDone  = setupstate.StepDone
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

type State setupstate.State
type Baseline = setupstate.Baseline
type ProfileSnapshot = setupstate.ProfileSnapshot

// New returns a fresh run positioned at the first step (locus).
func New() State { return State(setupstate.Fresh()) }

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

func StatePath() string   { return setupstate.StatePath() }
func Load() (State, bool) { s, ok := setupstate.Load(); return State(s), ok }
func Save(s State) error  { return setupstate.Save(setupstate.State(s)) }
func Clear() error        { return setupstate.Clear() }
