package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
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
	available := map[string]bool{}
	for _, name := range got.GetAvailable() {
		available[name] = true
	}
	if !available["plan"] || !available["autonomous"] {
		t.Fatalf("available = %v, want to include plan and autonomous", got.GetAvailable())
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

	// Autonomous is a live posture too, but it does not fence normal tool tiers by
	// itself; permission mode remains the approval dial.
	res, err = srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "autonomous", ConversationId: conv})
	if err != nil || !res.GetOk() {
		t.Fatalf("SetSessionProfile(autonomous): err=%v resp=%+v", err, res)
	}
	got, _ = srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if got.GetActive() != "autonomous" {
		t.Fatalf("active = %q, want autonomous", got.GetActive())
	}
	if !srv.runnerDeps().Profiles(conv).Restricts() {
		t.Fatal("autonomous: runner accessor should signal active profile")
	}

	// Return to planning for the unchanged-profile assertion below.
	res, err = srv.SetSessionProfile(ctx, &proto.SetSessionProfileRequest{Name: "plan", ConversationId: conv})
	if err != nil || !res.GetOk() {
		t.Fatalf("SetSessionProfile(plan) second time: err=%v resp=%+v", err, res)
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

func TestSessionProfile_RehydratesAutonomousFromRunningLedger(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	const conv = "conv-auto-ledger"
	if err := store.EnsureConversation(ctx, conv, "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: conv, State: "running", BriefJSON: `{"goal":"ship"}`}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}

	got, err := srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if err != nil {
		t.Fatalf("GetSessionProfile: %v", err)
	}
	if got.GetActive() != "autonomous" {
		t.Fatalf("active = %q, want autonomous", got.GetActive())
	}
	if srv.profileBroker.ActiveName(conv) != "autonomous" {
		t.Fatalf("broker active = %q, want autonomous", srv.profileBroker.ActiveName(conv))
	}
}

func TestSessionProfile_RehydratesAutonomousFromActiveRunNotLatestHistoricalRun(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	const conv = "conv-auto-active-with-history"
	if err := store.EnsureConversation(ctx, conv, "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if _, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: conv, State: "running", BriefJSON: `{"goal":"ship"}`}); err != nil {
		t.Fatalf("CreateAutonomyRun running: %v", err)
	}
	if _, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: conv, State: "completed", BriefJSON: `{"goal":"older history"}`}); err != nil {
		t.Fatalf("CreateAutonomyRun completed: %v", err)
	}

	got, err := srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if err != nil {
		t.Fatalf("GetSessionProfile: %v", err)
	}
	if got.GetActive() != "autonomous" {
		t.Fatalf("active = %q, want autonomous", got.GetActive())
	}
}

func TestSessionProfile_DoesNotRehydrateAutonomousFromTerminalLedger(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	const conv = "conv-auto-done"
	if err := store.EnsureConversation(ctx, conv, "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: conv, State: "completed", BriefJSON: `{"goal":"ship"}`}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}

	got, err := srv.GetSessionProfile(ctx, &proto.GetSessionProfileRequest{ConversationId: conv})
	if err != nil {
		t.Fatalf("GetSessionProfile: %v", err)
	}
	if got.GetActive() != agent.DefaultProfileName {
		t.Fatalf("active = %q, want default", got.GetActive())
	}
}
