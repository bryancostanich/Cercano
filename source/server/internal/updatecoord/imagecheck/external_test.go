package imagecheck

import (
	"os"
	"testing"
)

// Optional owned cross-build fixture. The path is read only by tests and is
// never executed. Production Check has no environment-dependent behavior.
func TestOwnedCrossCompiledImage(t *testing.T) {
	p := os.Getenv("CERCANO_IMAGECHECK_FIXTURE")
	if p == "" {
		t.Skip("no cross-build fixture provided")
	}
	d, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = Check(d, os.Getenv("CERCANO_IMAGECHECK_OS"), os.Getenv("CERCANO_IMAGECHECK_ARCH")); err != nil {
		t.Fatal(err)
	}
}
