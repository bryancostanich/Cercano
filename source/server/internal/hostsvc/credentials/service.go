// Package credentials owns credential refresh and replacement for the host.
// Provider adapters and workers receive lightweight views of the same service;
// keychain persistence remains in secrets.Store. No network operation holds a
// profile's mutation lock, and stale refresh results cannot overwrite a login.
package credentials

import (
	"context"
	"errors"
	"sync"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

const refreshTimeout = 30 * time.Second

// Service is shared by config, login handlers, host providers and workers. Its
// Store facade routes all host writes through the same generation boundary.
// Do not retain or mutate its raw backing store after handing it to this owner.
type Service struct {
	mu       sync.Mutex
	store    secrets.Store
	profiles map[string]*profileState
}
type profileState struct {
	mu         sync.Mutex
	store      secrets.Store
	generation uint64
	flight     *flight
	login      *LoginAttempt
}
type token struct{ access, account string }
type flight struct {
	provider   string
	generation uint64
	waiters    int
	done       chan struct{}
	exited     chan struct{}
	cancel     context.CancelFunc
	superseded bool
	abandoned  bool
	value      token
	err        error
}

func New(store secrets.Store) *Service {
	return &Service{store: store, profiles: make(map[string]*profileState)}
}
func (s *Service) profile(name string) *profileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.profiles[name]
	if p == nil {
		p = &profileState{store: s.store}
		s.profiles[name] = p
	}
	return p
}

// ReplaceStore keeps the service identity stable during host reconfiguration.
// Old adapters remain views of this service rather than of a retired backend.
func (s *Service) ReplaceStore(store secrets.Store) {
	if store == s {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
	for _, p := range s.profiles {
		p.mu.Lock()
		p.store = store
		p.invalidate()
		p.mu.Unlock()
	}
}

// invalidate is called with the profile mutation lock held. Wake waiters so
// they can resolve the new generation immediately, even if an old transport is
// slow to honor cancellation. That transport can no longer commit its result.
func (p *profileState) invalidate() {
	p.cancelLogin()
	p.generation++
	if f := p.flight; f != nil {
		p.flight = nil
		f.superseded = true
		f.cancel()
		close(f.done)
	}
}

var errNoStore = errors.New("credential store unavailable")

func (s *Service) Get(name string) (string, error) {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return "", errNoStore
	}
	return p.store.Get(name)
}
func (s *Service) Set(name, value string) error {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return errNoStore
	}
	if err := p.store.Set(name, value); err != nil {
		return err
	}
	p.invalidate()
	return nil
}
func (s *Service) Delete(name string) error {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return errNoStore
	}
	if err := p.store.Delete(name); err != nil {
		return err
	}
	p.invalidate()
	return nil
}
func (s *Service) List() ([]string, error) {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store == nil {
		return nil, errNoStore
	}
	return store.List()
}

// transaction stages the legacy source's write until the service verifies its
// generation. Sources retain provider-specific parsing/refresh logic but never
// receive the raw backing store or independently commit refreshed credentials.
type transaction struct {
	profile, raw string
	loadErr      error
	staged       *string
}

func (t *transaction) Get(profile string) (string, error) {
	if profile != t.profile {
		return "", errors.New("credential transaction profile mismatch")
	}
	return t.raw, t.loadErr
}
func (t *transaction) Set(profile, value string) error {
	if profile != t.profile {
		return errors.New("credential transaction profile mismatch")
	}
	t.staged = &value
	return nil
}

type resolve func(context.Context, *transaction) (token, error)

func (s *Service) acquire(ctx context.Context, profile, provider string, resolve resolve) (token, error) {
	p := s.profile(profile)
	// Repeated replacement is bounded; no automatic retry of rejected grants.
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return token{}, err
		}
		p.mu.Lock()
		f := p.flight
		if f != nil && f.provider != provider {
			p.mu.Unlock()
			return token{}, &llm.CredentialError{Class: llm.ErrCredential, Provider: provider, Profile: profile, Method: llm.AuthSubscription, Reason: "provider_mismatch"}
		}
		if f == nil {
			snapshot := &transaction{profile: profile}
			if p.store == nil {
				snapshot.loadErr = errNoStore
			} else {
				snapshot.raw, snapshot.loadErr = p.store.Get(profile)
			}
			// A waiter owns only its cancellation, not the shared refresh. The last
			// waiter cancels the bounded refresh context; other waiters stay live.
			workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
			f = &flight{provider: provider, generation: p.generation, done: make(chan struct{}), exited: make(chan struct{}), cancel: cancel}
			p.flight = f
			go s.run(workCtx, p, f, snapshot, resolve)
		}
		f.waiters++
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			p.mu.Lock()
			f.waiters--
			if p.flight == f && f.waiters == 0 {
				f.abandoned = true
				f.cancel()
			}
			p.mu.Unlock()
			return token{}, ctx.Err()
		case <-f.done:
			if err := ctx.Err(); err != nil {
				return token{}, err
			}
			if f.superseded || f.abandoned {
				continue
			}
			return f.value, f.err
		}
	}
	return token{}, &llm.CredentialError{Class: llm.ErrCredential, Provider: provider, Profile: profile, Method: llm.AuthSubscription, Reason: "replaced_repeatedly"}
}

func (s *Service) run(ctx context.Context, p *profileState, f *flight, snapshot *transaction, resolve resolve) {
	defer f.cancel()
	defer close(f.exited)
	value, err := resolve(ctx, snapshot)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flight != f || p.generation != f.generation {
		return
	}
	if err == nil && snapshot.staged != nil {
		if writeErr := p.store.Set(snapshot.profile, *snapshot.staged); writeErr != nil {
			err = &llm.CredentialError{Class: llm.ErrCredential, Provider: f.provider, Profile: snapshot.profile, Method: llm.AuthSubscription, Reason: llm.CredentialStore, Cause: writeErr}
		} else {
			p.generation++
		}
	}
	if err != nil {
		value = token{}
	}
	f.value, f.err = value, err
	p.flight = nil
	close(f.done)
}

// AnthropicSource and ChatGPTSource are uncached views: rebuilding a provider
// cannot create a second credential owner or retain obsolete credentials.
type AnthropicSource struct {
	service *Service
	profile string
	flow    anthropicauth.Flow
}

func (s *Service) Anthropic(profile string, flow anthropicauth.Flow) *AnthropicSource {
	return &AnthropicSource{s, profile, flow}
}
func (s *AnthropicSource) CredentialProfile() string { return s.profile }
func (s *AnthropicSource) Token(ctx context.Context) (string, error) {
	value, err := s.service.acquire(ctx, s.profile, "anthropic", func(ctx context.Context, t *transaction) (token, error) {
		access, err := anthropicauth.NewSource(t, s.profile, s.flow).Token(ctx)
		return token{access: access}, err
	})
	return value.access, err
}

type ChatGPTSource struct {
	service *Service
	profile string
	flow    chatgptauth.Flow
}

func (s *Service) ChatGPT(profile string, flow chatgptauth.Flow) *ChatGPTSource {
	return &ChatGPTSource{s, profile, flow}
}
func (s *ChatGPTSource) CredentialProfile() string { return s.profile }
func (s *ChatGPTSource) Token(ctx context.Context) (string, string, error) {
	value, err := s.service.acquire(ctx, s.profile, "openai-responses", func(ctx context.Context, t *transaction) (token, error) {
		access, account, err := chatgptauth.NewSource(t, s.profile, s.flow).Token(ctx)
		return token{access, account}, err
	})
	return value.access, value.account, err
}

var _ secrets.Store = (*Service)(nil)
