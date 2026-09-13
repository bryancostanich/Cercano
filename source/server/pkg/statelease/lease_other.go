//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package statelease

// Neither API pretends to acquire a lock. Normal application startup may elect
// to keep its existing behavior when errors.Is(err, ErrUnsupported), but reset
// must unconditionally fail closed on that same platform.
func acquire(string, bool) (*Lease, error) { return nil, ErrUnsupported }
