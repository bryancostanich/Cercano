package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/config"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"strings"
	"time"
)

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
func qualityLabel(q config.CostTier) string {
	if q == config.CostEconomy {
		return "Light"
	}
	if q == config.CostStandard {
		return "Standard"
	}
	return "Premium"
}
func (sp *settingsPage) routingConfig() config.Config {
	sp.ensureRoutingDraft()
	a := sp.routingDraft
	c := config.Config{SecondaryRedirect: config.Destination(a.SecondaryRedirect), LocalRedirect: config.Destination(a.LocalRedirect), TaskAssignments: map[config.Task]config.TaskAssignment{}}
	for k, v := range a.Tasks {
		c.TaskAssignments[config.Task(k)] = config.TaskAssignment{Destination: config.Destination(v.Destination), Quality: config.CostTier(v.Quality)}
	}
	return c
}
func (sp *settingsPage) buildRoutingSection() form.Section {
	sp.ensureRoutingDraft()
	a := sp.routingDraft
	profiles := []form.Option{{Label: "none", Value: ""}}
	seen := map[string]bool{"": true}
	for _, p := range sp.profiles {
		profiles = append(profiles, form.Option{Label: p.Name, Value: p.Name})
		seen[p.Name] = true
	}
	for _, name := range []string{a.Primary, a.PrimaryBackup, a.Secondary, a.SecondaryBackup} {
		if !seen[name] {
			profiles = append(profiles, form.Option{Label: name + " (unavailable)", Value: name})
			seen[name] = true
		}
	}
	fields := []form.Field{
		form.NewSelect("routing-primary", "Primary", profiles, a.Primary), form.NewSelect("routing-primary-backup", "Primary backup", profiles, a.PrimaryBackup),
		form.NewSelect("routing-secondary", "Secondary", profiles, a.Secondary), form.NewSelect("routing-secondary-backup", "Secondary backup", profiles, a.SecondaryBackup),
		form.NewSelect("routing-secondary-redirect", "Secondary routing", []form.Option{{Label: "own configuration", Value: ""}, {Label: "redirect to Primary", Value: "primary"}, {Label: "redirect to Local", Value: "local"}}, a.SecondaryRedirect),
		form.NewSelect("routing-local-redirect", "Local routing", []form.Option{{Label: "own configuration", Value: ""}, {Label: "redirect to Primary", Value: "primary"}, {Label: "redirect to Secondary", Value: "secondary"}}, a.LocalRedirect),
		form.NewReadOnly("routing-local-setup", "Local setup", "Manage runtime in Runtime; models in Local Models", "No cloud profile binding"),
	}
	c := sp.routingConfig()
	for _, d := range config.TaskDefinitions() {
		task := string(d.Task)
		current := a.Tasks[task]
		effective := c.TaskAssignment(d.Task)
		final, err := c.ResolveDestination(effective.Destination)
		placement := string(effective.Destination) + " → " + string(final)
		if err != nil {
			placement = "invalid redirect: " + err.Error()
		}
		dest := []form.Option{{Label: "inherit: " + string(d.Default.Destination), Value: ""}, {Label: "Primary", Value: "primary"}, {Label: "Secondary", Value: "secondary"}, {Label: "Local", Value: "local"}}
		qualities := []form.Option{{Label: "inherit: " + qualityLabel(d.Default.Quality), Value: ""}, {Label: "Light", Value: "economy"}, {Label: "Standard", Value: "standard"}, {Label: "Premium", Value: "premium"}}
		fields = append(fields, form.NewReadOnly("routing-task-"+task+"-heading", d.Label, "", ""), form.NewSelect("routing-task-"+task+"-destination", "  destination", dest, current.Destination), form.NewSelect("routing-task-"+task+"-quality", "  quality", qualities, current.Quality), form.NewReadOnly("routing-task-"+task+"-effective", "  effective", placement+" / "+qualityLabel(effective.Quality), "Saved assignment is preserved through redirects"), form.NewButton("routing-task-"+task+"-reset", "  reset task", true))
	}
	fields = append(fields, form.NewButton("routing-save", "Save routing", true), form.NewButton("routing-discard", "Discard routing", true))
	return form.Section{Title: "Routing", Fields: fields}
}
func (sp *settingsPage) commitRouting(field, value string) (string, tea.Cmd, error) {
	sp.ensureRoutingDraft()
	switch field {
	case "routing-discard":
		sp.routingDraft = nil
		sp.routingDirty = false
		return "discarded routing draft", nil, nil
	case "routing-save":
		if err := sp.routingConfig().ValidateDestinationRedirects(); err != nil {
			return "", nil, err
		}
		if sp.agent == nil {
			return "", nil, fmt.Errorf("agent reconnecting — retry in a moment")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		warning, err := sp.agent.UpdateRoutingAssignments(ctx, sp.routingDraft.Clone())
		return sp.finishRoutingSave(warning, err)
	case "routing-primary":
		sp.routingDraft.Primary = value
	case "routing-primary-backup":
		sp.routingDraft.PrimaryBackup = value
	case "routing-secondary":
		sp.routingDraft.Secondary = value
	case "routing-secondary-backup":
		sp.routingDraft.SecondaryBackup = value
	case "routing-secondary-redirect":
		sp.routingDraft.SecondaryRedirect = value
	case "routing-local-redirect":
		sp.routingDraft.LocalRedirect = value
	default:
		key := strings.TrimPrefix(field, "routing-task-")
		cut := strings.LastIndex(key, "-")
		if cut < 0 {
			return "", nil, fmt.Errorf("unknown routing field %q", field)
		}
		task, part := key[:cut], key[cut+1:]
		if !config.ValidTask(config.Task(task)) {
			return "", nil, fmt.Errorf("unknown task %q", task)
		}
		a := sp.routingDraft.Tasks[task]
		switch part {
		case "destination":
			a.Destination = value
		case "quality":
			a.Quality = value
		case "reset":
			a.Destination = ""
			a.Quality = ""
		default:
			return "", nil, fmt.Errorf("unknown routing field %q", field)
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
func (sp *settingsPage) finishRoutingSave(warning string, err error) (string, tea.Cmd, error) {
	sp.profilesLoaded = false
	if err != nil {
		return "", nil, err
	}
	sp.cloudView.Assignments = sp.routingDraft.Clone()
	sp.routingDirty = false
	if warning != "" {
		return "saved routing; provider unavailable: " + warning, nil, nil
	}
	return "saved routing assignments", nil, nil
}
func (sp *settingsPage) hasUnsavedSettings() bool {
	switch sp.scope {
	case scopeRouting:
		return sp.routingDirty
	case scopeCloud:
		return sp.cloudDirty
	default:
		return sp.cloudDirty || sp.routingDirty
	}
}
func (sp *settingsPage) discardSettingsDrafts() {
	if sp.scope != scopeRouting {
		sp.selectCloudRow(sp.cloudSelected)
	}
	if sp.scope != scopeCloud {
		sp.routingDraft = nil
		sp.routingDirty = false
	}
	sp.settingsPendingLeave = ""
}
