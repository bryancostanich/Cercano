// Package config implements the config service — the sole owner of the live
// config state (currentConfig / cfgMu / configPath / secrets). All other
// packages read config through the Service interface; none hold a copy of
// cfgMu or currentConfig.
package config

import (
	"sync"

	"cercano/source/server/internal/secrets"
	cfg "cercano/source/server/pkg/config"
)

// Service is the interface the front door (Server) depends on for config state.
type Service interface {
	// Reads
	Get() cfg.Config
	Path() string
	Secrets() secrets.Store
	ActiveProfile() (cfg.CloudProfile, bool)

	// Full-state writes (replace entire config; no notify — caller persists
	// and broadcasts as needed)
	Set(c cfg.Config)

	// Initialization (no persist, no notify)
	SetPath(path string)
	SetSecrets(st secrets.Store)

	// Locked partial mutations (under internal lock; no persist, no notify —
	// caller persists explicitly)
	SetActiveProfile(name string) bool // false if name not found
	UpsertProfile(p cfg.CloudProfile) (replaced bool, isActive bool)
	RemoveProfile(name string) (existed, wasActive bool)
	SetBackupProfile(name string) bool // false if name!="" and not found
	ProfileInfo(name string) (exists bool, isActive bool)

	// Mutate applies fn to the live config under the write lock. It does NOT
	// persist to disk and does NOT notify — use Persist() and/or Set()
	// explicitly when those side-effects are needed. Intended for targeted
	// in-place patches (rebuildCloud CloudModel write-back, tests).
	Mutate(fn func(*cfg.Config))

	// CloudModel mirror write (rebuildCloud write-back only; no notify, no persist)
	SetCloudModel(model string)

	// Persist current config to disk (no-op if path empty)
	Persist()
}

type svc struct {
	mu      sync.RWMutex
	current cfg.Config
	path    string
	store   secrets.Store
}

// New returns a Service initialized with the given path, config, and secrets.
func New(path string, c cfg.Config, st secrets.Store) Service {
	return &svc{path: path, current: c.Clone(), store: st}
}

// Get returns a deep copy of the current config. The returned snapshot shares
// no backing array with the live config, so callers may iterate its slices
// without holding any lock.
func (s *svc) Get() cfg.Config {
	s.mu.RLock()
	c := s.current.Clone()
	s.mu.RUnlock()
	return c
}

func (s *svc) Path() string {
	s.mu.RLock()
	p := s.path
	s.mu.RUnlock()
	return p
}

func (s *svc) Secrets() secrets.Store {
	s.mu.RLock()
	st := s.store
	s.mu.RUnlock()
	return st
}

func (s *svc) ActiveProfile() (cfg.CloudProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.current.CloudProfiles {
		if p.Name == s.current.ActiveCloudProfile {
			return p, true
		}
	}
	return cfg.CloudProfile{}, false
}

// Set replaces the entire config (deep-cloning the incoming value so the
// caller retaining c cannot later mutate shared state). Does NOT persist and
// does NOT notify — caller handles those.
func (s *svc) Set(c cfg.Config) {
	clone := c.Clone()
	s.mu.Lock()
	s.current = clone
	s.mu.Unlock()
}

func (s *svc) SetPath(path string) {
	s.mu.Lock()
	s.path = path
	s.mu.Unlock()
}

func (s *svc) SetSecrets(st secrets.Store) {
	s.mu.Lock()
	s.store = st
	s.mu.Unlock()
}

func (s *svc) SetActiveProfile(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := profileByName(s.current.CloudProfiles, name); !ok {
		return false
	}
	previous := s.current.ActiveCloudProfile
	if previous != "" && previous != name {
		// Selecting a different primary should keep the previous primary as the
		// automatic failover target. This also handles the common swap where the
		// selected profile was already the backup: primary and backup trade places
		// instead of leaving backup equal to primary.
		s.current.BackupCloudProfile = previous
	} else if s.current.BackupCloudProfile == name {
		// Preserve the invariant that the primary profile is never also backup.
		s.current.BackupCloudProfile = ""
	}
	s.current.ActiveCloudProfile = name
	return true
}

func (s *svc) UpsertProfile(p cfg.CloudProfile) (replaced bool, isActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := p.Name
	for i, existing := range s.current.CloudProfiles {
		if existing.Name == name {
			s.current.CloudProfiles[i] = p
			return true, name == s.current.ActiveCloudProfile
		}
	}
	s.current.CloudProfiles = append(s.current.CloudProfiles, p)
	return false, name == s.current.ActiveCloudProfile
}

func (s *svc) RemoveProfile(name string) (existed, wasActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := profileByName(s.current.CloudProfiles, name)
	if !ok {
		return false, false
	}
	kept := s.current.CloudProfiles[:0]
	for _, p := range s.current.CloudProfiles {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	s.current.CloudProfiles = kept
	wasActive = s.current.ActiveCloudProfile == name
	if wasActive {
		s.current.ActiveCloudProfile = ""
	}
	return true, wasActive
}

func (s *svc) SetBackupProfile(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name != "" {
		if _, ok := profileByName(s.current.CloudProfiles, name); !ok {
			return false
		}
	}
	s.current.BackupCloudProfile = name
	return true
}

func (s *svc) ProfileInfo(name string) (exists bool, isActive bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := profileByName(s.current.CloudProfiles, name)
	return ok, s.current.ActiveCloudProfile == name
}

func (s *svc) Mutate(fn func(*cfg.Config)) {
	s.mu.Lock()
	fn(&s.current)
	s.mu.Unlock()
}

func (s *svc) SetCloudModel(model string) {
	s.mu.Lock()
	s.current.CloudModel = model
	s.mu.Unlock()
}

func (s *svc) Persist() {
	s.mu.RLock()
	c := s.current.Clone()
	p := s.path
	s.mu.RUnlock()
	if p != "" {
		_ = cfg.Save(c, p)
	}
}

func profileByName(profiles []cfg.CloudProfile, name string) (cfg.CloudProfile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return cfg.CloudProfile{}, false
}
