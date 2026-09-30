package chatgptauth

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/llm"
)

// Store is the subset of the secrets store the token source needs. The
// keychain-backed secrets.Store satisfies it structurally, so this package
// stays free of a direct dependency on the secrets package.
type Store interface {
	Get(profile string) (string, error)
	Set(profile, value string) error
}

// Save stores a token set for a profile, JSON-encoded, in the given store.
// The sign-in handler calls it once the device flow completes.
func Save(store Store, profile string, ts TokenSet) error {
	enc, err := ts.Encode()
	if err != nil {
		return err
	}
	return store.Set(profile, enc)
}

// Source hands out a valid access token (and account ID) for a signed-in
// profile, transparently refreshing and persisting the token set when the
// stored access token is at or near expiry. Safe for concurrent use: the
// load/refresh/persist critical section is serialized.
type Source struct {
	store   Store
	profile string
	flow    Flow
	now     func() time.Time

	mu sync.Mutex
}

// NewSource builds a token source for a profile. flow supplies the refresh
// endpoint (its zero value targets the real issuer).
func NewSource(store Store, profile string, flow Flow) *Source {
	return &Source{store: store, profile: profile, flow: flow, now: time.Now}
}

// Token returns a valid bearer access token and the ChatGPT account ID for
// the profile. When the stored token has expired it refreshes via the
// refresh token, persists the new set, and returns the fresh token. Returns
// an error when nothing is stored, or when the token is expired and no
// refresh token is available (the user must sign in again).
func (s *Source) Token(ctx context.Context) (access, accountID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := s.store.Get(s.profile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", s.failure(llm.ErrLoginRequired, llm.CredentialMissing, err)
		}
		return "", "", s.failure(llm.ErrCredential, llm.CredentialStore, err)
	}
	if strings.TrimSpace(raw) == "" {
		return "", "", s.failure(llm.ErrLoginRequired, llm.CredentialMissing, nil)
	}
	ts, err := DecodeTokenSet(raw)
	if err != nil {
		return "", "", s.failure(llm.ErrCredential, llm.CredentialMalformed, err)
	}
	if ts.Access == "" {
		return "", "", s.failure(llm.ErrCredential, llm.CredentialMalformed, nil)
	}
	if !ts.Expired(s.clock()) {
		return ts.Access, ts.AccountID, nil
	}
	if ts.Refresh == "" {
		return "", "", s.failure(llm.ErrLoginRequired, llm.CredentialExpired, nil)
	}

	refreshed, err := s.flow.Refresh(ctx, ts.Refresh)
	if err != nil {
		return "", "", llm.RefreshFailure(err, "openai-responses", s.profile)
	}
	// A refresh response may omit the account ID (no id_token); keep the one
	// we already had so requests keep their ChatGPT-Account-Id header.
	if refreshed.AccountID == "" {
		refreshed.AccountID = ts.AccountID
	}
	if refreshed.Identity.Email == "" {
		refreshed.Identity.Email = ts.Identity.Email
	}
	if refreshed.Identity.Name == "" {
		refreshed.Identity.Name = ts.Identity.Name
	}
	if err := Save(s.store, s.profile, *refreshed); err != nil {
		return "", "", s.failure(llm.ErrCredential, llm.CredentialStore, err)
	}
	return refreshed.Access, refreshed.AccountID, nil
}

func (s *Source) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// CredentialProfile supplies non-secret identity to the provider adapter.
func (s *Source) CredentialProfile() string { return s.profile }
func (s *Source) failure(class llm.ErrorClass, reason string, cause error) error {
	return &llm.CredentialError{Class: class, Provider: "openai-responses", Profile: s.profile, Method: llm.AuthSubscription, Reason: reason, Cause: cause}
}
