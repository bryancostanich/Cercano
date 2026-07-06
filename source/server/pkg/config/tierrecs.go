package config

import (
	"bytes"
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// tier_recommendations.yaml ships the setup wizard's autofill data: per
// cloud provider (plus one open-weight set), ordered candidate model IDs
// for every tier. Updating a recommendation is a data edit, not a code
// change.
//
//go:embed tier_recommendations.yaml
var tierRecommendationsYAML []byte

// TierCandidates maps a tier to its ordered candidate model IDs, best first.
type TierCandidates map[Tier][]string

// TierRecommendations is the parsed recommendations table. Cloud is keyed
// by the CLI's cloud preset IDs (anthropic, openai, gemini, …); Open is the
// single open-weight set.
type TierRecommendations struct {
	Version int                       `yaml:"version"`
	Cloud   map[string]TierCandidates `yaml:"cloud"`
	Open    TierCandidates            `yaml:"open"`
}

// LoadTierRecommendations parses and validates the embedded table.
func LoadTierRecommendations() (TierRecommendations, error) {
	return parseTierRecommendations(tierRecommendationsYAML)
}

// yamlUnmarshalStrict decodes with unknown-field rejection so a typo'd
// struct key in the data file fails at load instead of being dropped.
func yamlUnmarshalStrict(raw []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(v)
}

func parseTierRecommendations(raw []byte) (TierRecommendations, error) {
	var r TierRecommendations
	if err := yamlUnmarshalStrict(raw, &r); err != nil {
		return TierRecommendations{}, fmt.Errorf("tier recommendations: %w", err)
	}
	if err := r.validate(); err != nil {
		return TierRecommendations{}, fmt.Errorf("tier recommendations: %w", err)
	}
	return r, nil
}

func (r TierRecommendations) validate() error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported version %d (want 1)", r.Version)
	}
	if len(r.Cloud) == 0 {
		return fmt.Errorf("no cloud providers")
	}
	for prov, c := range r.Cloud {
		if err := c.validate(); err != nil {
			return fmt.Errorf("cloud.%s: %w", prov, err)
		}
	}
	if err := r.Open.validate(); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	return nil
}

// validate requires every tier to be present with at least one candidate:
// a provider with a hole would silently leave a wizard slot unfilled.
func (c TierCandidates) validate() error {
	for _, t := range []Tier{TierMostCapable, TierEveryday, TierFastLight, TierFastLightText} {
		models, ok := c[t]
		if !ok || len(models) == 0 {
			return fmt.Errorf("tier %s: no candidates", t)
		}
		for _, m := range models {
			if m == "" {
				return fmt.Errorf("tier %s: empty model id", t)
			}
		}
	}
	for t := range c {
		if (&ModelsConfig{}).tierSlot(t) == nil {
			return fmt.Errorf("unknown tier %q", t)
		}
	}
	return nil
}

// Candidates returns the ordered candidates for one wizard slot. side is
// the taxonomy Provider; cloudID names the cloud preset and is ignored for
// the open side. Unknown providers return nil — the wizard treats that as
// "no recommendation" and leaves the slot for manual pick.
func (r TierRecommendations) Candidates(side Provider, cloudID string, t Tier) []string {
	switch side {
	case ProviderOpen:
		return r.Open[t]
	case ProviderCloud:
		return r.Cloud[cloudID][t]
	}
	return nil
}

// PickFirst returns the first candidate accepted by available, which
// reports whether a model can actually be used (e.g. present in Copilot's
// live catalog). A nil available accepts everything.
func PickFirst(candidates []string, available func(string) bool) (string, bool) {
	for _, m := range candidates {
		if available == nil || available(m) {
			return m, true
		}
	}
	return "", false
}
