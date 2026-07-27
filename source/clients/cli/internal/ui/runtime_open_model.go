package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/server/pkg/agentclient"
)

const (
	runtimeActionOpenRuntimePick     = "open_runtime_pick"
	runtimeActionOllamaURL           = "ollama_url"
	runtimeActionMistralPagedAttn    = "mistralrs_paged_attn"
	runtimeActionMistralPAMemoryFrac = "mistralrs_pa_memory_fraction"
	runtimeActionMistralPAMemoryMB   = "mistralrs_pa_memory_mb"
	runtimeActionMistralISQ          = "mistralrs_isq"
)

// runtimeConfigRows builds the Runtime tab's picker block: the open-runtime
// switch and, only when the ollama runtime is active, the ollama-URL edit. The
// legacy "chat model" (open_model) picker was dropped — it predates the model
// tier system and no longer has a meaningful role. Each row is actionable;
// Enter opens a wizard-style overlay for that field.
func runtimeConfigRows(cfg *agentclient.Config) []runtimeDashboardActionRow {
	if cfg == nil {
		return []runtimeDashboardActionRow{{Label: "runtime", Value: "config unavailable"}}
	}
	runtime := firstNonEmpty(cfg.OpenRuntime, "ollama")
	rows := []runtimeDashboardActionRow{
		{
			Label:  "runtime",
			Value:  runtime,
			Hint:   "enter to change",
			Action: runtimeDashboardAction{Kind: runtimeActionOpenRuntimePick},
		},
	}
	// The ollama endpoint is only meaningful under the ollama runtime; the
	// embedded llama-server manages its own process and has no URL to set.
	if runtime == "ollama" {
		ollamaURL := cfg.OllamaURL
		if ollamaURL == "" {
			ollamaURL = "—"
		}
		rows = append(rows, runtimeDashboardActionRow{
			Label:  "ollama URL",
			Value:  ollamaURL,
			Hint:   "enter to edit",
			Action: runtimeDashboardAction{Kind: runtimeActionOllamaURL},
		})
	}
	// mistral.rs process-launch flags. They only apply when mistral.rs is the
	// active runtime, and take effect on the next runtime start — the running
	// sidecar keeps its current flags until restarted.
	if runtime == "mistralrs" {
		rows = append(rows, mistralRuntimeRows(cfg)...)
	}
	return rows
}

// mistralRuntimeRows builds the three mistral.rs launch-flag rows: paged
// attention (concurrency/throughput), KV-cache memory budget, and ISQ
// (in-situ quantization). "restart" hints flag that these bite on the next
// runtime start, not live.
func mistralRuntimeRows(cfg *agentclient.Config) []runtimeDashboardActionRow {
	return []runtimeDashboardActionRow{
		{
			Label:  "paged-attn",
			Value:  firstNonEmpty(cfg.MistralRSPagedAttn, "auto"),
			Hint:   "enter to change · restart",
			Action: runtimeDashboardAction{Kind: runtimeActionMistralPagedAttn},
		},
		{
			Label:  "pa-memory-fraction",
			Value:  firstNonEmpty(cfg.MistralRSPAMemoryFraction, "—"),
			Hint:   "enter to edit · restart",
			Action: runtimeDashboardAction{Kind: runtimeActionMistralPAMemoryFrac},
		},
		{
			Label:  "pa-memory-mb",
			Value:  firstNonEmpty(cfg.MistralRSPAMemoryMB, "—"),
			Hint:   "enter to edit · absolute cap, wins over fraction · restart",
			Action: runtimeDashboardAction{Kind: runtimeActionMistralPAMemoryMB},
		},
		{
			Label:  "isq",
			Value:  firstNonEmpty(cfg.MistralRSISQ, "—"),
			Hint:   "enter to edit · restart",
			Action: runtimeDashboardAction{Kind: runtimeActionMistralISQ},
		},
	}
}

