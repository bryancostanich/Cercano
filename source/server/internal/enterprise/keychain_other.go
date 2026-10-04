//go:build !darwin

package enterprise

func OpenCredentialStore() (CredentialStore, error) { return nil, ErrCredentialStore }
