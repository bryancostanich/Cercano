package enterprise

import (
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/pkg/proto"
	"context"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestHostPinsSharedSkillsThroughUpdateRemovalAndRollback(t *testing.T) {
	f := newFixture(t, true)
	h := fixtureHost(t, f, t.TempDir())
	if err := h.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, finish, err := h.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	id := "enterprise/" + testOrg + "/review"
	for i, version := range []string{"2", "removed", "1"} {
		f.mu.Lock()
		f.revision = int64(i + 2)
		f.skills = []v1.SkillContentResponse{}
		if version != "removed" {
			f.skills = append(f.skills, v1.SkillContentResponse{SchemaVersion: v1.Version, ID: testOrg + "/review", Version: version, Name: "Review", Content: "Instructions version " + version})
		}
		f.mu.Unlock()
		if err := h.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if sk, ok := managedsettings.Skill(old, id); !ok || sk.Version != "1" || sk.Content != "Review carefully." {
			t.Fatal("active turn's instructions changed")
		}
		next, release, err := h.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sk, ok := managedsettings.Skill(next, id)
		release()
		if version == "removed" {
			if ok {
				t.Fatal("removed skill survived in new turn")
			}
		} else if !ok || sk.Version != version || sk.Content != "Instructions version "+version {
			t.Fatal("next turn did not receive published skill")
		}
	}
}

func TestSkillDiscoveryScopesAndRevocation(t *testing.T) {
	f := newFixture(t, true)
	h := fixtureHost(t, f, t.TempDir())
	if err := h.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{proto.Agent_ListSkills_FullMethodName, proto.Agent_GetSkill_FullMethodName} {
		_, err := h.UnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
			if _, ok := managedsettings.Skill(ctx, "enterprise/"+testOrg+"/review"); !ok {
				t.Fatal("discovery RPC has no assigned bundle")
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	f.mode = "denied"
	f.mu.Unlock()
	if err := h.Sync(context.Background()); err == nil {
		t.Fatal("revocation sync unexpectedly succeeded")
	}
	_, err := h.UnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: proto.Agent_GetSkill_FullMethodName}, func(context.Context, any) (any, error) {
		t.Fatal("revoked member reached skill catalog")
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked discovery: %v", err)
	}
}
