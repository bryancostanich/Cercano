package providers

import (
	"cercano/source/server/internal/inference/profilechain"
	"context"
	"fmt"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

type recordingRouter struct {
	open  agent.TurnRunner
	cloud agent.TurnRunner
}

func (r *recordingRouter) SetOpenProvider(p agent.TurnRunner)  { r.open = p }
func (r *recordingRouter) SetCloudProvider(p agent.TurnRunner) { r.cloud = p }
func (r *recordingRouter) Tiers() agent.Tiers                  { return agent.Tiers{Open: r.open, Cloud: r.cloud} }

type labelProvider struct{ label string }

func (p *labelProvider) Name() string                         { return p.label }
func (p *labelProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *labelProvider) Chat(_ context.Context, req inference.Call) (inference.Result, error) {
	return inference.Result{
		Model: p.label + ":" + req.Model,
		Blocks: []llm.Block{{
			Type: llm.BlockText,
			Text: p.label + ":" + req.Model,
		}},
	}, nil
}
func (p *labelProvider) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	return nil, nil
}

type staticSecretStore struct{ keys map[string]string }

func (s staticSecretStore) Get(profile string) (string, error) { return s.keys[profile], nil }
func (s staticSecretStore) Set(profile, key string) error      { return nil }
func (s staticSecretStore) Delete(profile string) error        { return nil }
func (s staticSecretStore) List() ([]string, error) {
	out := make([]string, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, k)
	}
	return out, nil
}

type quotaLabelProvider struct{ labelProvider }

func (*quotaLabelProvider) Chat(context.Context, inference.Call) (inference.Result, error) {
	return inference.Result{}, &llm.Error{Class: llm.ErrQuota, Err: fmt.Errorf("fixture quota")}
}
func TestBackupResolvesInheritedRequestedQuality(t *testing.T) {
	c := config.Defaults()
	// Inheritance is what's under test: an unset call tier must fall back to the
	// destination's task quality. Pin Chat to premium so the expected fallback
	// is most_capable, independent of the product default.
	c.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskChat: {Quality: config.CostPremium}}
	c.ActiveCloudProfile = "primary"
	c.BackupCloudProfile = "backup"
	c.CloudProfiles = []config.CloudProfile{{Name: "primary", Provider: "anthropic", Flavor: "messages"}, {Name: "backup", Provider: "anthropic", Flavor: "messages"}}
	for _, tier := range []config.Tier{"", config.TierEveryday} {
		chain, err := profilechain.Build(c, config.DestinationPrimary, func(p config.CloudProfile) (inference.Provider, error) {
			if p.Name == "primary" {
				return &quotaLabelProvider{}, nil
			}
			return &labelProvider{label: "backup"}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := chain.Chat(context.Background(), inference.Call{Model: "primary", Tier: string(tier)})
		if tier == "" {
			tier = config.TierMostCapable
		}
		want := "backup:" + c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[1], tier)
		if err != nil || result.Model != want {
			t.Fatalf("inherited backup model=%q want=%q err=%v", result.Model, want, err)
		}
	}
}

func TestReconfigure_RebuildsAndResetsOpenTurnRunner(t *testing.T) {
	router := &recordingRouter{}
	svc := New(nil, nil, router, nil, nil, nil, nil)
	svc.SetOpenLLMProvider(&labelProvider{label: "initial"})
	svc.SetOpenProviderFactory(func(c config.Config) inference.Provider {
		return &labelProvider{label: c.OpenRuntime}
	})

	svc.Reconfigure(ReconfigureArgs{
		OpenRuntime:       "llama_server",
		ResolvedOpenModel: "/models/model-a.gguf",
		MutatedConfig:     config.Config{OpenRuntime: "llama_server"},
	})

	if router.open == nil {
		t.Fatalf("router open tier was not reset")
	}
	resp, err := router.open.Process(context.Background(), &agent.Request{Input: "hello"})
	if err != nil {
		t.Fatalf("open tier Process: %v", err)
	}
	want := "llama_server:/models/model-a.gguf"
	if resp.Output != want {
		t.Fatalf("open tier output = %q, want %q", resp.Output, want)
	}
	if svc.OpenLLMProvider().Name() != "llama_server" {
		t.Fatalf("raw open provider was not rebuilt, got %q", svc.OpenLLMProvider().Name())
	}
}

func TestReconfigure_OpenModelOnlyResetsOpenTurnRunner(t *testing.T) {
	router := &recordingRouter{}
	svc := New(nil, nil, router, nil, nil, nil, nil)
	svc.SetOpenLLMProvider(&labelProvider{label: "ollama"})

	svc.Reconfigure(ReconfigureArgs{OpenModel: "qwen3-coder", ResolvedOpenModel: "qwen3-coder"})

	if router.open == nil {
		t.Fatalf("router open tier was not reset for model-only change")
	}
	resp, err := router.open.Process(context.Background(), &agent.Request{Input: "hello"})
	if err != nil {
		t.Fatalf("open tier Process: %v", err)
	}
	if resp.Output != "ollama:qwen3-coder" {
		t.Fatalf("open tier output = %q, want ollama:qwen3-coder", resp.Output)
	}
}

func TestClearingOpenProviderRemovesOldTurnRunner(t *testing.T) {
	r := &recordingRouter{}
	svc := &service{router: r}
	svc.SetOpenLLMProvider(&labelProvider{label: "old"})
	svc.setOpenTurnRunner("old-model")
	if r.open == nil {
		t.Fatal("fixture never installed old runner")
	}
	svc.SetOpenLLMProvider(nil)
	if svc.Open() != nil || r.open != nil {
		t.Fatal("cleared native provider retained old model runner")
	}
}
