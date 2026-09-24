//go:build !darwin || !cgo

package brewrestart

import "fmt"

// ListCandidates returns an error on platforms that don't support macOS libproc
// process enumeration with PROC_UID_ONLY.
func ListCandidates() ([]int, error) {
	return nil, fmt.Errorf("process enumeration requires macOS with cgo")
}
