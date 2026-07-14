package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/server/pkg/agentclient"
)

const (
	runtimeActionOpenRuntimePick = "open_runtime_pick"
	runtimeActionOllamaURL       = "ollama_url"
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
	return rows
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

// openRuntimeSwitchCmd builds the tea.Cmd for a runtime switch. For
// llama_server it checks readiness first and returns
// openOpenRuntimeInstallModalMsg when the runtime isn't usable (missing
// binary or model) — reusing the settings path's install-modal flow. For any
// other target it calls UpdateConfig directly, swallowing the error the same
// way dispatchOpenRuntimeSwitch does (the server broadcasts ConfigChanged).
func openRuntimeSwitchCmd(ag *agentclient.Client, target string) tea.Cmd {
	if ag == nil {
		return nil
	}
	return func() tea.Msg {
		if target == "llama_server" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			st, err := ag.GetOpenRuntimeStatus(ctx, "llama_server")
			cancel()
			if err == nil && st != nil && !st.Ok {
				return openOpenRuntimeInstallModalMsg{status: *st, pending: "llama_server"}
			}
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, agentclient.ConfigUpdate{OpenRuntime: target})
		return nil
	}
}
