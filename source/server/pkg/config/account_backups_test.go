package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOrderedPrimaryBackups(t *testing.T) {
	c := routingFixture()
	if got := c.PrimaryBackups(); !reflect.DeepEqual(got, []string{"pb"}) {
		t.Fatal(got)
	}
	c.SetPrimaryBackups([]string{"pb", "sb"})
	if err := c.ValidateRouting(); err != nil {
		t.Fatal(err)
	}
	if got := c.DestinationProfileNames(DestinationPrimary); !reflect.DeepEqual(got, []string{"p", "pb", "sb"}) {
		t.Fatal(got)
	}
	if !c.ReferencesProfile("sb") {
		t.Fatal("later backup not referenced")
	}
	clone := c.Clone()
	clone.BackupCloudProfiles[1] = "s"
	if c.BackupCloudProfiles[1] != "sb" {
		t.Fatal("clone aliases backup list")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.PrimaryBackups(), []string{"pb", "sb"}) {
		t.Fatal(loaded.PrimaryBackups())
	}
	c.SetPrimaryBackups(nil)
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PrimaryBackups()) != 0 || loaded.BackupCloudProfile != "" {
		t.Fatal("clear resurrected legacy backup")
	}
	if c.SecondaryBackupCloudProfile != "sb" {
		t.Fatal("changed Secondary")
	}
}

func TestOrderedPrimaryBackupsValidation(t *testing.T) {
	for _, names := range [][]string{{"pb", "pb"}, {"p"}, {"pb", "missing"}, {""}} {
		c := routingFixture()
		c.SetPrimaryBackups(names)
		if err := c.ValidateRouting(); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
}

func TestAddedSubscriptionAccountsSurviveLoad(t *testing.T) {
	c := Defaults()
	c.CloudProfiles = []CloudProfile{{Name: "claude", Flavor: "messages", Route: "subscription"}, {Name: "claude-2", Flavor: "messages", Route: "subscription"}, {Name: "claude-3", Flavor: "messages", Route: "subscription"}}
	c.ActiveCloudProfile = "claude"
	c.SetPrimaryBackups([]string{"claude-2", "claude-3"})
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CloudProfiles) != 3 || !reflect.DeepEqual(got.PrimaryBackups(), c.PrimaryBackups()) {
		t.Fatal("added accounts collapsed")
	}
}
