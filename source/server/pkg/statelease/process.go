package statelease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultRoot is user-wide, not config-file-specific: the Cercano keychain
// namespace is shared even by processes using different configuration files.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", fmt.Errorf("cannot determine an absolute home directory for setup coordination")
	}
	return filepath.Join(home, ".cercano", "state"), nil
}

// BeginParticipation is for callers that have a defined Close lifecycle.
// Unsupported platforms retain ordinary application behavior, but TryReset
// always returns ErrUnsupported there; it never claims exclusive access.
func BeginParticipation() (*Lease, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	lease, err := Participate(root)
	if errors.Is(err, ErrUnsupported) {
		return nil, nil
	}
	return lease, err
}

var processLeases struct {
	sync.Mutex
	leases map[string]*Lease
}

// HoldProcessLifetime keeps a strong reference until operating-system process
// exit. Do not defer Close in main: that could release exclusivity while other
// goroutines still have a chance to persist settings or refresh credentials.
func HoldProcessLifetime() error {
	root, err := DefaultRoot()
	if err != nil {
		return err
	}
	processLeases.Lock()
	defer processLeases.Unlock()
	if processLeases.leases[root] != nil {
		return nil
	}
	lease, err := Participate(root)
	if errors.Is(err, ErrUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	if processLeases.leases == nil {
		processLeases.leases = map[string]*Lease{}
	}
	processLeases.leases[root] = lease
	return nil
}
