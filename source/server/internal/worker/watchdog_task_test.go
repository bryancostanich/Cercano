package worker

import (
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

type watchdogTaskProvider struct{ calls []llm.ChatRequest }

func (p *watchdogTaskProvider) Name() string                         { return "fixture" }
func (p *watchdogTaskProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *watchdogTaskProvider) Chat(_ context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls = append(p.calls, r)
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "VIOLATION: yes\nCHALLENGE: fixture"}}}, nil
}
func (p *watchdogTaskProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	panic("unexpected streaming watchdog call")
}
func TestWatchdogTaskRouting(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-local-standard", true: "saved-secondary-premium"}[saved], func(t *testing.T) {
			cfg := config.Config{Watchdog: config.WatchdogConfig{Enabled: true, Mode: "strict", Checks: []string{"debug-loop"}, Model: "retired-pin"}}
			task := config.Task("watchdog")
			if saved {
				cfg.TaskAssignments = map[config.Task]config.TaskAssignment{task: {Destination: config.DestinationSecondary, Quality: config.CostPremium}}
			}
			local, cloud := &watchdogTaskProvider{}, &watchdogTaskProvider{}
			taskCalls := 0
			e := dispatch.NewEngine(func() inference.Tiers {
				return inference.Tiers{Open: local, Cloud: cloud, TaskFor: func(got config.Task) config.TaskAssignment {
					taskCalls++
					if got != task {
						t.Errorf("task=%q", got)
					}
					return cfg.TaskAssignment(got)
				}, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: cloud, Profile: "secondary", IsCloud: true}}}
			}, func() locus.Mode { return locus.CloudPrimary }, nil)
			e.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string {
				return string(sel.Destination) + "-" + string(tier)
			})
			wd := buildWorkerWatchdog(cfg, e)
			if wd == nil {
				t.Fatal("watchdog missing")
			}
			dec := wd.Gate(context.Background(), "fixture", watchdog.Action{Kind: "tool_call", ToolName: "edit_file"})
			if dec.Action != "block" {
				t.Fatalf("watchdog gate=%+v", dec)
			}
			chosen, other, want := local, cloud, "local-everyday"
			if saved {
				chosen, other, want = cloud, local, "secondary-most_capable"
			}
			if taskCalls == 0 || len(chosen.calls) != 1 || len(other.calls) != 0 {
				t.Fatalf("task lookups=%d selected=%d other=%d", taskCalls, len(chosen.calls), len(other.calls))
			}
			if chosen.calls[0].Model != want {
				t.Fatalf("model=%q want %q", chosen.calls[0].Model, want)
			}
		})
	}
}
