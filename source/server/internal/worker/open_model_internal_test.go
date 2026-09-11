package worker

// open_model_internal_test.go — regression guard for open-model resolution.
//
// Config normalization blanks the legacy open_model. The host resolves the
// effective Premium model (override ⊕ catalog) and snapshots it to the worker
// as the active runtime's override. The worker must therefore read the active
// runtime override, NOT the blanked legacy field.

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
)

func TestWorkerResolver_OpenModelFromEverydayTier(t *testing.T) {
	// Mirror a worker snapshot: OpenModel blanked, effective host-resolved
	// model stored as the active runtime's override.
	cfg := pkgcfg.Config{
		LocusMode:   "open_only",
		OpenRuntime: "llama_server",
		OpenModel:   "", // finalizeModelTiers blanks this on every load path
		Models:      workerTestModels("llama_server", map[pkgcfg.Tier]string{pkgcfg.TierMostCapable: "qwen3-coder"}),
	}
	r := &workerResolver{cfgSvc: cfgsvc.New("", cfg, secrets.NewMemory())}

	if got := r.MainModel(false); got != "qwen3-coder" {
		t.Fatalf("MainModel(false) = %q, want %q — must read the Premium-open tier, "+
			"not the blanked legacy cfg.OpenModel", got, "qwen3-coder")
	}
	if got := r.PrimaryModel(); got != "qwen3-coder" {
		t.Fatalf("PrimaryModel() = %q, want %q (open_primary/default locus resolves "+
			"to the Premium-open tier)", got, "qwen3-coder")
	}
}

func TestMainAvailabilityUsesSelectedTaskQuality(t *testing.T) {
	c := pkgcfg.Config{OpenRuntime: "ollama", LocusMode: "open_only", Models: pkgcfg.ModelsConfig{Open: pkgcfg.OpenModels{Overrides: map[string]map[string]string{"ollama": {string(pkgcfg.TierMostCapable): "premium-only"}}}}}
	r := &workerResolver{cfgSvc: cfgsvc.New("", c, nil), openProv: &readinessStub{}}
	provider, isCloud, _, err := r.Main()
	if err != nil || isCloud || provider == nil {
		t.Fatalf("configured Premium model rejected by unrelated Everyday readiness: %v", err)
	}
}

type readinessStub struct{}

func (*readinessStub) Name() string                         { return "ready" }
func (*readinessStub) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (*readinessStub) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("must not infer while selecting")
}
func (*readinessStub) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	panic("must not infer while selecting")
}
