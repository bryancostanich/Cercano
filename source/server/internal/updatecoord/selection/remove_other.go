//go:build !darwin && !linux && !windows

package selection

import "os"

func openRemovalCandidate(string) (*os.File, error) { return nil, ErrUnsupportedPlatform }
