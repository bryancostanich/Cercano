package config

import (
	"context"
	"errors"

	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/hostsvc/credentials"
	cfg "cercano/source/server/pkg/config"
)

var ErrLoginProfileChanged = errors.New("cloud profile changed during login; start again")

// CloudLogin couples a credential commit to the profile state the user chose.
// The config lock is held only at begin/commit, never while awaiting login.
type CloudLogin struct {
	owner                             *svc
	attempt                           *credentials.LoginAttempt
	proposed, before                  cfg.CloudProfile
	existed, activate, reauthenticate bool
}
type CloudLoginResult struct {
	Profile                 cfg.CloudProfile
	Active, Reauthenticated bool
}

func (s *svc) BeginCloudLogin(ctx context.Context, profile cfg.CloudProfile, activate, reauthenticate bool) (*CloudLogin, error) {
	if profile.Name == "" || !cloudfactory.IsSubscription(profile) {
		return nil, errors.New("subscription profile required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before, exists := profileByName(s.current.CloudProfiles, profile.Name)
	if reauthenticate && (!exists || before.Flavor != profile.Flavor || before.Route != profile.Route) {
		return nil, errors.New("matching subscription profile required for reauthentication")
	}
	provider := "anthropic"
	if profile.Route == cloudfactory.RouteChatGPT {
		provider = "openai-responses"
	}
	attempt, err := s.credentials.BeginLogin(ctx, profile.Name, provider)
	if err != nil {
		return nil, err
	}
	return &CloudLogin{owner: s, attempt: attempt, proposed: profile, before: before, existed: exists, activate: activate, reauthenticate: reauthenticate}, nil
}
func (l *CloudLogin) Context() context.Context { return l.attempt.Context() }
func (l *CloudLogin) Close()                   { l.attempt.Close() }
func (l *CloudLogin) Commit(encoded string) (CloudLoginResult, error) {
	s := l.owner
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := profileByName(s.current.CloudProfiles, l.proposed.Name)
	if exists != l.existed || (exists && current != l.before) {
		return CloudLoginResult{}, ErrLoginProfileChanged
	}
	if err := l.attempt.Commit(encoded); err != nil {
		return CloudLoginResult{}, err
	}
	if l.reauthenticate {
		return CloudLoginResult{Profile: current, Active: s.current.ActiveCloudProfile == current.Name, Reauthenticated: true}, nil
	}
	replaced := false
	for i, p := range s.current.CloudProfiles {
		if p.Name == l.proposed.Name {
			s.current.CloudProfiles[i] = l.proposed
			replaced = true
			break
		}
	}
	if !replaced {
		s.current.CloudProfiles = append(s.current.CloudProfiles, l.proposed)
	}
	if l.activate {
		s.setActiveProfileLocked(l.proposed.Name)
	}
	return CloudLoginResult{Profile: l.proposed, Active: s.current.ActiveCloudProfile == l.proposed.Name}, nil
}

// Called under the config lock. Profile identity/configuration changes cancel
// interactive attempts; unrelated changes and primary selection do not.
func (s *svc) cancelChangedLogins(next []cfg.CloudProfile) {
	for _, old := range s.current.CloudProfiles {
		p, ok := profileByName(next, old.Name)
		if !ok || p != old {
			s.credentials.CancelLogin(old.Name)
		}
	}
	for _, p := range next {
		if _, ok := profileByName(s.current.CloudProfiles, p.Name); !ok {
			s.credentials.CancelLogin(p.Name)
		}
	}
}
