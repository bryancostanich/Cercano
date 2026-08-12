package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/pkg/proto"
)

// The RPCs switch the session profile and the runner's live accessor reflects
// it — proving the entrypoint reaches the tool loop's fence.
func TestSessionProfile_SetGetAndLiveAccessor(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	ctx := context.Background()
	const conv = "conv1"

	// Defaults to unrestricted.
	got, err := srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetActive() != agent.DefaultProfileName {
		t.Fatalf("initial active = %q, want default", got.GetActive())
	}
	if len(got.GetAvailable()) == 0 || got.GetAvailable()[0] != "plan" {
		t.Fatalf("available = %v, want to include plan", got.GetAvailable())
	}

	// The runner's live accessor sees the default (no fence).
	if srv.runnerDeps().Profiles(conv).Restricts() {
		t.Fatal("default: runner accessor should be unrestricted")
	}

	// Enter planning.
	res, err := srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "plan", ConversationId: conv})
	if err != nil || !res.GetOk() {
		t.Fatalf("SetSessionProfile(plan): err=%v resp=%+v", err, res)
	}
	got, _ = srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if got.GetActive() != "plan" {
		t.Fatalf("active = %q, want plan", got.GetActive())
	}
	// Live accessor now returns the fence.
	if !srv.runnerDeps().Profiles(conv).Restricts() {
		t.Fatal("plan: runner accessor should be fenced")
	}

	// A DIFFERENT conversation is NOT fenced by this one entering planning mode.
	if srv.runnerDeps().Profiles("conv-other").Restricts() {
		t.Fatal("planning mode must not leak to another conversation")
	}

	// Unknown profile is a loud error and does not change the active profile.
	res, err = srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "bogus", ConversationId: conv})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res.GetOk() {
		t.Fatal("expected Ok=false for unknown profile")
	}
	got, _ = srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if got.GetActive() != "plan" {
		t.Fatalf("after failed switch, active = %q, want plan (unchanged)", got.GetActive())
	}

	// Leave planning.
	res, _ = srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "off", ConversationId: conv})
	if res.GetOk() {
		// "off" is not a registered name; the broker only accepts ""/"default".
		// The /plan command maps "off"->"default" client-side, so the server
		// legitimately rejects a raw "off". Assert that contract here.
		t.Fatal("server should reject raw \"off\"; the client maps it to default")
	}
	res, _ = srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "default", ConversationId: conv})
	if !res.GetOk() {
		t.Fatal("SetSessionProfile(default) should succeed")
	}
	if srv.runnerDeps().Profiles(conv).Restricts() {
		t.Fatal("after default: runner accessor should be unrestricted again")
	}
}
