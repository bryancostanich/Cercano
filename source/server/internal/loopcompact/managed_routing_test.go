package loopcompact

import (
	"context"
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
