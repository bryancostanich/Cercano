package secrets

import (
	"errors"
	"github.com/99designs/keyring"
	"io/fs"
	"testing"
)

type failingKeyring struct {
	keyring.Keyring
	err error
}

func (k failingKeyring) Get(string) (keyring.Item, error) { return keyring.Item{}, k.err }

func TestMissingCredentialHasPortableSentinel(t *testing.T) {
	for _, store := range []Store{NewMemory(), &keyringStore{kr: failingKeyring{err: keyring.ErrKeyNotFound}}} {
		if _, err := store.Get("missing"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing key is not distinguishable: %v", err)
		}
	}
	denied := errors.New("keychain access denied")
	store := &keyringStore{kr: failingKeyring{err: denied}}
	if _, err := store.Get("exists"); !errors.Is(err, denied) || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("store failure misrepresented as missing login: %v", err)
	}
}
