package enterprise

import (
	"cercano/source/server/internal/modelpolicy"
	"context"
)

// Authorize consults the current lease and restrictions for every physical
// attempt. Turn-pinned defaults never pin permission. Keeping this Manager
// installed after logout ensures managed execution remains blocked.
func (m *Manager) Authorize(ctx context.Context, attempt modelpolicy.Attempt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := m.Current()
	if err != nil {
		return modelpolicy.Deny(attempt, "no valid enterprise authorization")
	}
	if !modelpolicy.Allowed(p, attempt) {
		return modelpolicy.Deny(attempt, "route is not in the current administrator allow-list")
	}
	return nil
}
