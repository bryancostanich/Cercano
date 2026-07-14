package ui

import (
	"strings"

	"cercano/source/server/pkg/agentclient"
)

type cloudTier int

const (
	tierVerified cloudTier = iota
	tierUntested
	tierComingSoon
	tierCustom
)

// tierFromString maps the agent's tier string (from GetCloudProviders) onto the
// CLI's annotation enum. Unknown values fall back to "untested".
func tierFromString(s string) cloudTier {
	switch s {
	case "verified":
		return tierVerified
	case "coming_soon":
		return tierComingSoon
	default:
		return tierUntested
	}
}

// cloudPreset carries one provider's template fields (flavor/backend/base URL)
// for a rendered row. The provider *catalog* now lives on the agent
// (GetCloudProviders); this struct is just the per-row carrier the detail editor
// reads — e.g. to decide whether to offer ChatGPT sign-in on responses rows.
type cloudPreset struct {
	ID, Label, Flavor, Backend, BaseURL string
	Tier                                cloudTier
}

// presetFromProvider builds the per-row template carrier from an agent-catalog
// provider, translating the agent's string tier into the CLI annotation enum.
// Shared by the settings row builder and the wizard's provider lookup so the
// provider→preset mapping lives in exactly one place.
func presetFromProvider(prov agentclient.CloudProvider) cloudPreset {
	return cloudPreset{
		ID: prov.ID, Label: prov.Label, Flavor: prov.Flavor,
		Backend: prov.Backend, BaseURL: prov.BaseURL, Tier: tierFromString(prov.Tier),
	}
}

// cloudRow is one rendered list entry: a configured provider (its primary
// profile merged in), an extra-profile sub-row, a bare provider template, a
// custom profile, or the trailing "other" row.
type cloudRow struct {
	ID, Label  string
	Tier       cloudTier
	IsProfile  bool
	HasKey     bool
	Active     bool
	Backup     bool // this profile is the configured fallback
	ComingSoon bool
	SubProfile bool // an extra (non-primary) profile listed under its provider
	Preset     *cloudPreset
	Profile    *agentclient.CloudProfileInfo
}

// profileSubIndent prefixes extra-profile sub-row labels so they read as nested
// under the provider row above them.
const profileSubIndent = "  "

// buildCloudRowsFromProviders renders the grouped cloud view (GetCloudProviders)
// as a flat row list: one row per provider — its primary profile merged in, or a
// bare template when it has none — an indented sub-row for each additional
// profile, then any custom (unmatched) profiles, then the trailing "other" row.
//
// This is the deduplication fix: a provider that has a configured profile
// renders as a single row, never as a profile row plus a duplicate template row.
func buildCloudRowsFromProviders(view agentclient.CloudProvidersView) []cloudRow {
	rows := make([]cloudRow, 0, len(view.Providers)+len(view.CustomProfiles)+1)
	for i := range view.Providers {
		prov := view.Providers[i]
		tier := tierFromString(prov.Tier)
		pv := presetFromProvider(prov)
		preset := &pv
		if len(prov.Profiles) == 0 {
			// No configured profile: a bare template row (selecting it creates one).
			rows = append(rows, cloudRow{
				ID: "template:" + prov.ID, Label: prov.Label, Tier: tier,
				ComingSoon: prov.Tier == "coming_soon", Preset: preset,
			})
			continue
		}
		// Merged provider row: the friendly provider label carries the primary
		// profile's status. Selecting it edits the primary. profs shares the
		// view's backing array, so &profs[j] stays valid for the row's lifetime.
		profs := view.Providers[i].Profiles
		rows = append(rows, cloudRow{
			ID: "profile:" + profs[0].Name, Label: prov.Label, Tier: tier,
			IsProfile: true, HasKey: profs[0].HasKey, Active: profs[0].Name == view.Active,
			Backup: profs[0].Name == view.Backup,
			Preset: preset, Profile: &profs[0],
		})
		// Additional profiles: indented sub-rows labeled by profile name, so two
		// accounts for one provider stay distinguishable.
		for j := 1; j < len(profs); j++ {
			rows = append(rows, cloudRow{
				ID: "profile:" + profs[j].Name, Label: profileSubIndent + profs[j].Name, Tier: tierCustom,
				IsProfile: true, HasKey: profs[j].HasKey, Active: profs[j].Name == view.Active,
				Backup:     profs[j].Name == view.Backup,
				SubProfile: true, Preset: preset, Profile: &profs[j],
			})
		}
	}
	// Custom profiles (matched no known provider) render as their own rows.
	for i := range view.CustomProfiles {
		p := view.CustomProfiles[i]
		rows = append(rows, cloudRow{
			ID: "profile:" + p.Name, Label: p.Name, Tier: tierCustom,
			IsProfile: true, HasKey: p.HasKey, Active: p.Name == view.Active,
			Backup:  p.Name == view.Backup,
			Profile: &view.CustomProfiles[i],
		})
	}
	rows = append(rows, cloudRow{ID: "other", Label: "+ other", Tier: tierCustom})
	return rows
}

// rowAnnotation is the right-side status text for a row.
func rowAnnotation(r cloudRow) string {
	if r.ID == "other" {
		return "(custom endpoint)"
	}
	if r.IsProfile {
		var parts []string
		// Show the model at row level so users don't have to expand the detail
		// editor to see what's configured. Only emitted when Profile is non-nil
		// (tests may construct bare cloudRows without one).
		if r.Profile != nil {
			if r.Profile.Model != "" {
				parts = append(parts, r.Profile.Model)
			} else {
				parts = append(parts, "— no model")
			}
		}
		// Active rows get an auth-aware "active (…)" marker so the active
		// provider is unmistakable and its auth path is visible; inactive rows
		// show just the auth hint.
		if r.Active {
			parts = append(parts, activeLabel(r))
		} else {
			parts = append(parts, authHint(r))
		}
		// The configured fallback is marked at row level so the pair (active
		// primary, backup) is visible at a glance.
		if r.Backup {
			parts = append(parts, "backup")
		}
		return strings.Join(parts, "  ")
	}
	switch r.Tier {
	case tierUntested:
		return "(untested)"
	case tierComingSoon:
		return "(coming soon)"
	}
	return ""
}

// activeLabel is the auth-aware marker for the active provider's row. Meridian
// (Claude Max OAuth) and ChatGPT (the responses OAuth path) are called out so
// the user sees not just that a provider is active but how it authenticates.
func activeLabel(r cloudRow) string {
	switch {
	case r.Profile != nil && r.Profile.Route == "subscription":
		return "active (subscription)"
	case r.Profile != nil && r.Profile.Flavor == "responses":
		return "active (ChatGPT OAuth)"
	default:
		return "active"
	}
}

// authHint is the non-active auth indicator. Meridian routes authenticate via
// Claude Max OAuth (no stored key), so "no key" would mislead — show the route
// instead. Otherwise reflect keychain presence.
func authHint(r cloudRow) string {
	switch {
	case r.Profile != nil && r.Profile.Route == "subscription":
		return "subscription"
	case r.HasKey:
		return "✓ key"
	default:
		return "— no key"
	}
}
