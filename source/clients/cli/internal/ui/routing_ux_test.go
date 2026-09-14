package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/config"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestRoutingUXNoUnsetHeading(t *testing.T) {
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 100, 40, themes, active, scopeRouting)
	view := form.New(sp.snapshotSections()).View(100, active.Palette, theme.NewStyles(active.Palette))
	for _, bad := range []string{"(unset)", "inherit:", "effective", "primary → primary"} {
		if strings.Contains(view, bad) {
			t.Fatalf("routing leaks %q", bad)
		}
	}
	if !strings.Contains(view, "Model tiers") || !strings.Contains(view, "Task routing") {
		t.Fatal("routing concepts not separated")
	}
}

func routingUXPage(t *testing.T) *settingsPage {
	t.Helper()
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 100, 40, themes, active, scopeRouting)
	sp.ensureRoutingDraft()
	return sp
}
func routingUXRow(t *testing.T, sp *settingsPage, task config.Task) *form.SelectPairField {
	t.Helper()
	for _, sec := range sp.buildRoutingSections() {
		for _, group := range sec.Groups {
			for _, f := range group.Fields {
				if row, ok := f.(*form.SelectPairField); ok && row.Left.Key() == "routing-task-"+string(task)+"-destination" {
					return row
				}
			}
		}
	}
	t.Fatalf("missing %s", task)
	return nil
}
func TestRoutingUXPartialOverridesAndDefaultSelection(t *testing.T) {
	sp := routingUXPage(t)
	task := config.TaskReview
	defaults := (config.Config{}).TaskAssignment(task)
	row := routingUXRow(t, sp, task)
	if row.Resettable || row.Note != "" || row.Left.Display() != destinationLabel(defaults.Destination) || row.Right.Display() != qualityLabel(defaults.Quality) {
		t.Fatalf("defaults not resolved: %q %q", row.Left.Display(), row.Right.Display())
	}
	dest := "primary"
	if defaults.Destination == config.DestinationPrimary {
		dest = "local"
	}
	quality := "premium"
	if defaults.Quality == config.CostPremium {
		quality = "economy"
	}
	sp.commitRouting("routing-task-review-destination", dest)
	row = routingUXRow(t, sp, task)
	if !strings.HasSuffix(row.Left.Display(), " (overridden)") || strings.Contains(row.Right.Display(), "overridden") {
		t.Fatal("partial override annotation wrong")
	}
	if !row.Resettable {
		t.Fatal("modified task cannot restore defaults")
	}
	sp.commitRouting("routing-task-review-quality", quality)
	sp.commitRouting("routing-task-review-destination", string(defaults.Destination))
	a := sp.routingDraft.Tasks["review"]
	if a.Destination != "" || a.Quality != quality {
		t.Fatalf("default selection did not remove only destination override: %+v", a)
	}
	row = routingUXRow(t, sp, task)
	if strings.Contains(row.Left.Display(), "overridden") || !strings.Contains(row.Right.Display(), "overridden") {
		t.Fatal("component annotations not independent")
	}
	sp.commitRouting("routing-task-review-quality", string(defaults.Quality))
	if _, ok := sp.routingDraft.Tasks["review"]; ok {
		t.Fatal("redundant override not removed")
	}
	if routingUXRow(t, sp, task).Resettable {
		t.Fatal("default task marked overridden")
	}
}
func TestRoutingUXRedirectNotesAreConditional(t *testing.T) {
	sp := routingUXPage(t)
	if routingUXRow(t, sp, config.TaskDispatch).Note != "" {
		t.Fatal("redundant effective note")
	}
	sp.commitRouting("routing-secondary-redirect", "primary")
	row := routingUXRow(t, sp, config.TaskDispatch)
	if row.Note != "Redirected to Primary" || row.Left.Display() != "Secondary" {
		t.Fatalf("redirect confused assignment: %q %q", row.Note, row.Left.Display())
	}
	if routingUXRow(t, sp, config.TaskChat).Note != "" {
		t.Fatal("unaffected task has redirect note")
	}
	sp.commitRouting("routing-local-redirect", "secondary")
	if routingUXRow(t, sp, config.TaskReconnaissance).Note != "Redirected to Primary" {
		t.Fatal("redirect chain not resolved")
	}
	sp.commitRouting("routing-secondary-redirect", "")
	if routingUXRow(t, sp, config.TaskDispatch).Note != "" {
		t.Fatal("redirect note survived clear")
	}
}
func TestRoutingUXOverridesAreNotUnsavedChanges(t *testing.T) {
	sp := routingUXPage(t)
	a := agentclient.RoutingAssignments{Tasks: map[string]agentclient.TaskAssignment{"review": {Destination: "local"}}}
	sp.cloudView.Assignments = a.Clone()
	sp.routingDraft = a.Clone()
	sp.routingDirty = false
	row := routingUXRow(t, sp, config.TaskReview)
	if !strings.Contains(row.Left.Display(), "overridden") {
		t.Fatal("saved override not labeled")
	}
	sections := sp.buildRoutingSections()
	if sections[1].Groups[1].Title != "No unsaved changes" {
		t.Fatal("saved overrides marked unsaved")
	}
	sp.commitRouting("routing-primary", "example")
	if sp.buildRoutingSections()[1].Groups[1].Title != "Unsaved changes" {
		t.Fatal("pending profile change not marked")
	}
	sp.commitRouting("routing-discard", "")
	sp.ensureRoutingDraft()
	if sp.routingDirty || sp.routingDraft.Primary != "" || sp.routingDraft.Tasks["review"].Destination != "local" {
		t.Fatal("discard lost saved override")
	}
	sp.commitRouting("routing-task-review-reset", "")
	if _, ok := sp.routingDraft.Tasks["review"]; ok {
		t.Fatal("restore task defaults failed")
	}
}
func TestRoutingUXRenderingWidthsAndSpecificEmptyLabels(t *testing.T) {
	sp := routingUXPage(t)
	_, active := scopeTestThemes(t)
	styles := theme.NewStyles(active.Palette)
	sp.commitRouting("routing-task-review-destination", "local")
	sp.commitRouting("routing-task-review-quality", "economy")
	for _, width := range []int{48, 80, 110} {
		view := ansi.Strip(form.New(sp.buildRoutingSections()).View(width, active.Palette, styles))
		for _, bad := range []string{"(unset)", "inherit:", "effective"} {
			if strings.Contains(view, bad) {
				t.Fatalf("width %d leaks %s", width, bad)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
		if width >= 80 {
			for _, label := range []string{"No profile selected", "No backup", "No redirect"} {
				if !strings.Contains(view, label) {
					t.Fatalf("missing empty label %s", label)
				}
			}
		}
		if width >= 80 {
			found := false
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, "Review") && strings.Contains(line, "Local (overridden)") && strings.Contains(line, "Light (overridden)") {
					found = true
				}
			}
			if !found {
				t.Fatalf("task values not on one line at width %d: %s", width, view)
			}
		}
	}
}

func TestRoutingUXUnchangedValuesAreNotUnsaved(t *testing.T) {
	sp := routingUXPage(t)
	defaults := (config.Config{}).TaskAssignment(config.TaskReview)
	sp.commitRouting("routing-task-review-quality", string(defaults.Quality))
	if sp.routingDirty {
		t.Fatal("selecting existing default marked unsaved")
	}
	sp.commitRouting("routing-task-review-destination", "local")
	if !sp.routingDirty {
		t.Fatal("changed assignment not marked unsaved")
	}
	sp.commitRouting("routing-task-review-reset", "")
	if sp.routingDirty {
		t.Fatal("restoring saved defaults still marked unsaved")
	}
}
