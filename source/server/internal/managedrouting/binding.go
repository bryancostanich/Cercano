package managedrouting

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// BindRoute reuses credentials only from a configured profile with the same
// physical provider and endpoint. A model policy cannot redirect a saved key to
// another service. It never builds the profile's personal backup/account cycle.
func BindRoute(ctx context.Context, c config.Config, route v1.Route, destination config.Destination, open inference.Provider, build func(config.CloudProfile) (inference.Provider, error)) (inference.Candidate, error) {
	if route.Provider == "ollama" {
		endpoint := strings.TrimRight(c.OllamaURL, "/")
		parsed, _ := url.Parse(endpoint)
		if open == nil || c.OpenRuntime != "ollama" || endpoint != route.Endpoint || modelpolicy.Placement(parsed) != route.Placement {
			return inference.Candidate{}, fmt.Errorf("approved Ollama route %q does not match the configured runtime", route.ID)
		}
		return inference.Candidate{Provider: open, IsCloud: route.Placement == "external"}, nil
	}
	if route.Placement == "local" {
		if open == nil || c.OpenRuntime != route.Provider {
			return inference.Candidate{}, fmt.Errorf("approved local route %q does not match the configured runtime", route.ID)
		}
		// Supervised runtime ports are resolved when the model is prepared. The
		// physical-request gate checks that exact endpoint against the current policy.
		return inference.Candidate{Provider: open}, nil
	}
	preferred, _ := c.DestinationProfiles(destination)
	profiles := append([]config.CloudProfile(nil), c.CloudProfiles...)
	for i, p := range profiles {
		if p.Name == preferred {
			profiles[0], profiles[i] = profiles[i], profiles[0]
			break
		}
	}
	for _, profile := range profiles {
		provider, endpoint, err := cloudfactory.PhysicalEndpoint(ctx, profile)
		if err != nil || provider != route.Provider || endpoint != route.Endpoint {
			continue
		}
		if build == nil {
			return inference.Candidate{}, fmt.Errorf("managed cloud provider builder unavailable")
		}
		profile.Model = route.Model
		raw, err := build(profile)
		if raw != nil {
			raw = profilechain.WithRoute(raw, profile.Name, string(destination))
		}
		return inference.Candidate{Provider: raw, Profile: profile.Name, IsCloud: true}, err
	}
	return inference.Candidate{}, fmt.Errorf("approved route %q needs a configured %s profile for %s", route.ID, route.Provider, route.Endpoint)
}
