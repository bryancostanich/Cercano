package config

import (
	cfg "cercano/source/server/pkg/config"
	"testing"
)

func TestDestinationEditsAreIndependentAndValidated(t *testing.T) {
	c := cfg.Config{CloudProfiles: []cfg.CloudProfile{{Name: "p"}, {Name: "pb"}, {Name: "s"}, {Name: "sb"}}}
	svc := New("", c, nil)
	if err := svc.SetDestinationProfiles(cfg.DestinationPrimary, "p", "pb"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDestinationProfiles(cfg.DestinationSecondary, "s", "sb"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDestinationProfiles(cfg.DestinationSecondary, "s", "s"); err == nil {
		t.Fatal("self failover accepted")
	}
	if err := svc.SetDestinationProfiles(cfg.DestinationSecondary, "missing", ""); err == nil {
		t.Fatal("missing profile accepted")
	}
	got := svc.Get()
	if got.ActiveCloudProfile != "p" || got.BackupCloudProfile != "pb" || got.SecondaryBackupCloudProfile != "sb" {
		t.Fatalf("mutation leaked: %+v", got)
	}
	svc.RemoveProfile("sb")
	got = svc.Get()
	if got.SecondaryBackupCloudProfile != "" || got.BackupCloudProfile != "pb" {
		t.Fatal("removal redirected backup")
	}
	svc.RemoveProfile("s")
	if got := svc.Get(); got.SecondaryCloudProfile != "" || got.ActiveCloudProfile != "p" {
		t.Fatal("removal redirected destination")
	}
}

func TestProfileOverrideOwnership(t *testing.T) {
	svc := New("", cfg.Config{ActiveCloudProfile: "p"}, nil)
	profile := cfg.CloudProfile{Name: "p", TierOverrides: map[cfg.CostTier]string{cfg.CostPremium: "original"}}
	svc.UpsertProfile(profile)
	profile.TierOverrides[cfg.CostPremium] = "caller mutation"
	got, _ := svc.ActiveProfile()
	if got.TierOverrides[cfg.CostPremium] != "original" {
		t.Fatal("upsert aliases input")
	}
	got.TierOverrides[cfg.CostPremium] = "reader mutation"
	again, _ := svc.ActiveProfile()
	if again.TierOverrides[cfg.CostPremium] != "original" {
		t.Fatal("active profile aliases live map")
	}
}

func TestTaskAssignmentClearRestoresDefaults(t *testing.T) {
	svc := New("", cfg.Config{}, nil)
	a := cfg.TaskAssignment{Destination: cfg.DestinationLocal, Quality: cfg.CostEconomy}
	if err := svc.SetTaskAssignment(cfg.TaskDispatch, &a); err != nil {
		t.Fatal(err)
	}
	if svc.Get().TaskAssignment(cfg.TaskDispatch) != a {
		t.Fatal("assignment lost")
	}
	if err := svc.SetTaskAssignment(cfg.TaskDispatch, nil); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get().TaskAssignment(cfg.TaskDispatch); got.Destination != cfg.DestinationSecondary || got.Quality != cfg.CostPremium {
		t.Fatal(got)
	}
}
