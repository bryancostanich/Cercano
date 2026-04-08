package research

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const sidecarFilename = "research_state.json"

// Sidecar manages the persistent research_state.json in an output directory.
type Sidecar struct {
	dir string
}

// NewSidecar creates a Sidecar for the given output directory.
func NewSidecar(outputDir string) *Sidecar {
	return &Sidecar{dir: outputDir}
}

// Path returns the full path to research_state.json.
func (s *Sidecar) Path() string {
	return filepath.Join(s.dir, sidecarFilename)
}

// Exists reports whether research_state.json exists in the output directory.
func (s *Sidecar) Exists() bool {
	_, err := os.Stat(s.Path())
	return err == nil
}

// Load reads and unmarshals the sidecar from disk.
func (s *Sidecar) Load() (*ResearchState, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return nil, err
	}
	var state ResearchState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// Save marshals state to JSON and writes it to disk, updating UpdatedAt first.
// It creates the output directory if it does not exist.
func (s *Sidecar) Save(state *ResearchState) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path(), data, 0644)
}

// IsInProgress reports whether the research run is still in progress.
// A state is in-progress when its Phase is non-empty and not "complete".
func (rs *ResearchState) IsInProgress() bool {
	return rs.Progress.Phase != "" && rs.Progress.Phase != "complete"
}

// NewState constructs a fresh ResearchState ready to begin the plan phase.
func NewState(topic, intent, depth, dateRange string) *ResearchState {
	now := time.Now()
	return &ResearchState{
		Version:   CurrentStateVersion,
		Depth:     depth,
		Topic:     topic,
		Intent:    intent,
		DateRange: dateRange,
		Progress: ProgressState{
			Phase:        "plan",
			RunStartedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
