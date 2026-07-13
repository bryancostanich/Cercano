package anthropicauth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Store is the subset of the secrets store the token source needs. The
// keychain-backed secrets.Store satisfies it structurally, so this package
// stays free of a direct dependency on the secrets package.
type Store interface {
	Get(profile string) (string, error)
	Set(profile, value string) error
}

// Save stores a token set for a profile, JSON-encoded, in the given store.
// The sign-in handler calls it once the PKCE flow completes.
func Save(store Store, profile string, ts TokenSet) error {
	enc, err := ts.Encode()
	if err != nil {
		return err
	}
	return store.Set(profile, enc)
}

// Source hands out a valid access token for a signed-in profile,
// transparently refreshing and persisting the token set when the stored
// access token is at or near expiry.
//
// Safe for concurrent use, and single-flight by construction: the whole
// load/refresh/persist critical section is serialized under mu, and the
// refreshed token is persisted to the store before the lock is released. So
// when N requests hit an expired token at once, the first refreshes and
// writes it back; the rest re-load the now-fresh token and return it — one
// refresh, not N. This matters because Anthropic's refresh tokens rotate
// (single-use): concurrent refreshes would invalidate each other.
type Source struct {
	store   Store
	profile string
	flow    Flow
	now     func() time.Time

	mu sync.Mutex
}

// NewSource builds a token source for a profile. flow supplies the refresh
// endpoint (its zero value targets the real endpoints).
func NewSource(store Store, profile string, flow Flow) *Source {
	return &Source{store: store, profile: profile, flow: flow, now: time.Now}
}

// Token returns a valid bearer access token for the profile. When the stored
// token has expired it refreshes via the refresh token, persists the new
// set, and returns the fresh token. Returns an error when nothing is stored,
// or when the token is expired and no refresh token is available (the user
// must sign in again).
func (s *Source) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := s.store.Get(s.profile)
	if err != nil {
		return "", fmt.Errorf("anthropic token source: load %q: %w", s.profile, err)
	}
	ts, err := DecodeTokenSet(raw)
	if err != nil {
		return "", err
	}
	if !ts.Expired(s.clock()) {
		return ts.Access, nil
	}
	if ts.Refresh == "" {
		return "", fmt.Errorf("anthropic token source: token expired and no refresh token; sign in again")
	}

	refreshed, err := s.flow.Refresh(ctx, ts.Refresh)
	if err != nil {
		return "", fmt.Errorf("anthropic token source: refresh: %w", err)
	}
	if err := Save(s.store, s.profile, *refreshed); err != nil {
		return "", fmt.Errorf("anthropic token source: persist refreshed token: %w", err)
	}
	return refreshed.Access, nil
}

func (s *Source) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
