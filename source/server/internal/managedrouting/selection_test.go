package managedrouting

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestManagedSelectionHonorsOnlyUnlockedApprovedPreferences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		unlocked bool
		request  Request
		want     string
		denied   bool
	}{
		{name: "locked ignores saved personal route", want: "approved"},
		{name: "locked rejects explicit different model", request: Request{Model: "personal"}, denied: true},
		{name: "unlocked accepts approved saved route", unlocked: true, want: "personal"},
		{name: "unlocked rejects unapproved explicit model", unlocked: true, request: Request{Model: "unapproved"}, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
			snapshot.Policy.TaskDefaults[0].AllowDeveloperOverride = tc.unlocked
			snapshot.Policy.AllowedRoutes = append(snapshot.Policy.AllowedRoutes, v1.Route{ID: "personal", Provider: "openai", Endpoint: "https://personal.example/v1", Model: "personal", Placement: "external"})
			c := config.Config{ActiveCloudProfile: "personal", CloudProfiles: []config.CloudProfile{{Name: "personal", Flavor: "chat_completions", BaseURL: "https://personal.example/v1", Model: "personal", TierOverrides: map[config.CostTier]string{config.CostStandard: "personal"}}}, TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationPrimary, Quality: config.CostStandard}}}
			var calls []string
			tiers := inference.Tiers{DeveloperConfig: &c, ManagedRoute: func(_ context.Context, r v1.Route, _ config.Destination) (inference.Candidate, error) {
				return inference.Candidate{Provider: &routeFixture{route: r, calls: &calls, window: 32768}, Profile: r.ID, IsCloud: true}, nil
			}}
			ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
			sel, _, model, managed, err := Select(ctx, config.TaskChat, tiers, tc.request)
			if !managed || modelpolicy.IsDenial(err) != tc.denied {
				t.Fatalf("managed=%v err=%v", managed, err)
			}
			if tc.denied {
				return
			}
			if err != nil || model != tc.want {
				t.Fatalf("model=%q err=%v", model, err)
			}
			result, err := sel.Provider.Chat(ctx, inference.Call{})
			if err != nil || result.Model != tc.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestManagedTurnPinsDeveloperPreferencesAcrossNestedSelection(t *testing.T) {
	snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
	snapshot.Policy.TaskDefaults[0].AllowDeveloperOverride = true
	ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
	var calls []string
	old := inference.Tiers{ManagedRoute: func(_ context.Context, r v1.Route, _ config.Destination) (inference.Candidate, error) {
		return inference.Candidate{Provider: &routeFixture{route: r, calls: &calls, window: 32768}, IsCloud: true}, nil
	}}
	ctx = PinCandidates(ctx, old)
	changed := inference.Tiers{DeveloperConfig: &config.Config{TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationSecondary, Quality: config.CostPremium}}}}
	ctx = PinCandidates(ctx, changed)
	_, _, model, _, err := Select(ctx, config.TaskChat, changed, Request{})
	if err != nil || model != "approved" {
		t.Fatalf("nested call used changed preferences: model=%q err=%v", model, err)
	}
	next := managedsettings.WithSnapshot(t.Context(), snapshot)
	if _, _, _, _, err = Select(next, config.TaskChat, changed, Request{}); err == nil {
		t.Fatal("next turn did not observe changed preferences")
	}
}

func TestManagedSelectionCannotLosePinnedDefaults(t *testing.T) {
	ctx := modelpolicy.WithAuthority(t.Context(), modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	if _, _, _, managed, err := Select(ctx, config.TaskChat, inference.Tiers{}, Request{}); !managed || !modelpolicy.IsDenial(err) {
		t.Fatalf("managed=%v err=%v", managed, err)
	}
	if _, _, _, managed, err := Select(context.Background(), config.TaskChat, inference.Tiers{}, Request{}); managed || err != nil {
		t.Fatalf("standalone changed: managed=%v err=%v", managed, err)
	}
}
