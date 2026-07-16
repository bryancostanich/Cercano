package worker_test

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/engine"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
)

// chatProvider is an inference.Provider whose Chat returns a fixed text response.
// The watchdog's OneShot lane calls Provider.Chat (not StreamChat), so this is
// what the watchdog's checks see when they dispatch a supervision prompt.
type chatProvider struct {
	name string
	text string
}

func (p *chatProvider) Name() string { return p.name }
func (p *chatProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *chatProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{
		Blocks:     []llm.Block{{Type: llm.BlockText, Text: p.text}},
		StopReason: "end_turn",
	}, nil
}
func (p *chatProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return &fixedReader{text: p.text}, nil
}

// openResolver serves prov as the OPEN provider (the watchdog oneShot lane
// dispatches to RoleCoproc, which under open_primary picks the open side). It
// mirrors fakeResolver but returns prov from Open()/Main() so the worker's
// dispatch engine resolves it.
type openResolver struct{ prov inference.Provider }

func (r *openResolver) Main() (inference.Provider, bool, bool, error) {
	return r.prov, false, false, nil
}
func (r *openResolver) MainModel(_ bool) string                                         { return "fake-model" }
func (r *openResolver) PrimaryModel() string                                            { return "fake-model" }
func (r *openResolver) Rebuild() error                                                  { return nil }
func (r *openResolver) InstallAbsentCloud(_ string)                                     {}
func (r *openResolver) Cloud() inference.Provider                                       { return nil }
func (r *openResolver) Open() inference.Provider                                        { return r.prov }
func (r *openResolver) ActiveCloudModel() string                                        { return "" }
func (r *openResolver) LocusMode() string                                               { return "" }
func (r *openResolver) Router() providers.RouterCloudUpdater                            { return nil }
func (r *openResolver) Registry() *engine.EngineRegistry                                { return nil }
func (r *openResolver) CatalogManager() *ollamacatalog.Manager                          { return nil }
func (r *openResolver) OpenLegacy() *legacymodels.OpenModelProvider                     { return nil }
func (r *openResolver) Reconfigure(_ providers.ReconfigureArgs)                         {}
func (r *openResolver) SetCloudLLMProvider(_ inference.Provider)                        {}
func (r *openResolver) SetOpenLLMProvider(_ inference.Provider)                         {}
func (r *openResolver) SetOpenProviderFactory(_ func(config.Config) inference.Provider) {}
func (r *openResolver) CloudLLMProvider() inference.Provider                            { return nil }
func (r *openResolver) OpenLLMProvider() inference.Provider                             { return nil }
func (r *openResolver) SetCatalogManager(_ *ollamacatalog.Manager)                      {}
func (r *openResolver) SetUsageSink(_ func(usage.Usage))                                {}

// TestBuildWorkerWatchdog_DisabledIsNil confirms the default-off path: an
// unset (or explicitly disabled) watchdog config yields a nil watchdog, exactly
// like in-process. The runner's live accessor then behaves as if the watchdog
// never existed.
func TestBuildWorkerWatchdog_DisabledIsNil(t *testing.T) {
	r := &openResolver{prov: &chatProvider{name: "open", text: ""}}

	// Enabled=false (zero value).
	cfg := config.Config{LocusMode: "open_primary"}
	if wd := worker.BuildWorkerWatchdogForTest(cfg, r); wd != nil {
		t.Fatalf("disabled watchdog should be nil, got %v", wd)
	}

	// Explicit Enabled=false with checks present — still nil.
	cfg.Watchdog = config.WatchdogConfig{Enabled: false, Mode: "strict", Checks: []string{"debug-loop"}}
	if wd := worker.BuildWorkerWatchdogForTest(cfg, r); wd != nil {
		t.Fatalf("explicitly-disabled watchdog should be nil, got %v", wd)
	}
}

// TestBuildWorkerWatchdog_EnabledGatesToolCall confirms an enabled worker
// watchdog actually GATES a tool call: in strict mode, a debug-loop violation
// (the worker's own local fast-model returns "VIOLATION: yes") blocks an
// edit_file action. This proves the watchdog is wired to the WORKER's provider
// (the oneShot dispatch resolves the worker's open provider, not the host).
func TestBuildWorkerWatchdog_EnabledGatesToolCall(t *testing.T) {
	// The worker's local fast-model returns a violation verdict deterministically.
	prov := &chatProvider{name: "open", text: "VIOLATION: yes\nCHALLENGE: no debug-loop evidence"}
	r := &openResolver{prov: prov}

	cfg := config.Config{
		LocusMode: "open_primary",
		Watchdog: config.WatchdogConfig{
			Enabled: true,
			Mode:    "strict",
			Checks:  []string{"debug-loop"},
		},
	}

	wd := worker.BuildWorkerWatchdogForTest(cfg, r)
	if wd == nil {
		t.Fatal("enabled watchdog should be non-nil")
	}

	// edit_file is an edit tool → debug-loop check applies. The oneShot lane
	// dispatches to the worker's open provider, which returns VIOLATION: yes.
	act := watchdog.Action{
		Kind:     "tool_call",
		ToolName: "edit_file",
		ToolArgs: json.RawMessage(`{"path":"main.go"}`),
	}
	dec := wd.Gate(context.Background(), "conv-1", act)
	if dec.Action != "block" {
		t.Fatalf("strict-mode watchdog should BLOCK a debug-loop violation, got %q (challenge=%q)", dec.Action, dec.Challenge)
	}
}

// TestBuildWorkerWatchdog_EnabledAllowsWhenNoViolation confirms the enabled
// watchdog allows when the worker's local model reports no violation — the gate
// is real, not a hard-coded block.
func TestBuildWorkerWatchdog_EnabledAllowsWhenNoViolation(t *testing.T) {
	prov := &chatProvider{name: "open", text: "VIOLATION: no"}
	r := &openResolver{prov: prov}

	cfg := config.Config{
		LocusMode: "open_primary",
		Watchdog: config.WatchdogConfig{
			Enabled: true,
			Mode:    "strict",
			Checks:  []string{"debug-loop"},
		},
	}

	wd := worker.BuildWorkerWatchdogForTest(cfg, r)
	if wd == nil {
		t.Fatal("enabled watchdog should be non-nil")
	}
	act := watchdog.Action{
		Kind:     "tool_call",
		ToolName: "edit_file",
		ToolArgs: json.RawMessage(`{"path":"main.go"}`),
	}
	dec := wd.Gate(context.Background(), "conv-2", act)
	if dec.Action != "allow" {
		t.Fatalf("no-violation should ALLOW, got %q", dec.Action)
	}
}
