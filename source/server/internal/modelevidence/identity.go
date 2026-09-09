package modelevidence

import (
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/pkg/config"
)

// IdentityFor builds the evidence key for a model served by a cloud profile.
//
// Every field participates: two profiles pointing at different endpoints, or
// the same endpoint over different auth routes, are different identities even
// when the model id is identical. Credentials are deliberately absent — this
// value is cached and shipped to workers.
func IdentityFor(p config.CloudProfile, model string) modelmetadata.Identity {
	return modelmetadata.Identity{
		Provider: p.Provider,
		BaseURL:  p.BaseURL,
		Route:    p.Route,
		Model:    model,
	}
}
