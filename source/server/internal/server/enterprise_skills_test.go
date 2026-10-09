package server

import (
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/pkg/proto"
	"context"
	"testing"
)

func TestManagedSkillsHaveDistinctIdentityAndMetadata(t *testing.T) {
	srv := &Server{}
	snapshot := settingstest.Snapshot("company-a", "7", "Review database migrations.")
	// A shared title cannot replace a built-in protocol's identity.
	snapshot.Skills[0].Name = "systematic-debugging"
	ctx := managedsettings.WithSnapshot(context.Background(), snapshot)
	catalog, err := srv.ListSkills(ctx, &proto.ListSkillsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sk := range catalog.Skills {
		if sk.Name == "enterprise/company-a/review" {
			found = true
			if sk.Source != "enterprise" || sk.Version != "7" || sk.DisplayName != "systematic-debugging" {
				t.Fatalf("bad metadata: %+v", sk)
			}
		}
	}
	if !found {
		t.Fatal("shared skill missing")
	}
	builtin, err := srv.GetSkill(ctx, &proto.GetSkillRequest{Name: "systematic-debugging"})
	if err != nil || builtin.Source != "builtin" || builtin.Content == snapshot.Skills[0].Content {
		t.Fatal("shared skill shadowed built-in")
	}
	for _, name := range []string{"enterprise/company-a/review", "enterprise/company-b/review"} {
		if _, err := srv.GetSkill(context.Background(), &proto.GetSkillRequest{Name: name}); err == nil {
			t.Fatal("standalone exposed enterprise content")
		}
	}
	got, err := srv.GetSkill(ctx, &proto.GetSkillRequest{Name: "enterprise/company-a/review"})
	if err != nil || got.Content != snapshot.Skills[0].Content || got.Source != "enterprise" || got.Version != "7" {
		t.Fatalf("bad retrieval: %+v %v", got, err)
	}
}
