package routingwire

import (
	"reflect"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

func TestOrderedBackupsWireRoundTrip(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "a", CloudProfiles: []config.CloudProfile{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
	c.SetPrimaryBackups([]string{"b", "c"})
	var got config.Config
	if err := ApplySnapshot(&got, Snapshot(c)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PrimaryBackups(), []string{"b", "c"}) || len(got.CloudProfiles) != 3 {
		t.Fatalf("lost accounts: %+v", got)
	}
	ApplyAssignments(&got, &proto.RoutingAssignments{Primary: "a", PrimaryBackup: "b"})
	if !reflect.DeepEqual(got.PrimaryBackups(), []string{"b"}) {
		t.Fatal("legacy draft not accepted")
	}
	ApplyAssignments(&got, &proto.RoutingAssignments{Primary: "a"})
	if len(got.PrimaryBackups()) != 0 || got.BackupCloudProfile != "" {
		t.Fatal("clear resurrected backup")
	}
}
