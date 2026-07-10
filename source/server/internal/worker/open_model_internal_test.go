package worker

// open_model_internal_test.go — regression guard for open-model resolution.
//
// Config normalization (finalizeModelTiers) migrates the legacy open_model into
// Models.Tiers.Everyday.Open and BLANKS cfg.OpenModel. So on the host's
// normalized config — the one the worker snapshots — cfg.OpenModel is always
// "". The worker must therefore read the open model from the everyday-open tier
// (OpenChatModel), like the host, NOT from the blanked legacy field.

import (
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
)

func TestWorkerResolver_OpenModelFromEverydayTier(t *testing.T) {
	// Mirror a host-normalized config: OpenModel blanked, model in the tier.
	cfg := pkgcfg.Config{
		OpenModel: "", // finalizeModelTiers blanks this on every load path
		Models: pkgcfg.ModelsConfig{
			Tiers: pkgcfg.ModelTiers{
				Everyday: pkgcfg.ModelTier{Open: "qwen3-coder"},
			},
		},
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
