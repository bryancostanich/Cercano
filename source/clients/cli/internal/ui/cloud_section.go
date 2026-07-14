package ui

import (
	"context"
	"time"

	"cercano/source/clients/cli/internal/form"
)

// cloudDraft is the in-progress profile edit backing the detail editor.
type cloudDraft struct {
	Name, Flavor, Backend, Route, BaseURL, Model string
}

// selectCloudRow expands a list row's detail editor and seeds the draft from the
// matching configured profile (edit) or preset template (create). Also clears
// the cloud-model catalog cache so the next detail-fields build fetches a
// fresh catalog for THIS profile's endpoint rather than reusing a stale one
// from the previously-selected row.
func (sp *settingsPage) selectCloudRow(rowID string) {
	sp.cloudSelected = rowID
	sp.cloudDraft = cloudDraft{}
	sp.cloudDraftNew = true
	sp.cloudModels = nil
	sp.cloudModelsFetched = false
	switch {
	case rowID == "other":
		sp.cloudDraft.Flavor = "chat_completions"
	case len(rowID) > 8 && rowID[:8] == "profile:":
		name := rowID[8:]
		for _, p := range sp.profiles {
			if p.Name == name {
				sp.cloudDraft = cloudDraft{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, Route: p.Route, BaseURL: p.BaseURL, Model: p.Model}
				sp.cloudDraftNew = false
				return
			}
		}
	case len(rowID) > 9 && rowID[:9] == "template:":
		id := rowID[9:]
		for _, prov := range sp.cloudView.Providers {
			if prov.ID == id {
				sp.cloudDraft = cloudDraft{Name: prov.ID, Flavor: prov.Flavor, Backend: prov.Backend, BaseURL: prov.BaseURL}
				return
			}
		}
	}
}

// buildCloudSection renders the Cloud Providers list with an inline detail editor
// under the selected row.
func (sp *settingsPage) buildCloudSection() form.Section {
	rows := buildCloudRowsFromProviders(sp.cloudView)
	fields := make([]form.Field, 0, len(rows)+8)
	for _, r := range rows {
		fields = append(fields, form.NewRow("cloud-row:"+r.ID, r.Label, rowAnnotation(r), r.Active))
		if r.ID == sp.cloudSelected {
			fields = append(fields, sp.cloudDetailFields(r)...)
		}
	}
	return form.Section{Title: "Cloud Providers", Fields: fields}
}

// cloudDetailIndent prefixes detail-field labels so the editor reads as a set of
// sub-settings nested under the selected provider row, rather than aligning flush
// with the sibling provider rows below it.
const cloudDetailIndent = "  "

