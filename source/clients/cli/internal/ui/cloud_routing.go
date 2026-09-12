package ui

import (
	"cercano/source/clients/cli/internal/form"
	"strings"
)

func (sp *settingsPage) cloudChoiceFields(row cloudRow) []form.Field {
	choices := sp.cloudDraft.Choices.Clone()
	var fields []form.Field
	for _, quality := range []string{"economy", "standard", "premium"} {
		current := choices.TierOverrides[quality]
		inherited := sp.cloudDraft.Effective[quality]
		if inherited == "" {
			inherited = "recommendation unavailable"
		}
		label := "inherit: " + inherited
		if current != "" {
			label = "reset to inherited recommendation"
		}
		opts := append([]form.Option{{Label: label, Value: ""}}, sp.cloudModelOptions(row, current)...)
		fields = append(fields, form.NewSelect("cloud-quality-"+quality, "  "+quality, opts, current), form.NewText("cloud-custom-"+quality, "  custom "+quality+" ID", current, "empty restores inheritance"))
	}
	opts := append([]form.Option{{Label: "none", Value: ""}}, sp.cloudModelOptions(row, choices.ImageModel)...)
	fields = append(fields, form.NewSelect("cloud-image", "  image model", opts, choices.ImageModel), form.NewText("cloud-custom-image", "  custom image ID", choices.ImageModel, "requires confirmed image capability"))
	return fields
}
func (sp *settingsPage) applyCloudChoice(field, value string) bool {
	if field == "cloud-image" || field == "cloud-custom-image" {
		sp.cloudDraft.Choices = sp.cloudDraft.Choices.Clone()
		sp.cloudDraft.Choices.ImageModel = value
		return true
	}
	quality := strings.TrimPrefix(strings.TrimPrefix(field, "cloud-quality-"), "cloud-custom-")
	if quality != "economy" && quality != "standard" && quality != "premium" {
		return false
	}
	sp.cloudDraft.Choices = sp.cloudDraft.Choices.Clone()
	if value == "" {
		delete(sp.cloudDraft.Choices.TierOverrides, quality)
	} else {
		sp.cloudDraft.Choices.TierOverrides[quality] = value
	}
	return true
}
