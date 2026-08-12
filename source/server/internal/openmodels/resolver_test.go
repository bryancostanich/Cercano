package openmodels

import (
	"testing"

	cfg "cercano/source/server/pkg/config"
)

// fakeCfg is a minimal ConfigSource for resolver tests.
type fakeCfg struct{ c cfg.Config }

func (f fakeCfg) Get() cfg.Config { return f.c }

// newResolver wires a Resolver over a config, a static catalog-default map for
// the active runtime, and a fixed RAM size.
func newResolver(c cfg.Config, defaults map[string]string) *Resolver {
	catalog := func(runtime string, ramBytes uint64) map[string]string {
		if runtime != c.OpenRuntime {
			return nil
		}
		return defaults
	}
	return New(fakeCfg{c: c}, catalog, func() uint64 { return 64 << 30 })
}

func TestVisionModel_Override(t *testing.T) {
	var c cfg.Config
	c.OpenRuntime = "llama_server"
	c.Models.SetOverride("llama_server", cfg.TierVision, "gemma-3-4b-it-q4_k_m")

	r := newResolver(c, nil)
	id, ok := r.VisionModel()
	if !ok || id != "gemma-3-4b-it-q4_k_m" {
		t.Fatalf("VisionModel() = (%q,%v), want gemma-3-4b-it-q4_k_m,true", id, ok)
	}
}

func TestVisionModel_CatalogDefault(t *testing.T) {
	var c cfg.Config
	c.OpenRuntime = "llama_server"

	// No override, but the runtime's catalog offers a vision default.
	r := newResolver(c, map[string]string{"vision": "glm-4.5v-q4_k_m"})
	id, ok := r.VisionModel()
	if !ok || id != "glm-4.5v-q4_k_m" {
		t.Fatalf("VisionModel() = (%q,%v), want glm-4.5v-q4_k_m,true", id, ok)
	}
}

func TestVisionModel_Unconfigured(t *testing.T) {
	var c cfg.Config
	c.OpenRuntime = "llama_server"

	// No override and no catalog vision default: the normal "no vision model"
	// condition. ok must be false, not an error, and id must be empty.
	r := newResolver(c, map[string]string{"everyday": "glm-4.5-air-q4_k_m"})
	id, ok := r.VisionModel()
	if ok || id != "" {
		t.Fatalf("VisionModel() = (%q,%v), want empty,false", id, ok)
	}
}

// TestVisionModel_DoesNotDisturbEveryday guards that adding the vision tier does
// not change everyday resolution.
func TestVisionModel_DoesNotDisturbEveryday(t *testing.T) {
	var c cfg.Config
	c.OpenRuntime = "llama_server"
	c.Models.SetOverride("llama_server", cfg.TierVision, "gemma-3-4b-it-q4_k_m")

	r := newResolver(c, map[string]string{"everyday": "glm-4.5-air-q4_k_m"})
	if got := r.ChatModel(); got != "glm-4.5-air-q4_k_m" {
		t.Fatalf("ChatModel() = %q, want glm-4.5-air-q4_k_m", got)
	}
}
