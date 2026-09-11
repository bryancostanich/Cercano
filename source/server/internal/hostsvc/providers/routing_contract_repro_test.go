package providers

import (
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/pkg/config"
	"testing"
)

func TestRoutingContractMainAndMeterUsePremium(t *testing.T) {
	c := config.Defaults()
	c.LocusMode = "cloud_only"
	c.ActiveCloudProfile = "fixture"
	c.CloudProfiles = []config.CloudProfile{{Name: "fixture", Provider: "fixture"}}
	c.ModelProfiles.Cloud.Providers["fixture"] = config.VendorCostTiers{
		Economy: config.CostTierModel{Model: "economy"}, Standard: config.CostTierModel{Model: "standard"}, Premium: config.CostTierModel{Model: "premium"},
	}
	s := &service{cfgSvc: cfgsvc.New("", c, nil)}
	for name, got := range map[string]string{"main": s.MainModel(true), "meter": s.PrimaryModel()} {
		t.Logf("%s=%q", name, got)
		if got != "premium" {
			t.Errorf("%s=%q, want premium", name, got)
		}
	}
}
