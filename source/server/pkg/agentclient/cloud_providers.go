package agentclient

import (
	"context"

	"cercano/source/server/pkg/proto"
)

// CloudProvider is one known-provider catalog entry with the configured
// profiles that belong to it grouped underneath (primary first). It is the
// Go-side view of the proto CloudProvider message.
type CloudProvider struct {
	ID             string
	Label          string
	Flavor         string
	Backend        string
	BaseURL        string
	Tier           string // "verified" | "untested" | "coming_soon"
	Profiles       []CloudProfileInfo
	PrimaryProfile string // primary profile name; "" when none configured
	Route          string // auth path this catalog entry represents ("subscription" for OAuth); "" = API key
}

// CloudProvidersView is the grouped cloud view returned by GetCloudProviders:
// the provider catalog (each with its profiles), any custom (unmatched)
// profiles, and the active profile name. Meridian status is intentionally not
// surfaced here — like GetCloudProfiles, callers source it separately.
type CloudProvidersView struct {
	Providers      []CloudProvider
	CustomProfiles []CloudProfileInfo
	Active         string
	// Backup is the fallback profile name; empty when none is configured.
	Backup string
}

// GetCloudProviders returns the known-provider catalog with configured profiles
// already grouped under their provider (primary first). The agent owns the
// grouping; callers render this view directly.
func (c *Client) GetCloudProviders(ctx context.Context) (CloudProvidersView, error) {
	resp, err := c.agent.GetCloudProviders(ctx, &proto.GetCloudProvidersRequest{})
	if err != nil {
		return CloudProvidersView{}, err
	}
	view := CloudProvidersView{Active: resp.GetActive(), Backup: resp.GetBackup()}
	for _, p := range resp.GetProviders() {
		cp := CloudProvider{
			ID:             p.GetId(),
			Label:          p.GetLabel(),
			Flavor:         p.GetFlavor(),
			Backend:        p.GetBackend(),
			BaseURL:        p.GetBaseUrl(),
			Tier:           p.GetTier(),
			PrimaryProfile: p.GetPrimaryProfile(),
			Route:          p.GetRoute(),
		}
		for _, pi := range p.GetProfiles() {
			cp.Profiles = append(cp.Profiles, cloudProfileInfoFromProto(pi))
		}
		view.Providers = append(view.Providers, cp)
	}
	for _, pi := range resp.GetCustomProfiles() {
		view.CustomProfiles = append(view.CustomProfiles, cloudProfileInfoFromProto(pi))
	}
	return view, nil
}

// cloudProfileInfoFromProto maps a proto CloudProfileInfo to the Go view type.
// Mirrors the inline mapping in GetCloudProfiles, factored out so both the
// profiles and providers wrappers share one conversion.
func cloudProfileInfoFromProto(p *proto.CloudProfileInfo) CloudProfileInfo {
	return CloudProfileInfo{
		Name:    p.GetName(),
		Flavor:  p.GetFlavor(),
		BaseURL: p.GetBaseUrl(),
		Model:   p.GetModel(),
		HasKey:  p.GetHasKey(),
		Backend: p.GetBackend(),
		Route:   p.GetRoute(),
	}
}
