package credentials

import (
	"context"
	"errors"

	"cercano/source/server/internal/llm"
)

var ErrLoginSuperseded = errors.New("login canceled or superseded")

// LoginAttempt is a capability to commit credentials for one profile. Only the
// current attempt can commit, once. It is never serialized or reconstructed
// from client-supplied IDs; later transport correlation cannot confer ownership.
type LoginAttempt struct {
	state             *profileState
	profile, provider string
	ctx               context.Context
	cancel            context.CancelFunc
}

func (s *Service) BeginLogin(ctx context.Context, profile, provider string) (*LoginAttempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := s.profile(profile)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.store == nil {
		return nil, errNoStore
	}
	p.cancelLogin()
	loginCtx, cancel := context.WithCancel(ctx)
	a := &LoginAttempt{state: p, profile: profile, provider: provider, ctx: loginCtx, cancel: cancel}
	p.login = a
	return a, nil
}
func (a *LoginAttempt) Context() context.Context { return a.ctx }
func (a *LoginAttempt) Close() {
	p := a.state
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.login == a {
		p.login = nil
	}
	a.cancel()
}
func (a *LoginAttempt) Commit(encoded string) error {
	p := a.state
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if p.login != a {
		return ErrLoginSuperseded
	}
	if p.store == nil {
		return errNoStore
	}
	if err := p.store.Set(a.profile, encoded); err != nil {
		return &llm.CredentialError{Class: llm.ErrCredential, Provider: a.provider, Profile: a.profile, Method: llm.AuthSubscription, Reason: llm.CredentialStore, Cause: err}
	}
	p.invalidate()
	if changed := p.loginCompleted[a.provider]; changed != nil {
		close(changed)
		p.loginCompleted[a.provider] = make(chan struct{})
	}
	return nil
}
func (p *profileState) cancelLogin() {
	if a := p.login; a != nil {
		p.login = nil
		a.cancel()
	}
}

// CancelLogin invalidates a profile's interactive attempt without deleting its
// credentials or interfering with another profile. Config mutations call it
// before replacing/removing a profile, including remove-and-recreate cycles.
func (s *Service) CancelLogin(profile string) {
	p := s.profile(profile)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelLogin()
}

// WatchLogin observes the next successful interactive commit. Refreshes and
// arbitrary store writes do not count as completion of a human login attempt.
func (s *Service) WatchLogin(profile, provider string) <-chan struct{} {
	p := s.profile(profile)
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := p.loginCompleted[provider]
	if changed == nil {
		changed = make(chan struct{})
		p.loginCompleted[provider] = changed
	}
	return changed
}
