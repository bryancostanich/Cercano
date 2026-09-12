package routingwire

import (
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
	"testing"
)

func TestRoutingSnapshotRoundTrip(t *testing.T) {
	c := config.Defaults()
	c.ActiveCloudProfile, c.BackupCloudProfile = "p", "pb"
	c.SecondaryCloudProfile, c.SecondaryBackupCloudProfile = "s", "sb"
	c.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskDispatch: {Destination: config.DestinationSecondary, Quality: config.CostStandard}}
	for _, name := range []string{"p", "pb", "s", "sb"} {
		c.CloudProfiles = append(c.CloudProfiles, config.CloudProfile{Name: name, Flavor: "messages", Provider: "anthropic", Route: "subscription", Region: "region", AWSProfile: "aws", TierOverrides: map[config.CostTier]string{config.CostPremium: name + "-text"}, ImageModel: name + "-image"})
	}
	encoded, err := pb.Marshal(Snapshot(c))
	if err != nil {
		t.Fatal(err)
	}
	var wire proto.RoutingSnapshot
	if err := pb.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	got := config.Defaults()
	if err := ApplySnapshot(&got, &wire); err != nil {
		t.Fatal(err)
	}
	if len(got.CloudProfiles) != 4 || got.SecondaryBackupCloudProfile != "sb" || got.TaskAssignment(config.TaskDispatch).Quality != config.CostStandard {
		t.Fatal("routing graph lost")
	}
	for _, p := range got.CloudProfiles {
		if p.TierOverrides[config.CostPremium] != p.Name+"-text" || p.ImageModel != p.Name+"-image" || p.Region != "region" || p.AWSProfile != "aws" || p.Route != "subscription" {
			t.Fatalf("profile lost: %+v", p)
		}
	}
	c.SecondaryCloudProfile = "p"
	if len(Snapshot(c).Profiles) != 3 {
		t.Fatal("duplicate profile identity")
	}
}
func TestChoicesPresence(t *testing.T) {
	p := config.CloudProfile{Name: "p", Region: "aws-region", TierOverrides: map[config.CostTier]string{config.CostPremium: "text"}, ImageModel: "image"}
	if err := ApplyChoices(&p, nil); err != nil || p.ImageModel != "image" {
		t.Fatal("omission changed choices")
	}
	if err := ApplyChoices(&p, &proto.ProfileModelChoices{}); err != nil || len(p.TierOverrides) != 0 || p.ImageModel != "" || p.Region != "aws-region" {
		t.Fatal("clear failed")
	}
	if err := ApplyChoices(&p, &proto.ProfileModelChoices{TierOverrides: map[string]string{"invalid": "x"}}); err == nil {
		t.Fatal("invalid quality accepted")
	}
}
func TestSnapshotAbsenceAndInvalidGraph(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "old", CloudProfiles: []config.CloudProfile{{Name: "old"}}}
	if err := ApplySnapshot(&c, nil); err != nil || c.ActiveCloudProfile != "old" {
		t.Fatal("old snapshot changed")
	}
	invalid := &proto.RoutingSnapshot{Assignments: &proto.RoutingAssignments{Primary: "missing"}}
	if ApplySnapshot(&c, invalid) == nil || c.ActiveCloudProfile != "old" {
		t.Fatal("invalid snapshot applied partially")
	}
}

func TestDestinationRedirectWirePresenceAndAtomicity(t *testing.T) {
	c := config.Config{SecondaryRedirect: config.DestinationLocal, LocalRedirect: config.DestinationPrimary}
	var got config.Config
	if err := ApplySnapshot(&got, Snapshot(c)); err != nil {
		t.Fatal(err)
	}
	if got.SecondaryRedirect != c.SecondaryRedirect || got.LocalRedirect != c.LocalRedirect {
		t.Fatal("snapshot lost redirects")
	}
	ApplyAssignments(&got, nil)
	if got.SecondaryRedirect != c.SecondaryRedirect {
		t.Fatal("absent draft cleared redirects")
	}
	bad := Snapshot(c)
	bad.Assignments.LocalRedirect = "secondary"
	if err := ApplySnapshot(&got, bad); err == nil {
		t.Fatal("cycle accepted")
	}
	if got.LocalRedirect != config.DestinationPrimary {
		t.Fatal("invalid snapshot partially applied")
	}
	ApplyAssignments(&got, &proto.RoutingAssignments{})
	if got.SecondaryRedirect != "" || got.LocalRedirect != "" {
		t.Fatal("present empty draft did not clear redirects")
	}
}
