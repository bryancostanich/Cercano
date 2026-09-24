//go:build !darwin || !cgo

package brewrestart

import (
	"fmt"
	"net/netip"
)

func CaptureLaunchState(expected Identity) (LaunchState, error) {
	return LaunchState{}, fmt.Errorf("launch-state capture requires macOS with cgo")
}

func HoldsListener(expected Identity, endpoint netip.AddrPort) (bool, error) {
	return false, fmt.Errorf("listener inspection requires macOS with cgo")
}
