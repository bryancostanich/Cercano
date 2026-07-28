package worker

// open_model_internal_test.go — regression guard for open-model resolution.
//
// Config normalization blanks the legacy open_model. The host resolves the
// effective everyday model (override ⊕ catalog) and snapshots it to the worker
// as the active runtime's override. The worker must therefore read the active
// runtime override, NOT the blanked legacy field.

import (
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
)

func TestWorkerResolver_OpenModelFromEverydayTier(t *testing.T) {
	// Mirror a worker snapshot: OpenModel blanked, effective host-resolved
	// model stored as the active runtime's override.
	cfg := pkgcfg.Config{
		OpenRuntime: "llama_server",
		OpenModel:   "", // finalizeModelTiers blanks this on every load path
		Models:      workerTestModels("llama_server", map[pkgcfg.Tier]string{pkgcfg.TierEveryday: "qwen3-coder"}),
	}
	r := &workerResolver{cfgSvc: cfgsvc.New("", cfg, secrets.NewMemory())}

	if got := r.MainModel(false); got != "qwen3-coder" {
		t.Fatalf("MainModel(false) = %q, want %q — must read the everyday-open tier, "+
			"not the blanked legacy cfg.OpenModel", got, "qwen3-coder")
	}
	if got := r.PrimaryModel(); got != "qwen3-coder" {
		t.Fatalf("PrimaryModel() = %q, want %q (open_primary/default locus resolves "+
			"to the everyday-open tier)", got, "qwen3-coder")
	}
}
