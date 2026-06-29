package ui

import (
	"cercano/source/clients/cli/internal/form"
)

// cloudDraft is the in-progress profile edit backing the detail editor.
type cloudDraft struct {
	Name, Flavor, Backend, BaseURL, Model string
}

// selectCloudRow expands a list row's detail editor and seeds the draft from the
// matching configured profile (edit) or preset template (create).
func (sp *settingsPage) selectCloudRow(rowID string) {
	sp.cloudSelected = rowID
	sp.cloudDraft = cloudDraft{}
	sp.cloudDraftNew = true
	switch {
	case rowID == "other":
		sp.cloudDraft.Flavor = "chat_completions"
	case len(rowID) > 8 && rowID[:8] == "profile:":
		name := rowID[8:]
		for _, p := range sp.profiles {
			if p.Name == name {
				sp.cloudDraft = cloudDraft{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Model: p.Model}
				sp.cloudDraftNew = false
				return
			}
		}
	case len(rowID) > 9 && rowID[:9] == "template:":
		id := rowID[9:]
		for _, pr := range cloudPresets() {
			if pr.ID == id {
				sp.cloudDraft = cloudDraft{Name: pr.ID, Flavor: pr.Flavor, Backend: pr.Backend, BaseURL: pr.BaseURL}
				return
			}
		}
	}
}

// presetByTemplateID resolves the preset for a "template:<id>" row, or nil.
func presetByTemplateID(rowID string) *cloudPreset {
	if len(rowID) <= 9 || rowID[:9] != "template:" {
		return nil
	}
	id := rowID[9:]
	for i, pr := range cloudPresets() {
		if pr.ID == id {
			ps := cloudPresets()[i]
			return &ps
		}
	}
	return nil
}

// buildCloudSection renders the Cloud Providers list with an inline detail editor
// under the selected row.
func (sp *settingsPage) buildCloudSection() form.Section {
	rows := buildCloudRows(cloudPresets(), sp.profiles, sp.activeProfile)
	fields := make([]form.Field, 0, len(rows)+8)
	for _, r := range rows {
		fields = append(fields, form.NewRow("cloud-row:"+r.ID, r.Label, rowAnnotation(r), r.Active))
		if r.ID == sp.cloudSelected {
			fields = append(fields, sp.cloudDetailFields(r)...)
		}
	}
	return form.Section{Title: "Cloud Providers", Fields: fields}
}

// cloudDetailFields are the editor fields shown beneath the selected row.
func (sp *settingsPage) cloudDetailFields(r cloudRow) []form.Field {
	d := sp.cloudDraft
	var out []form.Field
	if sp.cloudDraftNew {
		out = append(out, form.NewText("cloud-name", "name", d.Name, "profile name"))
	} else {
		out = append(out, form.NewReadOnly("cloud-name", "name", d.Name, ""))
	}
	// flavor/backend: editable only for the custom "other" row; read-only otherwise.
	if r.ID == "other" {
		out = append(out,
			form.NewSelect("cloud-flavor", "flavor", []form.Option{
				{Label: "chat_completions", Value: "chat_completions"},
				{Label: "messages", Value: "messages"},
			}, d.Flavor),
			form.NewSelect("cloud-backend", "backend", []form.Option{
				{Label: "default", Value: ""},
				{Label: "openai", Value: "openai"},
				{Label: "gemini", Value: "gemini"},
				{Label: "groq", Value: "groq"},
			}, d.Backend),
		)
	} else {
		out = append(out, form.NewReadOnly("cloud-flavor", "flavor", d.Flavor, ""))
		if d.Flavor == "chat_completions" {
			be := d.Backend
			if be == "" {
				be = "default"
			}
			out = append(out, form.NewReadOnly("cloud-backend", "backend", be, ""))
		}
	}
	out = append(out,
		form.NewText("cloud-base-url", "base-url", d.BaseURL, "https://…"),
		form.NewText("cloud-model", "model", d.Model, "model id"),
		form.NewMasked("cloud-key", "api-key", sp.draftHasKey(r)),
		form.NewButton("cloud-save", "save", true),
		form.NewButton("cloud-activate", "activate", !r.ComingSoon),
	)
	if !sp.cloudDraftNew {
		out = append(out, form.NewButton("cloud-delete", "delete", true))
	}
	return out
}

// draftHasKey reports whether the row's profile already has a stored key (drives
// the masked field's "(stored)" vs "(not set)" hint).
func (sp *settingsPage) draftHasKey(r cloudRow) bool {
	return r.IsProfile && r.HasKey
}
