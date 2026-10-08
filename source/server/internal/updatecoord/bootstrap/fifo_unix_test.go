//go:build unix

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestPrepareRejectsFIFOSource: a FIFO would hang a naive reader; it must
// be refused by type before any open blocks.
func TestPrepareRejectsFIFOSource(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	req := validRequest(t, stateRoot, versions, fifo, digestOfLengthOne(t), 1)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Prepare on FIFO source: error %v, want ErrInvalidSource", err)
	}
	if _, err := os.Lstat(fifo); err != nil {
		t.Fatalf("FIFO source was modified or removed: %v", err)
	}
	assertNoStagingLeft(t, stateRoot)
}
