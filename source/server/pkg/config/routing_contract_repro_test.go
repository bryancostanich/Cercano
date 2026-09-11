package config

import "testing"

// The approved routing contract retires profile-wide pins rather than migrating
// them to quality overrides. This regression is intentionally red on the baseline.
func TestRoutingContractLegacyPinDoesNotOverrideQuality(t *testing.T) {
	models := ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"fixture": {
			Economy:  CostTierModel{Model: "eco"},
			Standard: CostTierModel{Model: "std"},
			Premium:  CostTierModel{Model: "pro"},
		},
	}}}
	for _, pinned := range []bool{false, true} {
		profile := CloudProfile{Provider: "fixture", Model: "legacy", ModelPinned: pinned}
		for _, tc := range []struct {
			tier Tier
			want string
		}{
			{TierFastLight, "eco"}, {TierEveryday, "std"}, {TierMostCapable, "pro"},
		} {
			if got := models.ResolveCloudModelForTier(profile, tc.tier); got != tc.want {
				t.Errorf("pinned=%t tier=%s: got %q, want %q", pinned, tc.tier, got, tc.want)
			}
		}
	}
}
