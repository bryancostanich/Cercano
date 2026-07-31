//go:build windows

package llamaserver

import "testing"

func TestMergePathValues_DedupesCaseInsensitivelyPreservingOrder(t *testing.T) {
	got := mergePathValues(
		`C:\Windows;C:\Windows\System32`,
		`C:\windows;C:\Users\me\llama.cpp`,
		`C:\Windows\System32;C:\Users\me\llama.cpp`,
	)
	want := `C:\Windows;C:\Windows\System32;C:\Users\me\llama.cpp`
	if got != want {
		t.Errorf("mergePathValues = %q, want %q", got, want)
	}
}

func TestMergePathValues_SkipsEmptySegments(t *testing.T) {
	got := mergePathValues(`C:\Windows;;`, "", `C:\Users\me\llama.cpp;`)
	want := `C:\Windows;C:\Users\me\llama.cpp`
	if got != want {
		t.Errorf("mergePathValues = %q, want %q", got, want)
	}
}

func TestReadRegistryPath_MissingKeyReturnsEmpty(t *testing.T) {
	// A key that doesn't exist should be treated as "nothing to add", not a
	// crash or a propagated error — defaultRefreshPATH has no error return.
	if got := readRegistryPath(0, `Software\Cercano\DoesNotExist`); got != "" {
		t.Errorf("readRegistryPath(missing key) = %q, want empty", got)
	}
}