// cloudDetailFields are the editor fields shown beneath the selected row. Their
// labels are indented (cloudDetailIndent) to signal they belong to the row above.
func (sp *settingsPage) cloudDetailFields(r cloudRow) []form.Field {
	d := sp.cloudDraft
	il := func(s string) string { return cloudDetailIndent + s }
	var out []form.Field
	if sp.cloudDraftNew {
		out = append(out, form.NewText("cloud-name", il("name"), d.Name, "profile name"))
	} else {
		out = append(out, form.NewReadOnly("cloud-name", il("name"), d.Name, ""))
	}
	// flavor/backend: editable only for the custom "other" row; read-only otherwise.
	if r.ID == "other" {
		out = append(out,
			form.NewSelect("cloud-flavor", il("flavor"), []form.Option{
				{Label: "chat_completions", Value: "chat_completions"},
				{Label: "messages", Value: "messages"},
			}, d.Flavor),
			form.NewSelect("cloud-backend", il("backend"), []form.Option{
				{Label: "default", Value: ""},
				{Label: "openai", Value: "openai"},
				{Label: "gemini", Value: "gemini"},
				{Label: "groq", Value: "groq"},
			}, d.Backend),
		)
	} else {
		out = append(out, form.NewReadOnly("cloud-flavor", il("flavor"), d.Flavor, ""))
		if d.Flavor == "chat_completions" {
			be := d.Backend
			if be == "" {
				be = "default"
			}
			out = append(out, form.NewReadOnly("cloud-backend", il("backend"), be, ""))
		}
	}
	if d.Route == "subscription" {
		out = append(out, form.NewReadOnly("cloud-route", il("auth"), "subscription", "OAuth sign-in; API key/base URL not used"))
	} else {
		out = append(out, form.NewText("cloud-base-url", il("base-url"), d.BaseURL, "https://…"))
	}
	// Model field: anthropic-style profiles (flavor=messages) get a curated
	// Select populated from the profile's /v1/models catalog; other flavors
	// keep the free-form text input because we don't have a shared model-
	// catalog shape for them yet. Falls back to a static Claude model list
	// when the live fetch fails.
	if d.Flavor == "messages" {
		out = append(out, form.NewSelect("cloud-model", il("model"), sp.cloudModelOptions(r, d.Model), d.Model))
	} else {
		out = append(out, form.NewText("cloud-model", il("model"), d.Model, "model id"))
	}
	// ChatGPT subscription sign-in: the responses flavor can authenticate with
	// a ChatGPT Plus/Pro subscription via device-code OAuth instead of an API
	// key. Offer the button on responses rows; the api-key field stays as the
	// sanctioned fallback one row below.
	if r.Preset != nil && r.Preset.Flavor == "responses" {
		out = append(out, form.NewButton("cloud-signin", il("sign in with ChatGPT"), true))
	}
	// Claude subscription sign-in: only show the OAuth action for an existing
	// subscription profile, or for the bare anthropic template that creates one.
	// Direct API-key Anthropic profiles use the key field instead; offering both
	// auth paths on those rows makes it look like two separate profiles should be
	// signed into.
	if shouldShowClaudeSignIn(r, d) {
		out = append(out, form.NewButton("cloud-signin-claude", il("sign in with Claude (subscription)"), true))
	}
	if d.Route != "subscription" {
		out = append(out, form.NewMasked("cloud-key", il("api-key"), sp.draftHasKey(r)))
	}
	out = append(out,
		form.NewButton("cloud-save", il("save"), true),
		form.NewButton("cloud-activate", il("activate"), !r.ComingSoon),
	)
	if !sp.cloudDraftNew {
		// Backup toggle: the active profile can't be its own backup (the
		// server rejects it), so the set button is disabled on the active row.
		label := "set as backup"
		enabled := !r.Active && !r.ComingSoon
		if r.Profile != nil && r.Profile.Name == sp.cloudView.Backup {
			label, enabled = "clear backup", true
		}
		out = append(out, form.NewButton("cloud-backup", il(label), enabled))
		out = append(out, form.NewButton("cloud-delete", il("delete"), true))
	}
	return out
}

// shouldShowClaudeSignIn reports whether the selected row should expose the
// subscription OAuth action. Existing direct Anthropic API-key profiles should
// not show it; otherwise a config with both a subscription profile and a direct
// API-key profile appears to require signing in twice.
func shouldShowClaudeSignIn(r cloudRow, d cloudDraft) bool {
	if r.Preset == nil || r.Preset.Flavor != "messages" {
		return false
	}
	if d.Route == "subscription" {
		return true
	}
	return r.ID == "template:anthropic"
}

// draftHasKey reports whether the row's profile already has a stored key (drives
// the masked field's "(stored)" vs "(not set)" hint).
func (sp *settingsPage) draftHasKey(r cloudRow) bool {
	return r.IsProfile && r.HasKey
}

// cloudModelOptions returns the model Select options for the currently
// selected anthropic-style profile row. First fetches the live catalog from
// the profile's endpoint (through Meridian for meridian routes, direct API
// key otherwise); falls back to the curated static Claude list when the
// fetch fails (no Meridian running, wrong URL, timeout, etc.). Cached per
// row-selection so we don't re-fetch on every form re-render.
func (sp *settingsPage) cloudModelOptions(r cloudRow, currentID string) []form.Option {
	if !sp.cloudModelsFetched && r.IsProfile && sp.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		models, _, err := sp.agent.ListCloudProfileModels(ctx, sp.cloudDraft.Name)
		cancel()
		if err == nil && len(models) > 0 {
			sp.cloudModels = models
		}
		sp.cloudModelsFetched = true
	}
	models := sp.cloudModels
	if len(models) == 0 {
		models = fallbackClaudeModels()
	}
	return modelOptionsFromCatalog(models, currentID)
}
