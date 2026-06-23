package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/internal/cli/agentclient"
	"cercano/source/server/internal/cli/overlay"
	"cercano/source/server/internal/cli/theme"
)

// configEditor wraps overlay.RowList with the config-specific row builder
// and UpdateConfig save hook. Other overlays (model picker, history picker,
// font picker, etc.) compose the same way: build rows + hooks, hand to
// overlay.New, mount in the root model.
type configEditor struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	list          overlay.RowList
}

func newConfigEditor(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (configEditor, tea.Cmd) {
	rows := buildConfigRows(ag)
	hooks := overlay.Hooks{
		OnEdit: func(row overlay.Row, newValue string) (string, error) {
			return saveSingle(ag, row.Key, newValue)
		},
		OnReload: func() []overlay.Row {
			return buildConfigRows(ag)
		},
	}
	return configEditor{
		palette: p,
		styles:  s,
		agent:   ag,
		width:   w,
		height:  h,
		list:    overlay.New("config editor", rows, hooks),
	}, nil
}

func (ed configEditor) Update(msg tea.KeyPressMsg) (configEditor, tea.Cmd, bool) {
	next, cmd, closed := ed.list.Update(msg, ed.styles)
	ed.list = next
	return ed, cmd, closed
}

func (ed configEditor) View() string {
	return ed.list.View(ed.width, ed.palette, ed.styles)
}

func (ed configEditor) setSize(w, h int) configEditor {
	ed.width = w
	ed.height = h
	return ed
}

// buildConfigRows snapshots the current config into overlay rows.
func buildConfigRows(ag *agentclient.Client) []overlay.Row {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg, err := ag.GetConfig(ctx)
	if err != nil {
		return []overlay.Row{{Label: "error", Value: err.Error(), ReadOnly: true}}
	}
	apiKey := "(unset)"
	if cfg.CloudAPIKeySet {
		apiKey = "(set)"
	}
	return []overlay.Row{
		{Key: "local-runtime", Label: "local-runtime", Value: cfg.LocalRuntime, Editable: true, Hint: "ollama | llama_server"},
		{Key: "local-model", Label: "local-model", Value: cfg.LocalModel, Editable: true},
		{Key: "ollama-url", Label: "ollama-url", Value: cfg.OllamaURL, Editable: true},
		{Key: "cloud-provider", Label: "cloud-provider", Value: cfg.CloudProvider, Editable: true},
		{Key: "cloud-model", Label: "cloud-model", Value: cfg.CloudModel, Editable: true},
		{Key: "cloud-base-url", Label: "cloud-base-url", Value: cfg.CloudBaseURL, Editable: true},
		{Key: "cloud-api-key", Label: "cloud-api-key", Value: apiKey, Editable: true, Masked: true},
		{Key: "embedding-model", Label: "embedding-model", Value: cfg.EmbeddingModel, ReadOnly: true, Hint: "(read-only)"},
		{Key: "cloud-state", Label: "cloud-state", Value: cfg.CloudState, ReadOnly: true, Hint: "(read-only)"},
		{Key: "port", Label: "port", Value: cfg.Port, ReadOnly: true, Hint: "(read-only)"},
	}
}

// saveSingle issues an UpdateConfig RPC with just the one field set.
func saveSingle(ag *agentclient.Client, wireKey, value string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var u agentclient.ConfigUpdate
	switch wireKey {
	case "local-runtime":
		u.LocalRuntime = value
	case "local-model":
		u.LocalModel = value
	case "ollama-url":
		u.OllamaURL = value
	case "cloud-provider":
		u.CloudProvider = value
	case "cloud-model":
		u.CloudModel = value
	case "cloud-api-key":
		u.CloudAPIKey = value
	case "cloud-base-url":
		u.CloudBaseURL = value
	default:
		return "", &editorError{Reason: "unsupported field: " + wireKey}
	}
	return ag.UpdateConfig(ctx, u)
}

type editorError struct{ Reason string }

func (e *editorError) Error() string { return e.Reason }
