//go:build !darwin && !linux

package enterprise

func LockConnection(string) (func(), error) { return nil, ErrCredentialStore }
