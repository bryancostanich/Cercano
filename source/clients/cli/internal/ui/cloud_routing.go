package ui

import (
	"cercano/source/clients/cli/internal/form"
	tea "charm.land/bubbletea/v2"
	"context"
	"strings"
	"time"
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
func (sp *settingsPage) ensureRoutingDraft() {
	if sp.routingDraft != nil {
		return
	}
	sp.routingDraft = sp.cloudView.Assignments.Clone()
	if sp.cloudView.Assignments == nil {
		sp.routingDraft.Primary = sp.cloudView.Active
		sp.routingDraft.PrimaryBackup = sp.cloudView.Backup
	}
}
func (sp *settingsPage) cloudRoutingFields() []form.Field {
	sp.ensureRoutingDraft()
	a := sp.routingDraft
	profiles := []form.Option{{Label: "none", Value: ""}}
	for _, p := range sp.profiles {
		profiles = append(profiles, form.Option{Label: p.Name, Value: p.Name})
	}
	fields := []form.Field{
		form.NewSelect("cloud-routing-primary", "Primary", profiles, a.Primary), form.NewSelect("cloud-routing-primary-backup", "Primary backup", profiles, a.PrimaryBackup),
		form.NewSelect("cloud-routing-secondary", "Secondary", profiles, a.Secondary), form.NewSelect("cloud-routing-secondary-backup", "Secondary backup", profiles, a.SecondaryBackup),
	}
	for _, task := range []string{"chat", "dispatch"} {
		current := a.Tasks[task]
		defaultDest := "primary"
		if task == "dispatch" {
			defaultDest = "secondary"
		}
		dest := []form.Option{{Label: "inherit: " + defaultDest, Value: ""}, {Label: "Primary", Value: "primary"}, {Label: "Secondary", Value: "secondary"}, {Label: "Local", Value: "local"}}
		qualities := []form.Option{{Label: "inherit: Premium", Value: ""}, {Label: "Economy", Value: "economy"}, {Label: "Standard", Value: "standard"}, {Label: "Premium", Value: "premium"}}
		fields = append(fields, form.NewSelect("cloud-task-"+task+"-destination", task+" destination", dest, current.Destination), form.NewSelect("cloud-task-"+task+"-quality", task+" quality", qualities, current.Quality))
	}
	return append(fields, form.NewButton("cloud-routing-save", "Save routing", true), form.NewButton("cloud-routing-discard", "Discard routing", true))
}
func (sp *settingsPage) commitCloudRouting(field, value string) (string, tea.Cmd, error) {
	sp.ensureRoutingDraft()
	if field == "cloud-routing-discard" {
		sp.routingDraft = nil
		sp.routingDirty = false
		return "discarded routing draft", nil, nil
	}
	if field == "cloud-routing-save" {
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		warning, err := sp.agent.UpdateRoutingAssignments(ctx, sp.routingDraft)
		if err != nil {
			return "", nil, err
		}
		sp.routingDirty = false
		sp.profilesLoaded = false
		if warning != "" {
			return "saved routing; provider unavailable: " + warning, nil, nil
		}
		return "saved routing assignments", nil, nil
	}
	switch field {
	case "cloud-routing-primary":
		sp.routingDraft.Primary = value
	case "cloud-routing-primary-backup":
		sp.routingDraft.PrimaryBackup = value
	case "cloud-routing-secondary":
		sp.routingDraft.Secondary = value
	case "cloud-routing-secondary-backup":
		sp.routingDraft.SecondaryBackup = value
	default:
		parts := strings.Split(field, "-")
		if len(parts) != 4 || parts[1] != "task" {
			return "", nil, nil
		}
		task := parts[2]
		a := sp.routingDraft.Tasks[task]
		if parts[3] == "destination" {
			a.Destination = value
		} else {
			a.Quality = value
		}
		if a.Destination == "" && a.Quality == "" {
			delete(sp.routingDraft.Tasks, task)
		} else {
			sp.routingDraft.Tasks[task] = a
		}
	}
	sp.routingDirty = true
	return "routing draft changed; Save to apply", nil, nil
}
func (sp *settingsPage) cloudHasUnsaved() bool { return sp.cloudDirty || sp.routingDirty }
func (sp *settingsPage) discardCloudDrafts() {
	sp.selectCloudRow(sp.cloudSelected)
	sp.routingDraft = nil
	sp.routingDirty = false
	sp.cloudPendingLeave = ""
}
