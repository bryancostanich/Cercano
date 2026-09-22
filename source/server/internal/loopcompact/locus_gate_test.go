package loopcompact

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

type policyProvider struct {
	name      string
	requests  []llm.ChatRequest
	deadlines []time.Time
	err       error
}

func (p *policyProvider) Name() string                       { return p.name }
func (*policyProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *policyProvider) Chat(ctx context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	p.requests = append(p.requests, r)
	d, _ := ctx.Deadline()
	p.deadlines = append(p.deadlines, d)
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "GOAL: preserve task\nSTATE: implementation pending"}}}, p.err
}
func (*policyProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	panic("unexpected stream")
}

func TestCompactionTaskRoutingPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, mode                      string
		destination                     config.Destination
		quality                         config.CostTier
		redirect                        config.Destination
		missingSecondary, unready, fail bool
		want                            string
		wantErr                         bool
	}{
		{name: "default", want: "secondary"},
		{name: "primary premium", destination: config.DestinationPrimary, quality: config.CostPremium, want: "primary"},
		{name: "local standard", destination: config.DestinationLocal, quality: config.CostStandard, want: "local"},
		{name: "redirect secondary to local", redirect: config.DestinationLocal, want: "local"},
		{name: "secondary absent must not fall back", missingSecondary: true, wantErr: true},
		{name: "secondary error must not use primary", fail: true, want: "secondary", wantErr: true},
		{name: "local only prohibits cloud", mode: "open_only", wantErr: true},
		{name: "cloud only prohibits local", mode: "cloud_only", destination: config.DestinationLocal, wantErr: true},
		{name: "local unavailable", destination: config.DestinationLocal, unready: true, wantErr: true},
		{name: "invalid mode", mode: "invalid", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{LocusMode: tc.mode, SecondaryRedirect: tc.redirect, TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskCompaction: {Destination: tc.destination, Quality: tc.quality}}}
			cfg.Compaction.SummarizerModel = "legacy-local"
			cfg.OpenRuntime = "mistralrs"
			cfg.MistralRS.MaxSeqLen = 32768
			p := &policyProvider{name: "primary"}
			s := &policyProvider{name: "secondary"}
			o := &policyProvider{name: "local"}
			if tc.fail {
				s.err = errors.New("secondary failed")
			}
			tiers := inference.Tiers{Cloud: p, Open: o, TaskFor: cfg.TaskAssignment, ResolveDestination: cfg.ResolveDestination, OpenReady: func(string) bool { return !tc.unready }, Destinations: map[config.Destination]inference.Candidate{
				config.DestinationPrimary: {Provider: p, IsCloud: true, Profile: "primary"}, config.DestinationSecondary: {Provider: s, IsCloud: true, Profile: "secondary"},
			}, ModelFor: func(sel inference.Selection, tier config.Tier) string {
				if !sel.IsCloud {
					return "local-" + string(tier)
				}
				return sel.Profile + "-" + string(tier)
			}}
			if tc.missingSecondary {
				delete(tiers.Destinations, config.DestinationSecondary)
			}
			deps := WiringDeps{Cfg: cfg, Candidates: func() inference.Tiers { return tiers }, OpenRuntimeContext: func(context.Context, string, bool) (llm.RuntimeContext, error) {
				t.Fatal("unexpected runtime startup")
				return llm.RuntimeContext{}, nil
			}}
			start := time.Now()
			_, err := BuildSummarizer(deps)(t.Context(), []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "task evidence"}}}})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			for _, provider := range []*policyProvider{p, s, o} {
				wantCalls := 0
				if provider.name == tc.want {
					wantCalls = 1
				}
				if len(provider.requests) != wantCalls {
					t.Fatalf("%s calls=%d want %d", provider.name, len(provider.requests), wantCalls)
				}
				if wantCalls == 0 {
					continue
				}
				req := provider.requests[0]
				tier := cfg.TaskAssignment(config.TaskCompaction).Quality.CapabilityTier()
				model := provider.name + "-" + string(tier)
				if provider == o && tc.destination == config.DestinationLocal {
					model = "legacy-local"
				}
				if req.Model != model || req.Tier != string(tier) || req.Temperature == nil || *req.Temperature != 0 {
					t.Fatalf("request=%+v", req)
				}
				remaining := provider.deadlines[0].Sub(start)
				if remaining < compaction.ExecutionTimeout-time.Second || remaining > compaction.ExecutionTimeout+time.Second {
					t.Fatalf("deadline budget=%s", remaining)
				}
			}
		})
	}
}

