package brewrestart

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

type processSource interface {
	ListCandidates() ([]int, error)
	Inspect(int) (Identity, error)
	HoldsListener(Identity, netip.AddrPort) (bool, error)
}
type kernelProcesses struct{}

func (kernelProcesses) ListCandidates() ([]int, error)    { return ListCandidates() }
func (kernelProcesses) Inspect(pid int) (Identity, error) { return Inspect(pid) }
func (kernelProcesses) HoldsListener(id Identity, endpoint netip.AddrPort) (bool, error) {
	return HoldsListener(id, endpoint)
}

// Discover finds at most one same-user agent from the same formula installation
// owning the exact endpoint. It never dials, launches or signals processes.
// A nil result means no matching owned listener, not that the port is free.
// Ownership is a point-in-time observation and must be checked again before
// shutdown. It is not authentication against hostile same-user processes.
func Discover(newExecutable string, uid uint32, endpoint netip.AddrPort) (*Identity, error) {
	return discover(kernelProcesses{}, newExecutable, uid, endpoint)
}

func discover(source processSource, newExecutable string, uid uint32, endpoint netip.AddrPort) (*Identity, error) {
	if _, err := FormulaRoot(newExecutable); err != nil {
		return nil, err
	}
	if !endpoint.IsValid() || !endpoint.Addr().IsLoopback() || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" {
		return nil, fmt.Errorf("discovery requires an explicit loopback TCP endpoint")
	}
	pids, err := source.ListCandidates()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var owner *Identity
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		identity, err := source.Inspect(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect candidate PID %d: %w", pid, err)
		}
		if identity.PID != pid {
			return nil, fmt.Errorf("candidate PID identity mismatch")
		}
		if CheckOwned(identity, newExecutable, uid) != nil {
			continue
		}
		listens, err := source.HoldsListener(identity, endpoint)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("verify candidate listener: %w", err)
		}
		if !listens {
			continue
		}
		if owner != nil {
			return nil, fmt.Errorf("multiple owned processes listen on the target endpoint; refusing restart")
		}
		copy := identity
		owner = &copy
	}
	return owner, nil
}
