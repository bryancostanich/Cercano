package ui

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/config"
)

func (sp *settingsPage) ensureRoutingDraft() {
	if sp.routingDraft != nil {
		return
	}
	sp.routingDraft = sp.cloudView.Assignments.Clone()
	if sp.cloudView.Assignments == nil {
		sp.routingDraft.Primary = sp.cloudView.Active
		if sp.cloudView.Backup != "" {
			sp.routingDraft.SetPrimaryBackupAccounts([]string{sp.cloudView.Backup})
		}
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
func destinationLabel(d config.Destination) string {
	switch d {
	case config.DestinationPrimary:
		return "Primary"
	case config.DestinationSecondary:
		return "Secondary"
	case config.DestinationLocal:
		return "Local"
	}
	return string(d)
}
func taskChoiceOptions(values []form.Option, current, defaultValue string) []form.Option {
	options := append([]form.Option(nil), values...)
	for i := range options {
		if options[i].Value == current && current != defaultValue {
			options[i].Label += " (overridden)"
		}
	}
	return options
}
func (sp *settingsPage) buildRoutingSections() []form.Section {
	sp.ensureRoutingDraft()
	a := sp.routingDraft
	profileOptions := func(empty string) []form.Option {
		options := []form.Option{{Label: empty, Value: ""}}
		seen := map[string]bool{"": true}
		for _, p := range sp.profiles {
			if !seen[p.Name] {
				options = append(options, form.Option{Label: sp.routingAccountLabel(p.Name), Value: p.Name})
				seen[p.Name] = true
			}
		}
		for _, name := range append([]string{a.Primary, a.Secondary, a.SecondaryBackup}, a.PrimaryBackupAccounts()...) {
			if !seen[name] {
				options = append(options, form.Option{Label: name + " (unavailable)", Value: name})
				seen[name] = true
			}
		}
		return options
	}
	primaryOptions := func(current string, primary bool) []form.Option {
		selected := map[string]bool{}
		for _, name := range a.PrimaryBackupAccounts() {
			selected[name] = true
		}
		if !primary {
			selected[a.Primary] = true
		}
		var out []form.Option
		for _, option := range profileOptions("Choose account…") {
			if option.Value == current || !selected[option.Value] {
				out = append(out, option)
			}
		}
		return out
	}
	primaryFields := []form.Field{form.NewSelect("routing-primary", "Account", primaryOptions(a.Primary, true), a.Primary)}
	backups := a.PrimaryBackupAccounts()
	for i, name := range backups {
		key := fmt.Sprintf("routing-primary-backup-%d", i)
		primaryFields = append(primaryFields,
			form.NewSelect(key, fmt.Sprintf("Backup %d", i+1), primaryOptions(name, false), name),
			form.NewButton(key+"-up", "  Move up", i > 0),
			form.NewButton(key+"-down", "  Move down", i+1 < len(backups)),
			form.NewButton(key+"-remove", "  Remove backup", true))
	}
	primaryFields = append(primaryFields, form.NewSelect("routing-primary-backup-add", "Add backup", primaryOptions("", false), ""))
	tiers := form.Section{Title: "Model tiers", Groups: []form.Group{
		{Title: "Primary", Fields: primaryFields},
		{Title: "Secondary", Fields: []form.Field{
			form.NewSelect("routing-secondary", "Profile", profileOptions("No profile selected"), a.Secondary),
			form.NewSelect("routing-secondary-backup", "Backup", profileOptions("No backup"), a.SecondaryBackup),
			form.NewSelect("routing-secondary-redirect", "Redirect all work to", []form.Option{{Label: "No redirect", Value: ""}, {Label: "Primary", Value: "primary"}, {Label: "Local", Value: "local"}}, a.SecondaryRedirect),
		}},
		{Title: "Local", Fields: []form.Field{
			form.NewReadOnly("routing-local-setup", "Setup", "Runtime and Local Models tabs", ""),
			form.NewSelect("routing-local-redirect", "Redirect all work to", []form.Option{{Label: "No redirect", Value: ""}, {Label: "Primary", Value: "primary"}, {Label: "Secondary", Value: "secondary"}}, a.LocalRedirect),
		}},
		{Fields: []form.Field{form.NewReadOnly("routing-behavior", "Behavior", "Primary cycles through backups on quota exhaustion and stays on the selected account. Redirects send all work to another model tier.", "")}},
	}}
	c := sp.routingConfig()
	var tasks []form.Field
	for _, d := range config.TaskDefinitions() {
		task := string(d.Task)
		assignment := c.TaskAssignment(d.Task)
		destination, quality := string(assignment.Destination), string(assignment.Quality)
		destinations := taskChoiceOptions([]form.Option{{Label: "Primary", Value: "primary"}, {Label: "Secondary", Value: "secondary"}, {Label: "Local", Value: "local"}}, destination, string(d.Default.Destination))
		qualities := taskChoiceOptions([]form.Option{{Label: "Light", Value: "economy"}, {Label: "Standard", Value: "standard"}, {Label: "Premium", Value: "premium"}}, quality, string(d.Default.Quality))
		note := ""
		final, err := c.ResolveDestination(assignment.Destination)
		if err != nil {
			note = "Invalid redirect: " + err.Error()
		} else if final != assignment.Destination {
			note = "Redirected to " + destinationLabel(final)
		}
		modified := assignment != d.Default
		row := form.NewSelectPair("routing-task-"+task, d.Label,
			form.NewSelect("routing-task-"+task+"-destination", "Model tier", destinations, destination),
			form.NewSelect("routing-task-"+task+"-quality", "Quality", qualities, quality), modified, note)
		if modified {
			row.Help = "Defaults: " + destinationLabel(d.Default.Destination) + " / " + qualityLabel(d.Default.Quality)
		}
		tasks = append(tasks, row)
	}
	status := "No unsaved changes"
	if sp.routingDirty {
		status = "Unsaved changes"
	}
	tiers.Groups = append(tiers.Groups, form.Group{Title: status, Fields: []form.Field{form.NewButton("routing-save-tiers", "Save routing", sp.routingDirty)}})
	routing := form.Section{Title: "Task routing", ColumnHeadings: [3]string{"Task", "Model tier", "Quality"}, Groups: []form.Group{
		{Fields: tasks},
		{Title: status, Fields: []form.Field{form.NewButton("routing-save", "Save routing", sp.routingDirty), form.NewButton("routing-discard", "Discard routing", sp.routingDirty)}},
	}}
	return []form.Section{tiers, routing}
}
func (sp *settingsPage) commitRouting(field, value string) (string, tea.Cmd, error) {
	sp.ensureRoutingDraft()
	switch field {
	case "routing-discard":
		sp.routingDraft = nil
		sp.routingDirty = false
		return "discarded routing draft", nil, nil
	case "routing-save", "routing-save-tiers":
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
		for _, name := range sp.routingDraft.PrimaryBackupAccounts() {
			if value != "" && name == value {
				return "", nil, fmt.Errorf("account is already a backup")
			}
		}
		sp.routingDraft.Primary = value
	case "routing-primary-backup":
		if value == "" {
			sp.routingDraft.SetPrimaryBackupAccounts(nil)
		} else {
			sp.routingDraft.SetPrimaryBackupAccounts([]string{value})
		}
	case "routing-secondary":
		sp.routingDraft.Secondary = value
	case "routing-secondary-backup":
		sp.routingDraft.SecondaryBackup = value
	case "routing-secondary-redirect":
		sp.routingDraft.SecondaryRedirect = value
	case "routing-local-redirect":
		sp.routingDraft.LocalRedirect = value
	default:
		if strings.HasPrefix(field, "routing-primary-backup-") {
			if err := sp.editPrimaryBackup(strings.TrimPrefix(field, "routing-primary-backup-"), value); err != nil {
				return "", nil, err
			}
			break
		}
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
		defaults := (config.Config{}).TaskAssignment(config.Task(task))
		switch part {
		case "destination":
			if value == string(defaults.Destination) {
				value = ""
			}
			a.Destination = value
		case "quality":
			if value == string(defaults.Quality) {
				value = ""
			}
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
	saved := sp.cloudView.Assignments.Clone()
	if sp.cloudView.Assignments == nil {
		saved.Primary = sp.cloudView.Active
		if sp.cloudView.Backup != "" {
			saved.SetPrimaryBackupAccounts([]string{sp.cloudView.Backup})
		}
	}
	sp.routingDirty = !reflect.DeepEqual(sp.routingDraft, saved)
	if !sp.routingDirty {
		return "routing matches saved settings", nil, nil
	}
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

func (sp *settingsPage) routingAccountLabel(name string) string {
	for _, provider := range sp.cloudView.Providers {
		for _, p := range provider.Profiles {
			if p.Name == name {
				return provider.Label + " — " + name
			}
		}
	}
	for _, p := range sp.profiles {
		if p.Name == name {
			if p.Provider != "" {
				return p.Provider + " — " + name
			}
			if p.Flavor != "" {
				return p.Flavor + " — " + name
			}
		}
	}
	return name
}

func (sp *settingsPage) editPrimaryBackup(action, value string) error {
	a := sp.routingDraft
	backups := a.PrimaryBackupAccounts()
	parts := strings.Split(action, "-")
	index := len(backups)
	if action != "add" {
		var err error
		index, err = strconv.Atoi(parts[0])
		if err != nil || index < 0 || index >= len(backups) {
			return fmt.Errorf("invalid backup position")
		}
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "up":
			if index == 0 {
				return nil
			}
			backups[index-1], backups[index] = backups[index], backups[index-1]
		case "down":
			if index+1 == len(backups) {
				return nil
			}
			backups[index+1], backups[index] = backups[index], backups[index+1]
		case "remove":
			backups = append(backups[:index], backups[index+1:]...)
		default:
			return fmt.Errorf("unknown backup action")
		}
	} else {
		if value == "" {
			if action == "add" {
				return nil
			}
			backups = append(backups[:index], backups[index+1:]...)
		} else {
			if value == a.Primary {
				return fmt.Errorf("Primary cannot also be a backup")
			}
			for i, name := range backups {
				if name == value && i != index {
					return fmt.Errorf("account is already a backup")
				}
			}
			exists := false
			for _, p := range sp.profiles {
				if p.Name == value {
					exists = true
				}
			}
			if !exists {
				return fmt.Errorf("account %q is not configured", value)
			}
			if action == "add" {
				backups = append(backups, value)
			} else {
				backups[index] = value
			}
		}
	}
	a.SetPrimaryBackupAccounts(backups)
	return nil
}
