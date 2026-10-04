package runner

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type managedResolver struct {
	*fakeResolver
	tiers inference.Tiers
}

func (r *managedResolver) Candidates() inference.Tiers { return r.tiers }

func TestManagedMainTurnHonorsExplicitModelChoice(t *testing.T) {
	for _, tc := range []struct {
		name, model      string
		unlocked, denied bool
	}{
		{"locked rejects different model", "alternate", false, true},
		{"locked accepts its default", "approved", false, false},
		{"unlocked uses approved choice", "alternate", true, false},
		{"unlocked rejects unapproved choice", "forbidden", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &spyProvider{}
			deps := buildDeps(provider)
			deps.Providers = &managedResolver{fakeResolver: &fakeResolver{prov: provider}, tiers: inference.Tiers{ManagedRoute: func(_ context.Context, _ v1.Route, _ config.Destination) (inference.Candidate, error) {
				return inference.Candidate{Provider: provider, IsCloud: true}, nil
			}}}
			snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
			alternate := snapshot.Policy.AllowedRoutes[0]
			alternate.ID, alternate.Model = "alternate", "alternate"
			snapshot.Policy.AllowedRoutes = append(snapshot.Policy.AllowedRoutes, alternate)
			snapshot.Policy.TaskDefaults[0].AllowDeveloperOverride = tc.unlocked
			ctx := managedsettings.WithSnapshot(context.Background(), snapshot)
			_, err := New(deps).RunTurn(ctx, Request{ConversationID: "model-choice", Input: "Hello", WorkDir: t.TempDir(), ModelOverride: tc.model}, noopSink{}, nil, nil)
			if tc.denied {
				if !modelpolicy.IsDenial(err) || len(provider.requests) != 0 {
					t.Fatalf("choice reached provider: err=%v requests=%d", err, len(provider.requests))
				}
			} else if err != nil || len(provider.requests) != 1 || provider.requests[0].Model != tc.model {
				t.Fatalf("explicit choice lost: err=%v requests=%+v", err, provider.requests)
			}
		})
	}
}
func TestMainTurnUsesManagedDefaultBeforePersonalRouter(t *testing.T) {
	var recorded []usage.Usage
	approved, personal := &spyProvider{}, &spyProvider{}
	deps := buildDeps(personal)
	cfg := config.Config{LocusMode: "cloud_primary", TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationLocal, Quality: config.CostPremium}}}
	deps.Config = &fakeConfig{cfg: cfg}
	deps.Providers = &managedResolver{fakeResolver: &fakeResolver{prov: personal, open: personal, cloud: personal}, tiers: inference.Tiers{DeveloperConfig: &cfg, WrapManagedMain: func(p inference.Provider, isCloud bool) inference.Provider {
		return usage.Wrap(p, "main", isCloud, func(u usage.Usage) { recorded = append(recorded, u) })
	}, ManagedRoute: func(_ context.Context, r v1.Route, _ config.Destination) (inference.Candidate, error) {
		return inference.Candidate{Provider: approved, Profile: "work", IsCloud: true}, nil
	}}}
	ctx := managedsettings.WithSnapshot(context.Background(), settingstest.Snapshot("company-a", "1", "Review carefully."))
	_, err := New(deps).RunTurn(ctx, Request{ConversationID: "managed-main", Input: "Hello", WorkDir: t.TempDir()}, noopSink{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Model != "approved" || !recorded[0].IsCloud {
		t.Fatalf("managed usage lost: %+v", recorded)
	}
	if len(personal.requests) != 0 || len(approved.requests) != 1 || approved.requests[0].Model != "approved" {
		t.Fatalf("personal=%d approved=%+v", len(personal.requests), approved.requests)
	}
}
