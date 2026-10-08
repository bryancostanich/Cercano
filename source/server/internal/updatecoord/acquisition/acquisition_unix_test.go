//go:build unix

package acquisition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestAcquireCacheLeafFIFORefused plants a FIFO (named pipe) leaf at the
// exact name the library would use for the cached target. The library's
// os.WriteFile would block forever opening it (no context bound), so the
// package must refuse before the library ever touches it. Validation only
// inspects the leaf with lstat-style readdir metadata, so the refusal
// itself must not block.
func TestAcquireCacheLeafFIFORefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	targetsDir := filepath.Join(cacheDir, cacheTargetsSubdir)
	if err := os.MkdirAll(targetsDir, 0o700); err != nil {
		t.Fatalf("seeding targets cache dir: %v", err)
	}
	leaf := filepath.Join(targetsDir, "cercano%2Fapp%2F1.0.0.zip")
	if err := syscall.Mkfifo(leaf, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a FIFO cache leaf in the library-owned targets directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("refusal %v does not identify the unsafe cache leaf", err)
	}
	// The FIFO must be left exactly as found: refusal never deletes or
	// rewrites the caller's cache.
	fi, lerr := os.Lstat(leaf)
	if lerr != nil {
		t.Fatalf("cache leaf was removed or replaced: %v", lerr)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("cache leaf %q is no longer a FIFO; the cache was rewritten", leaf)
	}
	if fx.targetRequests != 0 {
		t.Fatalf("target bytes were fetched %d time(s) before the cache refusal", fx.targetRequests)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}
