package enterprise

import (
	"path/filepath"
	"testing"
)

func TestConnectionLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.lock")
	release, err := LockConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if unexpected, err := LockConnection(path); err == nil {
		unexpected()
		t.Fatal("concurrent connection owner admitted")
	}
	release()
	release()
	next, err := LockConnection(path)
	if err != nil {
		t.Fatal("lock survived release:", err)
	}
	next()
}
