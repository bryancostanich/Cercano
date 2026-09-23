package ui

import (
	"cercano/source/server/pkg/agentclient"
	"context"
	"fmt"
	"time"

	"cercano/source/clients/cli/internal/form"
)

// cloudDraft is the in-progress profile edit backing the detail editor.
type cloudDraft struct {
	// Pending credentials are memory-only and never included in profile metadata.
	apiKey                                       string
	apiKeyEdited                                 bool
	Name, Flavor, Backend, Route, BaseURL, Model string
	Choices                                      *agentclient.CloudModelChoices
	Effective                                    map[string]string
	Provider, Region, AWSProfile                 string
}

// selectCloudRow expands a list row's detail editor and seeds the draft from the
// matching configured profile (edit) or preset template (create). Also clears
// the cloud-model catalog cache so the next detail-fields build fetches a
// fresh catalog for THIS profile's endpoint rather than reusing a stale one
// from the previously-selected row.
func (sp *settingsPage) selectCloudRow(rowID string) {
	sp.cloudSelected = rowID
	sp.cloudDirty = false
	sp.cloudDraft = cloudDraft{Choices: (&agentclient.CloudModelChoices{}).Clone()}
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
				sp.cloudDraft = cloudDraft{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, Route: p.Route, BaseURL: p.BaseURL, Model: p.Model, Choices: p.Choices.Clone(), Effective: p.RecommendedQualityModels, Provider: p.Provider, Region: p.Region, AWSProfile: p.AWSProfile}
				sp.cloudDraftNew = false
				return
			}
		}
	case len(rowID) > 9 && rowID[:9] == "template:":
		id := rowID[9:]
		for _, prov := range sp.cloudView.Providers {
			if prov.ID == id {
				sp.cloudDraft = cloudDraft{Name: prov.ID, Flavor: prov.Flavor, Backend: prov.Backend, Route: prov.Route, BaseURL: prov.BaseURL, Choices: (&agentclient.CloudModelChoices{}).Clone()}
				return
			}
		}
	}
}

// buildCloudSection renders the Cloud Providers list with an inline detail editor
// under the selected row.
func (sp *settingsPage) buildCloudSection() form.Section {
	// Save keeps the draft selected while the profile snapshot reloads. Refresh
	// derived recommendations without reselecting (and discarding draft edits).
	if !sp.cloudDraftNew {
		for _, p := range sp.profiles {
			if sp.cloudSelected == "profile:"+p.Name && sp.cloudDraft.Name == p.Name {
				sp.cloudDraft.Effective = p.RecommendedQualityModels
				break
			}
		}
	}
	rows := buildCloudRowsFromProviders(sp.cloudView)
	var fields []form.Field
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
	out = append(out, sp.cloudChoiceFields(r)...)
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
	if sp.shouldShowClaudeSignIn(r, d) {
		out = append(out, form.NewButton("cloud-signin-claude", il("sign in with Claude (subscription)"), true))
	}
	if d.Route != "subscription" {
		out = append(out, form.NewMasked("cloud-key", il("api-key"), sp.draftHasKey(r)))
	}
	out = append(out, form.NewButton("cloud-save", il("save"), true), form.NewButton("cloud-discard", il("discard"), true))
	if !sp.cloudDraftNew {
		out = append(out, form.NewButton("cloud-add-account", il("Add another account"), true), form.NewButton("cloud-delete", il("delete"), true))
	}
	return out
}

// Every named subscription account owns its sign-in and refresh credentials.
func (sp *settingsPage) shouldShowClaudeSignIn(r cloudRow, d cloudDraft) bool {
	return (d.Flavor == "messages" || (r.Preset != nil && r.Preset.Flavor == "messages")) && d.Route == "subscription"
}

func (sp *settingsPage) cloudAccountNameExists(name string) bool {
	for _, p := range sp.profiles {
		if p.Name == name {
			return true
		}
	}
	for _, provider := range sp.cloudView.Providers {
		for _, p := range provider.Profiles {
			if p.Name == name {
				return true
			}
		}
	}
	for _, p := range sp.cloudView.CustomProfiles {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (sp *settingsPage) addCloudAccount() {
	old := sp.cloudDraft
	base := old.Name
	name := base
	for i := 2; sp.cloudAccountNameExists(name) || name == base; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	// Copy connection structure, never credentials or model overrides.
	sp.cloudDraft = cloudDraft{Name: name, Flavor: old.Flavor, Backend: old.Backend, Route: old.Route, BaseURL: old.BaseURL, Provider: old.Provider, Region: old.Region, AWSProfile: old.AWSProfile, Choices: (&agentclient.CloudModelChoices{}).Clone()}
	sp.cloudDraftNew = true
	sp.cloudDirty = true
	sp.cloudModels = nil
	sp.cloudModelsFetched = false
}

// draftHasKey reports whether the row's profile already has a stored key (drives
// the masked field's "(stored)" vs "(not set)" hint).
func (sp *settingsPage) draftHasKey(r cloudRow) bool {
	if sp.cloudDraft.apiKeyEdited {
		return sp.cloudDraft.apiKey != ""
	}
	return !sp.cloudDraftNew && r.IsProfile && r.HasKey
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
	if len(models) == 0 && sp.cloudDraft.Flavor == "messages" {
		models = fallbackClaudeModels()
	}
	return modelOptionsFromCatalog(models, currentID)
}

// A successful login commits account identity, not unrelated model/key edits.
// Keep those edits pending while making the newly created account reselectable.
func (sp *settingsPage) cloudAccountSignedIn(name, route string) {
	sp.profilesLoaded = false
	if sp.cloudDraft.Name != name {
		return
	}
	if sp.cloudDraftNew {
		sp.cloudDirty = sp.cloudDraft.apiKeyEdited || (sp.cloudDraft.Choices != nil && (len(sp.cloudDraft.Choices.TierOverrides) > 0 || sp.cloudDraft.Choices.ImageModel != ""))
	}
	sp.cloudDraftNew = false
	sp.cloudSelected = "profile:" + name
	sp.cloudDraft.Route = route
}
