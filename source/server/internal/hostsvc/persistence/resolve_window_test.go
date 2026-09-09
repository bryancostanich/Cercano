package persistence

import (
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	cfg "cercano/source/server/pkg/config"
)

func windowSvc(t *testing.T, c cfg.Config, cloud func(string) (int, bool)) *svc {
	t.Helper()
	return &svc{
		cfgSvc:             cfgsvc.New("", c, secrets.NewMemory()),
		cloudContextWindow: cloud,
	}
}

// The meter denominator must be the capacity the turn was budgeted against.
// A hosted model's published window is invisible to the model-name family
// table, which answers from name matching alone.
func TestResolveWindow_PrefersPublishedCapacityOverNameTable(t *testing.T) {
	x := windowSvc(t, cfg.Config{LocusMode: "cloud_primary"}, func(string) (int, bool) {
		return 262_144, true
	})
	window, known := x.resolveWindow("Qwen/Qwen3.8-2.4T-A95B")
	if window != 262_144 || !known {
		t.Fatalf("resolveWindow = %d/%v, want 262144/true", window, known)
	}
}

// Absent evidence keeps the conventional table — this adds provider truth, it
// does not remove the existing fallback.
func TestResolveWindow_FallsBackToNameTable(t *testing.T) {
	x := windowSvc(t, cfg.Config{LocusMode: "cloud_primary"}, func(string) (int, bool) {
		return 0, false
	})
	window, known := x.resolveWindow("claude-sonnet-4-6")
	if window != 200_000 || !known {
		t.Fatalf("resolveWindow = %d/%v, want the conventional 200000/true", window, known)
	}
}

// On a local route the launched --ctx-size is a hard ceiling. Provider metadata
// for a same-named hosted model must never raise it, or the meter would report
// headroom the running process cannot serve.
func TestResolveWindow_LocalRuntimeCeilingWins(t *testing.T) {
	c := cfg.Config{LocusMode: "open_primary", OpenRuntime: "llama_server"}
	c.LlamaServer.ContextSize = 8192
	c.LlamaServer.ContextSizeSet = true
	x := windowSvc(t, c, func(string) (int, bool) { return 1_048_576, true })
	window, known := x.resolveWindow("some-local-model")
	if window != 8192 || !known {
		t.Fatalf("resolveWindow = %d/%v, want the launched 8192/true", window, known)
	}
}

// A nil resolver behaves exactly as before provider evidence existed.
func TestResolveWindow_NilResolverKeepsPreviousBehavior(t *testing.T) {
	x := windowSvc(t, cfg.Config{LocusMode: "cloud_primary"}, nil)
	window, known := x.resolveWindow("claude-sonnet-4-6")
	if window != 200_000 || !known {
		t.Fatalf("resolveWindow = %d/%v, want 200000/true", window, known)
	}
}
