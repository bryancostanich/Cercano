//go:build !windows && !unix

package selection

// The publication primitive is fail-closed on platforms with no approved
// commit/flush primitive. (The privdir guard refuses these platforms
// first anyway; these stubs keep the package honest and compiling.)
func commitReplaceSelection(tmp, dest string) error { return ErrUnsupportedPlatform }
func commitCreateSelection(tmp, dest string) error  { return ErrUnsupportedPlatform }
func syncSelectionDirectory(dir string) error       { return ErrUnsupportedPlatform }
