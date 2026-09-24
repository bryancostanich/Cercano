//go:build !darwin || !cgo

package brewrestart

import (
	"context"
	"fmt"
	"net/netip"
)

func RestartInstalled(ctx context.Context, executable string, endpoint netip.AddrPort) (bool, error) {
	return false, fmt.Errorf("Homebrew upgrade restart requires macOS with cgo")
}
