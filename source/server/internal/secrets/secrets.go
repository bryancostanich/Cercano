// Package secrets stores cloud provider API keys in the OS keychain (macOS
// Keychain, Windows Credential Manager, Linux Secret Service) via 99designs/
// keyring, keyed by profile name under a single service. A headless/Docker
// fallback is deferred; OpenKeychain returns an error there and callers treat
// cloud as absent.
package secrets

import (
	"fmt"
	"sync"

	"github.com/99designs/keyring"
)

const service = "cercano"

// Store reads/writes a profile's API key.
type Store interface {
	Get(profile string) (string, error)
	Set(profile, key string) error
	Delete(profile string) error
	// List returns the names of every profile that has a stored secret. On
	// macOS this lists keychain item attributes WITHOUT reading secret data,
	// so it does not trigger the per-item authorization prompt that Get does.
	// Use it for presence checks (e.g. rendering a "key stored" marker) so
	// merely browsing profiles never prompts for the login-keychain password.
	List() ([]string, error)
}

type keyringStore struct{ kr keyring.Keyring }

// OpenKeychain opens the OS keychain backend. Errors on headless systems with
// no secret service (deferred fallback).
func OpenKeychain() (Store, error) {
	kr, err := keyring.Open(keyring.Config{
		ServiceName:              service,
		KeychainTrustApplication: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open keychain: %w", err)
	}
	return &keyringStore{kr: kr}, nil
}

func (s *keyringStore) Get(profile string) (string, error) {
	item, err := s.kr.Get(profile)
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

func (s *keyringStore) Set(profile, key string) error {
	return s.kr.Set(keyring.Item{Key: profile, Data: []byte(key)})
}

func (s *keyringStore) Delete(profile string) error { return s.kr.Remove(profile) }

// List returns the stored profile names. keyring.Keys lists item attributes
// only (no secret data), so on macOS it does not raise the authorization
// prompt that Get does.
func (s *keyringStore) List() ([]string, error) { return s.kr.Keys() }

// memoryStore is an in-process Store for tests.
type memoryStore struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an in-memory Store suitable for unit tests.
func NewMemory() Store { return &memoryStore{m: map[string]string{}} }

func (s *memoryStore) Get(profile string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[profile]
	if !ok {
		return "", fmt.Errorf("secrets: key %q not found", profile)
	}
	return v, nil
}

func (s *memoryStore) Set(profile, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[profile] = key
	return nil
}

func (s *memoryStore) Delete(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, profile)
	return nil
}

func (s *memoryStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	return out, nil
}
