package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// researchStateFilename is the sidecar written into a durable research run's
// output directory. It mirrors the deep-research pipeline's research_state.json
// convention so the two research tools share one durability idiom.
const researchStateFilename = "research_state.json"

// currentResearchStateVersion is stamped into researchState.Version so a future
// format change can be detected and a stale sidecar ignored rather than
// misread.
const currentResearchStateVersion = 1

// Research phases, in execution order. The sidecar records the last phase that
// completed so a resumed run can skip finished work.
const (
	phaseQueries   = "queries"   // search queries crafted
	phaseSearch    = "search"    // search + dedup done, results captured
	phaseFetch     = "fetch"     // pages fetched
	phaseSynthesis = "synthesis" // answer synthesized
	phaseComplete  = "complete"  // run finished
)

// researchState is the persistent sidecar for the light research pipeline. It
// captures enough of each step's output that a crashed run can resume from the
// last completed phase instead of restarting from an empty query.
type researchState struct {
	Version    int            `json:"version"`
	Question   string         `json:"question"`
	MaxResults int            `json:"max_results"`
	Phase      string         `json:"phase"`
	Queries    []string       `json:"queries,omitempty"`
	Results    []SearchResult `json:"results,omitempty"`
	Pages      []FetchedPage  `json:"pages,omitempty"`
	Answer     string         `json:"answer,omitempty"`
	Sources    []string       `json:"sources,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// researchSidecar manages research_state.json inside an output directory. When
// its dir is empty the sidecar is a no-op, so callers that want the classic
// in-memory-only behavior pay nothing.
type researchSidecar struct {
	dir string
}

// newResearchSidecar returns a sidecar bound to outputDir. An empty dir yields
// a disabled sidecar whose Save/Load/Exists are inert.
func newResearchSidecar(outputDir string) *researchSidecar {
	return &researchSidecar{dir: outputDir}
}

// enabled reports whether this sidecar persists to disk.
func (s *researchSidecar) enabled() bool { return s.dir != "" }

// path returns the full path to the sidecar file.
func (s *researchSidecar) path() string {
	return filepath.Join(s.dir, researchStateFilename)
}

// exists reports whether a sidecar file is present.
func (s *researchSidecar) exists() bool {
	if !s.enabled() {
		return false
	}
	_, err := os.Stat(s.path())
	return err == nil
}

// load reads and unmarshals the sidecar. Callers should guard with exists.
func (s *researchSidecar) load() (*researchState, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		return nil, err
	}
	var st researchState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// save writes state to disk, stamping UpdatedAt and creating the directory if
// needed. It is a no-op when the sidecar is disabled, so pipeline code can call
// it unconditionally after each phase.
func (s *researchSidecar) save(st *researchState) error {
	if !s.enabled() {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	st.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), data, 0o644)
}

// isInProgress reports whether a loaded state represents an interrupted run
// that can be resumed — a non-empty phase that has not reached completion.
func (st *researchState) isInProgress() bool {
	return st.Phase != "" && st.Phase != phaseComplete
}

// phaseReached reports whether the given phase has already completed, using the
// fixed phase ordering. It lets a resumed run skip finished steps.
func (st *researchState) phaseReached(phase string) bool {
	return phaseOrder(st.Phase) >= phaseOrder(phase)
}

// phaseOrder maps a phase name to its ordinal position (0 for unknown/empty).
func phaseOrder(phase string) int {
	switch phase {
	case phaseQueries:
		return 1
	case phaseSearch:
		return 2
	case phaseFetch:
		return 3
	case phaseSynthesis:
		return 4
	case phaseComplete:
		return 5
	default:
		return 0
	}
}
