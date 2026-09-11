package builtins

import (
	"cercano/source/server/pkg/config"
	"testing"
)

func TestRoutingContractDispatchDefaultQuality(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  config.Tier
	}{
		{"", config.TierMostCapable},
		{"light", config.TierFastLight},
		{"standard", config.TierEveryday},
		{"deep", config.TierMostCapable},
		{"unrecognized", config.TierFastLight},
	} {
		t.Run("difficulty="+tc.input, func(t *testing.T) {
			got := tierForDispatch(tc.input)
			t.Logf("difficulty=%q quality=%q", tc.input, got)
			if got != tc.want {
				t.Errorf("quality=%q, want %q", got, tc.want)
			}
		})
	}
}
