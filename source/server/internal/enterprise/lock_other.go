//go:build !darwin

package enterprise

func LockConnection(string) (func(), error) { return nil, ErrCredentialStore }
