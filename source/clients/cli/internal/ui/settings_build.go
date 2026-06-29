package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
)

// accentColorOptions lists the palette tokens accepted by Model.resolvePromptColor.
// Value tokens use the "palette:<key>" shape; the hex escape hatch stays on /color.
func accentColorOptions() []form.Option {
	return []form.Option{
		{Label: "accent (lime)", Value: "palette:accent"},
		{Label: "primary (amber)", Value: "palette:primary"},
		{Label: "info (cyan)", Value: "palette:info"},
		{Label: "bright", Value: "palette:bright"},
		{Label: "muted", Value: "palette:muted"},
		{Label: "border", Value: "palette:border"},
	}
}

func buildSettingsSections(cfg *agentclient.Config, mode, accentToken string) []form.Section {
	return []form.Section{
		{Title: "Local Model", Fields: []form.Field{
			form.NewSelect("local-runtime", "local-runtime", []form.Option{
				{Label: "ollama", Value: "ollama"}, {Label: "llama_server", Value: "llama_server"},
			}, cfg.LocalRuntime),
			form.NewText("local-model", "local-model", cfg.LocalModel, ""),
			form.NewText("ollama-url", "ollama-url", cfg.OllamaURL, ""),
			form.NewReadOnly("embedding-model", "embedding-model", cfg.EmbeddingModel, "(read-only)"),
		}},
		{Title: "Routing", Fields: []form.Field{
			form.NewSelect("locus-mode", "locus-mode", []form.Option{
				{Label: "cloud_only", Value: "cloud_only"},
				{Label: "cloud_primary", Value: "cloud_primary"},
				{Label: "local_primary", Value: "local_primary"},
				{Label: "local_only", Value: "local_only"},
			}, cfg.LocusMode),
		}},
		{Title: "Permissions", Fields: []form.Field{
			form.NewSelect("permission-mode", "permission-mode", []form.Option{
				{Label: "strict", Value: "strict"},
				{Label: "permissive", Value: "permissive"},
				{Label: "bypass", Value: "bypass"},
			}, mode),
		}},
		{Title: "Server", Fields: []form.Field{
			form.NewReadOnly("port", "port", cfg.Port, "(read-only)"),
		}},
	}
}

type commitKind int

const (
	commitNoop commitKind = iota
	commitConfig
	commitPermission
	commitColor
)

type commitAction struct {
	kind   commitKind
	update agentclient.ConfigUpdate
	value  string
}

// classifyCommit maps a committed (key,value) to the sink that should apply it.
func classifyCommit(key, value string) commitAction {
	var u agentclient.ConfigUpdate
	switch key {
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
	case "cloud-base-url":
		u.CloudBaseURL = value
	case "cloud-api-key":
		u.CloudAPIKey = value
	case "locus-mode":
		u.LocusMode = value
	case "permission-mode":
		return commitAction{kind: commitPermission, value: value}
	case "accent-color":
		return commitAction{kind: commitColor, value: value}
	default:
		return commitAction{kind: commitNoop}
	}
	return commitAction{kind: commitConfig, update: u}
}
