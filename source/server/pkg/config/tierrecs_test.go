package config

import (
	"strings"
	"testing"
)

func TestLoadTierRecommendationsEmbedded(t *testing.T) {
	r, err := LoadTierRecommendations()
	if err != nil {
		t.Fatalf("LoadTierRecommendations: %v", err)
	}
	if len(r.Cloud) == 0 {
		t.Fatal("no cloud providers in embedded recommendations")
	}
	// Every shipped provider must fill every tier — validate() enforces it,
	// but assert here so a validation regression can't slip a hole through.
	for prov, c := range r.Cloud {
		for _, tier := range []Tier{TierMostCapable, TierEveryday, TierFastLight, TierFastLightText} {
			if len(c[tier]) == 0 {
				t.Errorf("cloud.%s.%s: no candidates", prov, tier)
			}
		}
	}
	if len(r.Open[TierEveryday]) == 0 {
		t.Error("open.everyday: no candidates")
	}
}

func TestTierRecommendationsCandidates(t *testing.T) {
	r, err := LoadTierRecommendations()
	if err != nil {
		t.Fatalf("LoadTierRecommendations: %v", err)
	}
	if got := r.Candidates(ProviderCloud, "anthropic", TierMostCapable); len(got) == 0 {
		t.Error("anthropic most_capable: want candidates, got none")
	}
	if got := r.Candidates(ProviderOpen, "", TierFastLightText); len(got) == 0 {
		t.Error("open fast_light_text: want candidates, got none")
	}
	if got := r.Candidates(ProviderCloud, "no-such-provider", TierEveryday); got != nil {
		t.Errorf("unknown provider: want nil, got %v", got)
	}
}

func TestParseTierRecommendationsRejectsBadData(t *testing.T) {
	cases := []struct {
		name, yaml, wantErr string
	}{
		{
			name:    "wrong version",
			yaml:    "version: 2\ncloud:\n  x:\n    most_capable: [a]\n    everyday: [a]\n    fast_light: [a]\n    fast_light_text: [a]\nopen:\n  most_capable: [a]\n  everyday: [a]\n  fast_light: [a]\n  fast_light_text: [a]\n",
			wantErr: "unsupported version",
		},
		{
			name:    "missing tier",
			yaml:    "version: 1\ncloud:\n  x:\n    most_capable: [a]\nopen:\n  most_capable: [a]\n  everyday: [a]\n  fast_light: [a]\n  fast_light_text: [a]\n",
			wantErr: "no candidates",
		},
		{
			name:    "unknown tier key",
			yaml:    "version: 1\ncloud:\n  x:\n    most_capable: [a]\n    everyday: [a]\n    fast_light: [a]\n    fast_light_text: [a]\n    turbo: [a]\nopen:\n  most_capable: [a]\n  everyday: [a]\n  fast_light: [a]\n  fast_light_text: [a]\n",
			wantErr: "unknown tier",
		},
		{
			name:    "unknown struct field",
			yaml:    "version: 1\nclouds: {}\n",
			wantErr: "not found",
		},
		{
			name:    "empty model id",
			yaml:    "version: 1\ncloud:\n  x:\n    most_capable: [\"\"]\n    everyday: [a]\n    fast_light: [a]\n    fast_light_text: [a]\nopen:\n  most_capable: [a]\n  everyday: [a]\n  fast_light: [a]\n  fast_light_text: [a]\n",
			wantErr: "empty model id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseTierRecommendations([]byte(tc.yaml))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %q", tc.wantErr, err)
			}
		})
	}
}

func TestPickFirst(t *testing.T) {
	cands := []string{"a", "b", "c"}
	if got, ok := PickFirst(cands, nil); !ok || got != "a" {
		t.Errorf("nil available: want a/true, got %s/%v", got, ok)
	}
	avail := func(m string) bool { return m == "b" || m == "c" }
	if got, ok := PickFirst(cands, avail); !ok || got != "b" {
		t.Errorf("filtered: want b/true, got %s/%v", got, ok)
	}
	if _, ok := PickFirst(cands, func(string) bool { return false }); ok {
		t.Error("none available: want ok=false")
	}
	if _, ok := PickFirst(nil, nil); ok {
		t.Error("no candidates: want ok=false")
	}
}
