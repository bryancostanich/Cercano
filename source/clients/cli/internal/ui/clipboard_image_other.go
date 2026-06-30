//go:build !darwin

package ui

// clipboardImage is unsupported off macOS for now; file drop still works.
func clipboardImage() ([]byte, string, bool) { return nil, "", false }