// openRuntimePicker installs the ollama / embedded-llama-server radio picker.
// Selecting fires openRuntimeSwitchCmd, which gates the llama_server switch on
// readiness (opening the install modal when the runtime isn't usable).
func (d *runtimeDashboard) openRuntimePicker() {
	cfg := d.snapshot.Config
	current := ""
	if cfg != nil {
		current = cfg.OpenRuntime
	}
	rows := []overlay.Row{
		{
			Key:      "ollama",
			Label:    "ollama",
			Value:    "local Ollama server",
			Selected: current == "ollama" || current == "",
		},
		{
			Key:      "llama_server",
			Label:    "embedded llama-server",
			Value:    "managed GGUF runtime",
			Selected: current == "llama_server",
		},
		{
			Key:      "mistralrs",
			Label:    "mistral.rs",
			Value:    "managed UQFF/safetensors runtime",
			Selected: current == "mistralrs",
		},
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			msg := ""
			if row.Key != current {
				// A runtime swap only takes effect on the next launch: the
				// worker builds its open provider at startup and the tier
				// models differ per runtime. Tell the user rather than let
				// it look like nothing happened.
				msg = "runtime set to " + row.Key + " — restart to load its models"
			}
			return msg, true, openRuntimeSwitchCmd(d.agent, row.Key)
		},
	}
	picker := overlay.New("open runtime", rows, hooks)
	d.tierPicker = &picker
}

// openOllamaURLPicker installs a single editable-row overlay for the ollama
// endpoint URL, committed via OnEdit.
func (d *runtimeDashboard) openOllamaURLPicker() {
	cfg := d.snapshot.Config
	current := ""
	if cfg != nil {
		current = cfg.OllamaURL
	}
	rows := []overlay.Row{
		{Label: "ollama URL", Value: current, Editable: true},
	}
	hooks := overlay.Hooks{
		OnEdit: func(row overlay.Row, newValue string) (string, error) {
			if d.agent == nil {
				return "no agent connection", nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msg, err := d.agent.UpdateConfig(ctx, agentclient.ConfigUpdate{OllamaURL: newValue})
			if err != nil {
				return "set failed: " + err.Error(), err
			}
			return msg, nil
		},
	}
	picker := overlay.New("ollama URL", rows, hooks)
	d.tierPicker = &picker
}

// openMistralPagedAttnPicker installs the auto/on/off radio for mistral.rs
// paged attention — the concurrency/throughput lever (off by default on
// Metal). Committed via OnSelect → UpdateConfig; takes effect on next start.
func (d *runtimeDashboard) openMistralPagedAttnPicker() {
	current := "auto"
	if cfg := d.snapshot.Config; cfg != nil {
		current = firstNonEmpty(cfg.MistralRSPagedAttn, "auto")
	}
	opts := []struct{ key, desc string }{
		{"auto", "on where supported (Metal/CUDA), off on CPU"},
		{"on", "force paged attention + prefix caching"},
		{"off", "disable paged attention (uncapped KV — risky on Metal)"},
	}
	rows := make([]overlay.Row, len(opts))
	for i, o := range opts {
		rows[i] = overlay.Row{Key: o.key, Label: o.key, Value: o.desc, Selected: current == o.key}
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			return d.mistralConfigStatus(row.Key), true,
				mistralConfigUpdateCmd(d.agent, agentclient.ConfigUpdate{MistralRSPagedAttn: row.Key})
		},
	}
	picker := overlay.New("paged attention", rows, hooks)
	d.tierPicker = &picker
}

// openMistralPAMemoryFractionPicker edits the KV-cache memory budget (0<f<=1).
// Blanking clears it (sends "-", the sparse-patch clear sentinel).
func (d *runtimeDashboard) openMistralPAMemoryFractionPicker() {
	d.openMistralTextPicker(
		"pa-memory-fraction",
		func(c *agentclient.Config) string { return c.MistralRSPAMemoryFraction },
		func(v string) agentclient.ConfigUpdate {
			return agentclient.ConfigUpdate{MistralRSPAMemoryFraction: dashIfEmpty(v)}
		},
	)
}

