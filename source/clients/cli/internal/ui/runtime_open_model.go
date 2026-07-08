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
	runtimeActionOpenModelPick   = "open_model_pick"
	runtimeActionOllamaURL       = "ollama_url"
)

// openModelRows builds the /m "open model" section: the open-runtime switch,
// the chat-model pick, and the ollama-URL edit — moved off the /config page so
// all model configuration lives on the runtime dashboard. Each row is
// actionable; Enter opens a wizard-style overlay for that field.
func openModelRows(cfg *agentclient.Config) []runtimeDashboardActionRow {
	if cfg == nil {
		return []runtimeDashboardActionRow{{Label: "open model", Value: "config unavailable"}}
	}
	chatModel := cfg.OpenModel
	if chatModel == "" {
		chatModel = "—"
	}
	ollamaURL := cfg.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "—"
	}
	return []runtimeDashboardActionRow{
		{
			Label:  "runtime",
			Value:  firstNonEmpty(cfg.OpenRuntime, "ollama"),
			Hint:   "enter to change",
			Action: runtimeDashboardAction{Kind: runtimeActionOpenRuntimePick},
		},
		{
			Label:  "chat model",
			Value:  chatModel,
			Hint:   "enter to change",
			Action: runtimeDashboardAction{Kind: runtimeActionOpenModelPick},
		},
		{
			Label:  "ollama URL",
			Value:  ollamaURL,
			Hint:   "enter to edit",
			Action: runtimeDashboardAction{Kind: runtimeActionOllamaURL},
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
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			return "", true, openRuntimeSwitchCmd(d.agent, row.Key)
		},
	}
	picker := overlay.New("open runtime", rows, hooks)
	d.tierPicker = &picker
}

// openOpenModelPicker installs the chat-model picker seeded from the
// downloaded runtime models — the same source the tiers picker uses.
func (d *runtimeDashboard) openOpenModelPicker() {
	cfg := d.snapshot.Config
	current := ""
	if cfg != nil {
		current = cfg.OpenModel
	}
	var rows []overlay.Row
	for _, m := range downloadedRuntimeModels(runtimeStatusModels(d.snapshot.Status)) {
		// Commit the human-readable name, not the runtime's hash ID — same
		// rule as the tier pickers. open_model must stay resolvable by BOTH
		// runtimes (a cloud turn that fails over lands on the Ollama adapter,
		// which 404s on a llama_server:<hash> ID). Legacy configs holding an
		// ID still get the current marker.
		name := firstNonEmpty(m.DisplayName, m.ID)
		hint := currentHint(name, current)
		if hint == "" {
			hint = currentHint(m.ID, current)
		}
		rows = append(rows, overlay.Row{
			Key:   name,
			Label: name,
			Value: m.Runtime,
			Hint:  hint,
		})
	}
	rows = append(rows, overlay.Row{Key: "-", Label: "(clear)", Value: "unset chat model"})
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			if d.agent == nil {
				return "no agent connection", false, nil
			}
			model := row.Key
			if model == "-" {
				model = ""
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msg, err := d.agent.UpdateConfig(ctx, agentclient.ConfigUpdate{OpenModel: model})
			if err != nil {
				return "set failed: " + err.Error(), false, nil
			}
			return msg, true, nil
		},
	}
	picker := overlay.New("chat model", rows, hooks)
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
