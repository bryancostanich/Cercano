package enterprise

import (
	"errors"
	"os"

	"github.com/99designs/keyring"
)

type keychain struct{ ring keyring.Keyring }

func OpenCredentialStore() (CredentialStore, error) {
	ring, err := keyring.Open(keyring.Config{ServiceName: "cercano-enterprise", AllowedBackends: []keyring.BackendType{keyring.KeychainBackend}, KeychainTrustApplication: true})
	if err != nil {
		return nil, ErrCredentialStore
	}
	return &keychain{ring}, nil
}
func (k *keychain) Get(name string) (string, error) {
	item, e := k.ring.Get(name)
	if errors.Is(e, keyring.ErrKeyNotFound) {
		return "", os.ErrNotExist
	}
	if e != nil {
		return "", ErrCredentialStore
	}
	return string(item.Data), nil
}
func (k *keychain) Set(name, value string) error {
	return k.ring.Set(keyring.Item{Key: name, Data: []byte(value)})
}
func (k *keychain) Delete(name string) error { return k.ring.Remove(name) }
