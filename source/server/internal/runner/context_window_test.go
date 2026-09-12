package runner

import (
	llamaengine "cercano/source/server/internal/engine/llamaserver"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/modelwindow"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
	"time"
)

func contextOverridePtr(n int) *int { return &n }

func TestLocalContextWindow_ConfigEditDoesNotChangeServingCapacity(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = contextOverridePtr(65536)
	svc := cfgsvc.New("", cfg, nil)
	manager := localruntime.NewManager()
	const model = "glm-4.5-air-q4_k_m"
	instance := localruntime.InstanceRecord{ID: "serving", ModelID: model, Runtime: "llama_server", State: localruntime.InstanceRunning, PID: 42, StartedAt: time.Now(), Context: localruntime.ContextCapacity{ConfirmedTokens: 65536, ConfirmedAt: time.Now()}}
	manager.UpdateInstance(instance)
	provider := llamaengine.NewLLMProvider(llamaengine.NewEngine(manager))
	core := &Core{d: Deps{Config: svc, Providers: &fakeResolver{open: provider}}}
	before, known := core.knownContextWindowFor(false, model)
	if !known || before != 65536 {
		t.Fatalf("initial capacity=(%d,%v)", before, known)
	}
	cfg.LlamaServer.ContextSize = contextOverridePtr(8192)
	if err := svc.Set(cfg); err != nil {
		t.Fatal(err)
	}
	after, known := core.knownContextWindowFor(false, model)
	if !known || after != before {
		t.Fatalf("config edit changed serving budget to (%d,%v)", after, known)
	}
	instance.State = localruntime.InstanceStopped
	manager.UpdateInstance(instance)
	if n, known := core.knownContextWindowFor(false, model); n != 0 || known {
		t.Fatal("stopped instance retained capacity")
	}
	if n, known := core.knownContextWindowFor(false, "other-model"); n != 0 || known {
		t.Fatal("capacity leaked across models")
	}
}

func TestLocalContextWindow_ConfigAloneIsUnknown(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = contextOverridePtr(65536)
	for _, model := range []string{"", "qwen3", "glm-4.5-air-q4_k_m"} {
		if n := modelwindow.LocalRuntimeWindow(cfg, model); n != 0 {
			t.Fatalf("config/catalog guessed %d", n)
		}
	}
}

func TestLocalContextWindow_MistralRSUnaffected(t *testing.T) {
	cfg := config.Config{OpenRuntime: "mistralrs"}
	cfg.MistralRS.MaxSeqLen = 8192
	if got := modelwindow.LocalRuntimeWindow(cfg, "mistralrs:catalog:qwen3-4b"); got != 8192 {
		t.Fatalf("mistral window=%d", got)
	}
}

type confirmedRuntimeProvider struct {
	inference.Provider
	window int
}

func (p *confirmedRuntimeProvider) Name() string { return "llama_server" }
func (p *confirmedRuntimeProvider) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: p.window, InstanceID: "fixture"}, nil
}
