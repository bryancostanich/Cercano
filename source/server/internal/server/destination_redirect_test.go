package server

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
	"testing"
)

func TestDestinationRedirectProviderGraph(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.LocusMode = "cloud_only"
	c.ActiveCloudProfile = "p"
	c.SecondaryCloudProfile = "s"
	c.CloudProfiles = []config.CloudProfile{
		{Name: "p", Flavor: "messages", TierOverrides: map[config.CostTier]string{config.CostStandard: "primary-standard"}},
		{Name: "s", Flavor: "messages", TierOverrides: map[config.CostTier]string{config.CostStandard: "secondary-standard"}},
	}
	c.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationLocal, Quality: config.CostStandard}}
	c.LocalRedirect = config.DestinationSecondary
	s.cfgSvc.Set(c)
	s.cfgSvc.Secrets().Set("p", "fixture-p")
	s.cfgSvc.Secrets().Set("s", "fixture-s")
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	check := func(wantProfile, wantModel string) {
		t.Helper()
		candidates := s.providerSvc.Candidates()
		a := candidates.TaskFor(config.TaskChat)
		sel, err := inference.SelectDestination(locus.CloudOnly, a.Destination, candidates)
		if err != nil {
			t.Fatalf("redirect selection: %v", err)
		}
		if sel.Profile != wantProfile {
			t.Fatalf("profile=%q want %q", sel.Profile, wantProfile)
		}
		p, cloud, _, err := s.providerSvc.Main()
		if err != nil || !cloud {
			t.Fatalf("main cloud=%v err=%v", cloud, err)
		}
		if effective, ok := inference.TaskDestination(p); !ok || effective != sel.PolicyDestination {
			t.Fatalf("effective destination=%q", effective)
		}
		model, ok := inference.TaskModelFor(p)
		if !ok || model != wantModel {
			t.Fatalf("model=%q want %q", model, wantModel)
		}
		assignment, ok := inference.TaskAssignmentFor(p, config.TaskChat)
		if !ok || assignment != c.TaskAssignments[config.TaskChat] {
			t.Fatalf("assignment changed: %+v", assignment)
		}
	}
	check("s", "secondary-standard")
	// Unbuilt settings must not change either route or model in a published graph.
	c.LocalRedirect = config.DestinationPrimary
	c.LocusMode = "open_only"
	s.cfgSvc.Set(c)
	check("s", "secondary-standard")
	c.LocusMode = "cloud_only"
	s.cfgSvc.Set(c)
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	check("p", "primary-standard")
}
