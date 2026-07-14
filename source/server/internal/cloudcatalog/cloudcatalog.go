// Package cloudcatalog owns the agent's knowledge of known cloud providers and
// the grouping of configured profiles under them. It is the single source of
// truth that used to live in the CLI (cloudPresets + buildCloudRows); moving it
// here keeps provider knowledge on the agent so the CLI can be a thin renderer.
//
// Everything in this package is pure and deterministic — no I/O, no config
// dependency — so it is trivially unit-testable and safe to call under a lock.
package cloudcatalog

import "strings"

// Tier classifies how much we trust a provider today. It drives the right-side
// annotation the CLI shows ("(untested)", "(coming soon)").
type Tier string

const (
	TierVerified   Tier = "verified"
	TierUntested   Tier = "untested"
	TierComingSoon Tier = "coming_soon"
)

// Provider is one known cloud provider template: the pre-filled flavor/backend/
// base URL a fresh profile for that provider starts from.
type Provider struct {
	ID      string // stable id ("anthropic", "openai-responses", …) — also the row key
	Label   string // friendly display label ("openai (responses)")
	Flavor  string // messages | chat_completions | responses | bedrock
	Backend string // chat_completions only: per-backend quirks selector; empty otherwise
	BaseURL string // best-effort default endpoint; user-editable
	Tier    Tier
}

// Catalog returns the known providers in display order. Base URLs are
// best-effort defaults; the user can edit them in the detail editor.
//
// This list is the former CLI cloudPresets(), verbatim in content — the agent
// now owns it.
func Catalog() []Provider {
	return []Provider{
		{ID: "anthropic", Label: "anthropic", Flavor: "messages", BaseURL: "", Tier: TierVerified},
		{ID: "openai-responses", Label: "openai (responses)", Flavor: "responses", BaseURL: "https://api.openai.com/v1", Tier: TierUntested},
		{ID: "openai", Label: "openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Tier: TierUntested},
		{ID: "gemini", Label: "gemini", Flavor: "chat_completions", Backend: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Tier: TierVerified},
		{ID: "groq", Label: "groq", Flavor: "chat_completions", Backend: "groq", BaseURL: "https://api.groq.com/openai/v1", Tier: TierUntested},
		{ID: "deepinfra", Label: "deepinfra", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepinfra.com/v1/openai", Tier: TierUntested},
		{ID: "together", Label: "together", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.together.xyz/v1", Tier: TierUntested},
		{ID: "openrouter", Label: "openrouter", Flavor: "chat_completions", Backend: "", BaseURL: "https://openrouter.ai/api/v1", Tier: TierUntested},
		{ID: "deepseek", Label: "deepseek", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepseek.com", Tier: TierUntested},
		{ID: "bedrock", Label: "bedrock", Flavor: "bedrock", BaseURL: "", Tier: TierComingSoon},
	}
}

// ProfileRef is the minimal shape of a configured profile that grouping needs.
// The server maps config.CloudProfile onto this so the package stays free of a
// config dependency (and thus free of import cycles).
type ProfileRef struct {
	Name    string
	Flavor  string
	Backend string
	BaseURL string
	Route   string
}

// GroupedProvider is a catalog provider with the configured profiles that
// belong to it attached. Profiles is primary-first; Primary is the primary
// profile name (empty when the provider has no configured profiles).
type GroupedProvider struct {
	Provider
	Profiles []ProfileRef // configured profiles for this provider, primary first
	Primary  string       // primary profile name; "" when none configured
}

// Group buckets the configured profiles under the known providers and returns
// them in catalog order, each carrying its profiles (primary first). Profiles
// that match no known provider are returned separately as custom.
//
// Primary selection: the active profile if it belongs to the provider,
// otherwise the profile whose name matches the provider ID, otherwise the first
// profile in the given order. This needs nothing persisted — it is derived
// purely from the profile list and the active name.
func Group(profiles []ProfileRef, active string) (providers []GroupedProvider, custom []ProfileRef) {
	cat := Catalog()

	// Bucket profiles by derived provider id, preserving input order within a bucket.
	byID := make(map[string][]ProfileRef, len(cat))
	for _, p := range profiles {
		id := ProviderIDFor(p)
		if id == "" {
			custom = append(custom, p)
			continue
		}
		byID[id] = append(byID[id], p)
	}

	providers = make([]GroupedProvider, 0, len(cat))
	for _, prov := range cat {
		gp := GroupedProvider{Provider: prov, Profiles: orderPrimaryFirst(byID[prov.ID], active, prov.ID)}
		if len(gp.Profiles) > 0 {
			gp.Primary = gp.Profiles[0].Name
		}
		providers = append(providers, gp)
	}
	return providers, custom
}

// orderPrimaryFirst returns bucket with the primary profile moved to the front.
// The primary is the active profile if it is in the bucket, otherwise the
// same-named catalog profile if present (for example provider "anthropic" uses
// profile "anthropic" before older/default aliases), otherwise the bucket stays
// in its existing input order. A fresh slice is returned; the input is not
// mutated.
func orderPrimaryFirst(bucket []ProfileRef, active, providerID string) []ProfileRef {
	if len(bucket) == 0 {
		return nil
	}
	out := make([]ProfileRef, 0, len(bucket))
	primaryIdx := -1
	for i, p := range bucket {
		if p.Name == active {
			primaryIdx = i
			break
		}
	}
	if primaryIdx == -1 && providerID != "" {
		for i, p := range bucket {
			if p.Name == providerID {
				primaryIdx = i
				break
			}
		}
	}
	if primaryIdx > 0 {
		out = append(out, bucket[primaryIdx])
		out = append(out, bucket[:primaryIdx]...)
		out = append(out, bucket[primaryIdx+1:]...)
		return out
	}
	// Preferred profile not in bucket, or already first: keep input order.
	out = append(out, bucket...)
	return out
}

// ProviderIDFor derives which catalog provider a profile belongs to, from its
// shape — profiles carry no "which preset" field, so the mapping is derived:
//
//   - flavor=messages          → anthropic (direct API key or Meridian/Claude Max)
//   - flavor=responses         → openai-responses (ChatGPT subscription)
//   - flavor=bedrock           → bedrock
//   - flavor=chat_completions  → by backend (openai|gemini|groq), else by base-URL host
//
// Returns "" when nothing in the catalog matches (a genuinely custom endpoint).
func ProviderIDFor(p ProfileRef) string {
	switch p.Flavor {
	case "messages":
		return "anthropic"
	case "responses":
		return "openai-responses"
	case "bedrock":
		return "bedrock"
	case "chat_completions":
		switch p.Backend {
		case "openai", "gemini", "groq":
			return p.Backend
		}
		// Backend-less OpenAI-compatible endpoints disambiguate by base-URL host.
		return providerIDByHost(p.BaseURL)
	}
	return ""
}

// providerIDByHost maps a base URL to a backend-less catalog provider by a
// substring of its host. Returns "" when no known host matches.
func providerIDByHost(baseURL string) string {
	host := strings.ToLower(baseURL)
	switch {
	case strings.Contains(host, "deepinfra."):
		return "deepinfra"
	case strings.Contains(host, "together."):
		return "together"
	case strings.Contains(host, "openrouter."):
		return "openrouter"
	case strings.Contains(host, "deepseek."):
		return "deepseek"
	}
	return ""
}
