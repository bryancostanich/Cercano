package dispatch

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
)

// Role is kept as a dispatch-level alias because dispatch.Spec lives in this
// package. Provider selection itself lives in inference.Router.
type Role = inference.Role

// Providers and Selection remain compatibility aliases for dispatch tests and
// Spec-adjacent seams; selection behavior lives in inference.Router.
type Providers = inference.Tiers
type Selection = inference.Selection

const (
	RoleMain   = inference.RoleMain
	RoleCoproc = inference.RoleCoproc
)

// Select delegates to inference.Select. Production callers should prefer
// inference.Select or inference.Router directly; this keeps dispatch package
// tests and Spec-adjacent compatibility code lightweight.
func Select(mode locus.Mode, role Role, p Providers) (Selection, error) {
	return inference.Select(mode, role, p)
}
