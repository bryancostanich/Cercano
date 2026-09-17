package config

import (
	"path/filepath"
	"testing"
)

func routingFixture() Config {
	c := Defaults()
	c.CloudProfiles = []CloudProfile{{Name: "p", Provider: "deepinfra"}, {Name: "pb", Provider: "deepinfra"}, {Name: "s", Provider: "deepinfra"}, {Name: "sb", Provider: "deepinfra"}}
	c.ActiveCloudProfile, c.BackupCloudProfile = "p", "pb"
	c.SecondaryCloudProfile, c.SecondaryBackupCloudProfile = "s", "sb"
	return c
}

func TestRoutingSparseProfilesAndTasks(t *testing.T) {
	c := routingFixture()
	if err := c.CloudProfiles[0].SetTierOverride(CostPremium, "custom"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"custom", "zai-org/GLM-5.3"} {
		if got := c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[i], TierMostCapable); got != want {
			t.Fatalf("profile %d: %q", i, got)
		}
	}
	c.CloudProfiles[0].SetTierOverride(CostPremium, "")
	tiers := c.ModelProfiles.Cloud.Providers["deepinfra"]
	tiers.Premium.Model = "updated"
	c.ModelProfiles.Cloud.Providers["deepinfra"] = tiers
	if got := c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[0], TierMostCapable); got != "updated" {
		t.Fatal(got)
	}
	if got := c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[0], TierVision); got != "" {
		t.Fatal("invented image model", got)
	}
	c.CloudProfiles[0].ImageModel = "image"
	if got := c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[0], TierVision); got != "image" {
		t.Fatal(got)
	}
	if got := c.ResolveTask(TaskChat, ""); got != (TaskAssignment{DestinationPrimary, CostStandard}) {
		t.Fatal(got)
	}
	if got := c.ResolveTask(TaskDispatch, ""); got != (TaskAssignment{DestinationSecondary, CostPremium}) {
		t.Fatal(got)
	}
	c.TaskAssignments = map[Task]TaskAssignment{TaskDispatch: {Destination: DestinationSecondary, Quality: CostStandard}}
	for input, want := range map[string]CostTier{"": CostStandard, "deep": CostPremium, "DEEP": CostPremium, "light": CostEconomy, "standard": CostStandard, "unknown": CostEconomy} {
		if got := c.ResolveTask(TaskDispatch, input); got.Quality != want || got.Destination != DestinationSecondary {
			t.Fatalf("%q: %+v", input, got)
		}
	}
}

func TestRoutingReferences(t *testing.T) {
	for _, primaryBackup := range []string{"", "pb"} {
		for _, secondaryBackup := range []string{"", "sb"} {
			c := routingFixture()
			c.BackupCloudProfile, c.SecondaryBackupCloudProfile = primaryBackup, secondaryBackup
			if err := c.ValidateRouting(); err != nil {
				t.Fatal(err)
			}
			want := 2
			if primaryBackup != "" {
				want++
			}
			if secondaryBackup != "" {
				want++
			}
			if got := len(c.ReferencedProfiles()); got != want {
				t.Fatalf("%d != %d", got, want)
			}
		}
	}
	c := routingFixture()
	c.SecondaryCloudProfile = "missing"
	if c.ValidateRouting() == nil {
		t.Fatal("accepted dangling reference")
	}
	c = routingFixture()
	c.SecondaryBackupCloudProfile = "s"
	if c.ValidateRouting() == nil {
		t.Fatal("accepted self failover")
	}
	c = routingFixture()
	c.SecondaryCloudProfile = "p"
	if len(c.ReferencedProfiles()) != 3 {
		t.Fatal("profiles not deduplicated")
	}
	c.SecondaryCloudProfile = ""
	if p, _ := c.DestinationProfiles(DestinationSecondary); p != "" {
		t.Fatal("missing Secondary substituted")
	}
}

func TestRoutingPersistenceAndClone(t *testing.T) {
	c := routingFixture()
	c.CloudProfiles[0].TierOverrides = map[CostTier]string{CostPremium: "custom"}
	c.CloudProfiles[0].ImageModel = "image"
	c.CloudProfiles[0].Model, c.CloudProfiles[0].ModelPinned = "obsolete", true
	c.CloudProfiles[0].Route, c.CloudProfiles[0].Region, c.CloudProfiles[0].AWSProfile = "direct", "region", "aws"
	c.TaskAssignments = map[Task]TaskAssignment{TaskDispatch: {Destination: DestinationSecondary, Quality: CostStandard}}
	clone := c.Clone()
	clone.CloudProfiles[0].TierOverrides[CostPremium] = "changed"
	clone.TaskAssignments[TaskDispatch] = TaskAssignment{DestinationLocal, CostEconomy}
	if c.CloudProfiles[0].TierOverrides[CostPremium] != "custom" || c.TaskAssignments[TaskDispatch].Quality != CostStandard {
		t.Fatal("clone aliases mutable state")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.ValidateRouting(); err != nil {
		t.Fatal(err)
	}
	p := got.CloudProfiles[0]
	if p.Model != "" || p.ModelPinned || p.TierOverrides[CostPremium] != "custom" || p.ImageModel != "image" || p.Region != "region" || p.AWSProfile != "aws" || p.Route != "direct" {
		t.Fatalf("round trip: %+v", p)
	}
	if got.SecondaryCloudProfile != "s" || got.SecondaryBackupCloudProfile != "sb" || got.TaskAssignment(TaskDispatch).Quality != CostStandard {
		t.Fatal("assignment round trip failed")
	}
	if c.CloudProfiles[0].Model != "obsolete" {
		t.Fatal("save mutated caller")
	}
}
