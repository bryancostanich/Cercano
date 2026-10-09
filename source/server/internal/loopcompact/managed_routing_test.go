package loopcompact

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestCompactionUsesPinnedManagedDefault(t *testing.T) {
	snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
	snapshot.Policy.TaskDefaults[0].Task = string(config.TaskCompaction)
	snapshot.Policy.TaskDefaults[0].Quality = "economy"
	snapshot.Policy.AllowedRoutes[0].Model = "gpt-4o-mini"
	approved, personal := &policyProvider{name: "approved"}, &policyProvider{name: "personal"}
	cfg := config.Config{LocusMode: "open_only", TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskCompaction: {Destination: config.DestinationLocal, Quality: config.CostPremium}}}
	cfg.Compaction.SummarizerModel = "personal-override"
	tiers := inference.Tiers{DeveloperConfig: &cfg, Cloud: personal, Open: personal, TaskFor: cfg.TaskAssignment, ManagedRoute: func(context.Context, v1.Route, config.Destination) (inference.Candidate, error) {
		return inference.Candidate{Provider: approved, IsCloud: true}, nil
	}}
	run := BuildSummarizer(WiringDeps{Cfg: cfg, Candidates: func() inference.Tiers { return tiers }})
	ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
	_, err := run(ctx, []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Preserve this task."}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(personal.requests) != 0 || len(approved.requests) != 1 || approved.requests[0].Model != "gpt-4o-mini" {
		t.Fatalf("compaction lost managed model: personal=%d approved=%+v", len(personal.requests), approved.requests)
	}
}

func TestBackgroundCompactionPinsHostSettingsAndReleasesWork(t *testing.T) {
	snapshot := settingstest.Snapshot("company-a", "1", "Review")
	snapshot.Policy.TaskDefaults[0].Task = string(config.TaskCompaction)
	snapshot.Policy.AllowedRoutes[0].Model = "gpt-4o-mini"
	approved := &policyProvider{name: "approved"}
	starts, finishes := 0, 0
	var beginErr error
	deps := WiringDeps{
		BeginWork: func(ctx context.Context) (context.Context, func(), error) {
			if beginErr != nil {
				return ctx, nil, beginErr
			}
			starts++
			return managedsettings.WithSnapshot(ctx, snapshot), func() { finishes++ }, nil
		},
		Candidates: func() inference.Tiers {
			return inference.Tiers{ManagedRoute: func(context.Context, v1.Route, config.Destination) (inference.Candidate, error) {
				return inference.Candidate{Provider: approved, IsCloud: true}, nil
			}}
		},
	}
	run := BuildSummarizer(deps)
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Keep this context."}}}}
	if _, err := run(t.Context(), msgs); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || finishes != 1 || len(approved.requests) != 1 {
		t.Fatalf("unscoped background work: starts=%d finishes=%d calls=%d", starts, finishes, len(approved.requests))
	}
	// Nested compaction keeps the original turn snapshot, even after host changes.
	beginErr = errors.New("host changing")
	if _, err := run(managedsettings.WithSnapshot(t.Context(), snapshot), msgs); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || finishes != 1 || len(approved.requests) != 2 {
		t.Fatal("pinned turn reopened host scope")
	}
	if _, err := run(t.Context(), msgs); !errors.Is(err, beginErr) || len(approved.requests) != 2 {
		t.Fatal("blocked background work reached provider", err)
	}
}
