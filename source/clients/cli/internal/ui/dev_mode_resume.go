package ui

import "path/filepath"

// Rehydrate mode without sending another kickoff prompt or enabling modes from
// transcript content. Empty metadata clears the previous conversation's mode.
func (m *Model) restoreDevMode(repo string) {
	if repo != "" && !filepath.IsAbs(repo) {
		repo = ""
	}
	if repo != "" {
		repo = filepath.Clean(repo)
	}
	m.workDirOverride = repo
	if m.wdRef != nil {
		m.wdRef.dir = repo
	}
}