func TestCompactionUsesLiveTaskAssignment(t *testing.T) {
	cfg := config.Config{}
	p := &policyProvider{name: "primary"}
	s := &policyProvider{name: "secondary"}
	tiers := inference.Tiers{Cloud: p, TaskFor: func(task config.Task) config.TaskAssignment { return cfg.TaskAssignment(task) }, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: s, IsCloud: true}}}
	summarize := BuildSummarizer(WiringDeps{Candidates: func() inference.Tiers { return tiers }})
	if _, err := summarize(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	cfg.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskCompaction: {Destination: config.DestinationPrimary, Quality: config.CostPremium}}
	if _, err := summarize(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(s.requests) != 1 || len(p.requests) != 1 || p.requests[0].Tier != string(config.CostPremium.CapabilityTier()) {
		t.Fatal("stale task route")
	}
}

func TestCompactionHonorsCallerContext(t *testing.T) {
	p := &policyProvider{name: "secondary"}
	summarize := BuildSummarizer(WiringDeps{Candidates: func() inference.Tiers {
		return inference.Tiers{Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: p, IsCloud: true}}}
	}})
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
	defer cancel()
	if _, err := summarize(ctx, nil); err != nil {
		t.Fatal(err)
	}
	want, _ := ctx.Deadline()
	if !p.deadlines[0].Equal(want) {
		t.Fatal("extended caller deadline")
	}
	cancel()
	if _, err := summarize(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := summarize(expired, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline=%v", err)
	}
	if len(p.requests) != 1 {
		t.Fatal("called provider after cancellation/deadline")
	}
}

func TestCompactionUsesDestinationBackupAtRequestedQuality(t *testing.T) {
	cfg := config.Config{SecondaryCloudProfile: "secondary", SecondaryBackupCloudProfile: "secondary-backup", CloudProfiles: []config.CloudProfile{
		{Name: "secondary", TierOverrides: map[config.CostTier]string{config.CostEconomy: "secondary-small", config.CostPremium: "secondary-large"}},
		{Name: "secondary-backup", TierOverrides: map[config.CostTier]string{config.CostEconomy: "backup-small", config.CostPremium: "backup-large"}},
	}}
	backup := &policyProvider{name: "backup"}
	chain, err := profilechain.Build(cfg, config.DestinationSecondary, func(p config.CloudProfile) (inference.Provider, error) {
		if p.Name == "secondary" {
			return nil, errors.New("unavailable")
		}
		return backup, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tiers := inference.Tiers{TaskFor: cfg.TaskAssignment, Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: chain, Profile: "secondary", IsCloud: true}}, ModelFor: func(sel inference.Selection, tier config.Tier) string {
		p, _ := cfg.Profile(sel.Profile)
		return cfg.ModelProfiles.ResolveCloudModelForTier(p, tier)
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = BuildSummarizer(WiringDeps{Cfg: cfg, Candidates: func() inference.Tiers { return tiers }})(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.requests) != 1 || backup.requests[0].Model != "backup-small" || backup.requests[0].Tier != string(config.CostEconomy.CapabilityTier()) {
		t.Fatalf("backup request=%+v", backup.requests)
	}
	deadline, _ := ctx.Deadline()
	if !backup.deadlines[0].Equal(deadline) {
		t.Fatal("backup escaped caller deadline")
	}
}

func TestInlineCompactionBudgetCannotExceedSharedPolicy(t *testing.T) {
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{Goal: "g"}, nil
	}
	for _, timeout := range []time.Duration{0, time.Hour, time.Second} {
		c := New(Options{Summarize: summarize, Timeout: timeout})
		want := compaction.ExecutionTimeout
		if timeout > 0 && timeout < want {
			want = timeout
		}
		if c.timeout != want {
			t.Fatalf("timeout=%s want %s", c.timeout, want)
		}
	}
}
