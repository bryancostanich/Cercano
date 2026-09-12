// Package config implements the config service — the sole owner of the live
// config state (currentConfig / cfgMu / configPath / secrets). All other
// packages read config through the Service interface; none hold a copy of
// cfgMu or currentConfig.
package config

import (
	"context"
	"sync"

	"cercano/source/server/internal/hostsvc/credentials"
	"cercano/source/server/internal/secrets"
	cfg "cercano/source/server/pkg/config"
)

// Service is the interface the front door (Server) depends on for config state.
type Service interface {
	// Reads
	Get() cfg.Config
	Path() string
	Secrets() secrets.Store
	Credentials() *credentials.Service
	BeginCloudLogin(context.Context, cfg.CloudProfile, bool, bool) (*CloudLogin, error)
	ActiveProfile() (cfg.CloudProfile, bool)

	// Full-state writes (replace entire config; no notify — caller persists
	// and broadcasts as needed)
	Set(c cfg.Config) error

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

	SetDestinationProfiles(destination cfg.Destination, preferred, backup string) error
	SetTaskAssignment(task cfg.Task, assignment *cfg.TaskAssignment) error

	// Mutate applies fn to an isolated candidate under the write lock, validates it,
	// and commits only valid state. It does NOT
	// persist to disk and does NOT notify — use Persist() and/or Set()
	// explicitly when those side-effects are needed. Intended for targeted
	// in-place patches (rebuildCloud CloudModel write-back, tests).
	Mutate(fn func(*cfg.Config)) error

	// CloudModel mirror write (rebuildCloud write-back only; no notify, no persist)
	SetCloudModel(model string)

	// Persist current config to disk (no-op if path empty)
	Persist()
}

type svc struct {
	mu          sync.RWMutex
	current     cfg.Config
	path        string
	store       secrets.Store
	credentials *credentials.Service
}

// New returns a Service initialized with the given path, config, and secrets.
func New(path string, c cfg.Config, st secrets.Store) Service {
	return &svc{path: path, current: c.Clone(), store: st, credentials: credentials.New(st)}
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
	defer s.mu.RUnlock()
	if s.store == nil {
		return nil
	}
	return s.credentials
}
func (s *svc) Credentials() *credentials.Service { return s.credentials }

func (s *svc) ActiveProfile() (cfg.CloudProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.current.CloudProfiles {
		if p.Name == s.current.ActiveCloudProfile {
			return p.Clone(), true
		}
	}
	return cfg.CloudProfile{}, false
}

// Set replaces the entire config (deep-cloning the incoming value so the
// caller retaining c cannot later mutate shared state). Does NOT persist and
// does NOT notify — caller handles those.
func (s *svc) Set(c cfg.Config) error {
	if err := c.LlamaServer.Validate(); err != nil {
		return err
	}
	clone := c.Clone()
	s.mu.Lock()
	s.cancelChangedLogins(clone.CloudProfiles)
	s.current = clone
	s.mu.Unlock()
	return nil
}

func (s *svc) SetPath(path string) {
	s.mu.Lock()
	s.path = path
	s.mu.Unlock()
}

func (s *svc) SetSecrets(st secrets.Store) {
	s.mu.Lock()
	if st != s.credentials {
		s.credentials.ReplaceStore(st)
		s.store = st
	}
	s.mu.Unlock()
}

func (s *svc) SetActiveProfile(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setActiveProfileLocked(name)
}
func (s *svc) setActiveProfileLocked(name string) bool {
	if _, ok := profileByName(s.current.CloudProfiles, name); !ok {
		return false
	}
	if s.current.BackupCloudProfile == name {
		return false
	}
	s.current.ActiveCloudProfile = name
	return true
}

func (s *svc) UpsertProfile(p cfg.CloudProfile) (replaced bool, isActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := p.Name
	p = p.Clone()
	for i, existing := range s.current.CloudProfiles {
		if existing.Name == name {
			if !existing.Equal(p) {
				s.credentials.CancelLogin(name)
			}
			s.current.CloudProfiles[i] = p
			return true, name == s.current.ActiveCloudProfile
		}
	}
	s.credentials.CancelLogin(name)
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
	s.credentials.CancelLogin(name)
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
	if s.current.BackupCloudProfile == name {
		s.current.BackupCloudProfile = ""
	}
	if s.current.SecondaryCloudProfile == name {
		s.current.SecondaryCloudProfile = ""
	}
	if s.current.SecondaryBackupCloudProfile == name {
		s.current.SecondaryBackupCloudProfile = ""
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

func (s *svc) Mutate(fn func(*cfg.Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.current.Clone()
	fn(&candidate)
	if err := candidate.LlamaServer.Validate(); err != nil {
		return err
	}
	s.cancelChangedLogins(candidate.CloudProfiles)
	s.current = candidate.Clone()
	return nil
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
			return p.Clone(), true
		}
	}
	return cfg.CloudProfile{}, false
}

func (s *svc) SetDestinationProfiles(destination cfg.Destination, preferred, backup string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.SetDestinationProfiles(destination, preferred, backup)
}
func (s *svc) SetTaskAssignment(task cfg.Task, assignment *cfg.TaskAssignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.SetTaskAssignment(task, assignment)
}
