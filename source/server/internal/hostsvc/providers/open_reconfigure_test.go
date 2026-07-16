package providers

import (
	"context"
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

func (p *labelProvider) Name() string { return p.label }
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

func TestReconfigure_RebuildsAndResetsOpenTurnRunner(t *testing.T) {
	router := &recordingRouter{}
	svc := New(nil, router, nil, nil, nil, nil)
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
	svc := New(nil, router, nil, nil, nil, nil)
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