// openMistralPAMemoryMBPicker edits the absolute KV-cache MB cap. When set it
// takes precedence over the fraction. Blanking clears it (sends "-").
func (d *runtimeDashboard) openMistralPAMemoryMBPicker() {
	d.openMistralTextPicker(
		"pa-memory-mb",
		func(c *agentclient.Config) string { return c.MistralRSPAMemoryMB },
		func(v string) agentclient.ConfigUpdate {
			return agentclient.ConfigUpdate{MistralRSPAMemoryMB: dashIfEmpty(v)}
		},
	)
}

// openMistralISQPicker edits the ISQ (in-situ quantization) level, e.g. Q4K.
// Blanking clears it.
func (d *runtimeDashboard) openMistralISQPicker() {
	d.openMistralTextPicker(
		"isq",
		func(c *agentclient.Config) string { return c.MistralRSISQ },
		func(v string) agentclient.ConfigUpdate {
			return agentclient.ConfigUpdate{MistralRSISQ: dashIfEmpty(v)}
		},
	)
}

// openMistralTextPicker is the shared single-editable-row overlay for the two
// free-text mistral.rs flags, committed via OnEdit → UpdateConfig.
func (d *runtimeDashboard) openMistralTextPicker(
	label string,
	get func(*agentclient.Config) string,
	build func(string) agentclient.ConfigUpdate,
) {
	current := ""
	if cfg := d.snapshot.Config; cfg != nil {
		current = get(cfg)
	}
	rows := []overlay.Row{{Label: label, Value: current, Editable: true}}
	hooks := overlay.Hooks{
		OnEdit: func(row overlay.Row, newValue string) (string, error) {
			if d.agent == nil {
				return "no agent connection", nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msg, err := d.agent.UpdateConfig(ctx, build(newValue))
			if err != nil {
				return "set failed: " + err.Error(), err
			}
			return firstNonEmpty(msg, d.mistralConfigStatus(label)), nil
		},
	}
	picker := overlay.New(label, rows, hooks)
	d.tierPicker = &picker
}

// mistralConfigStatus notes the restart requirement so the change doesn't look
// like it silently did nothing — the running sidecar keeps its old flags.
func (d *runtimeDashboard) mistralConfigStatus(field string) string {
	return field + " updated — restart the runtime to apply"
}

func runtimeSwitchNeedsReadinessProbe(target string) bool {
	switch target {
	case "llama_server", "mistralrs":
		return true
	default:
		return false
	}
}

// mistralConfigUpdateCmd dispatches a mistral.rs config update off the UI
// thread, swallowing the error the way the runtime-switch path does (the
// server broadcasts ConfigChanged on success).
func mistralConfigUpdateCmd(ag *agentclient.Client, update agentclient.ConfigUpdate) tea.Cmd {
	if ag == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, update)
		return nil
	}
}

// openRuntimeSwitchCmd builds the tea.Cmd for a runtime switch. Managed local
// runtimes are probed first; if the target isn't usable (missing binary/model,
// or model downloading), return openOpenRuntimeInstallModalMsg so the user can
// confirm/cancel or browse models. Ollama still switches directly because it
// manages its own model presence.
func openRuntimeSwitchCmd(ag *agentclient.Client, target string) tea.Cmd {
	if ag == nil {
		return nil
	}
	return func() tea.Msg {
		if runtimeSwitchNeedsReadinessProbe(target) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			st, err := ag.GetOpenRuntimeStatus(ctx, target)
			cancel()
			if err == nil && st != nil && !st.Ok {
				return openOpenRuntimeInstallModalMsg{status: *st, pending: target}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, agentclient.ConfigUpdate{OpenRuntime: target})
		return nil
	}
}
