package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
)

func TestBuildThemeSections(t *testing.T) {
	secs := buildThemeSections(theme.Theme{Name: "cracker", Palette: theme.Cracker()},
		[]string{"cracker", "phosphor"}, true /*builtin*/, false /*dirty*/)
	titles := map[string]bool{}
	keys := map[string]bool{}
	for _, s := range secs {
		titles[s.Title] = true
		for _, f := range s.Fields {
			keys[f.Key()] = true
		}
	}
	for _, want := range []string{"Theme", "Theme · Chrome", "Theme · Content", "Theme · Actions"} {
		if !titles[want] {
			t.Errorf("missing section %q", want)
		}
	}
	for _, want := range []string{"theme-select", "color:accent", "color:buffer_code", "theme-save", "theme-save-as", "theme-delete", "theme-import"} {
		if !keys[want] {
			t.Errorf("missing field %q", want)
		}
	}

	// Assert that color fields are NOT editable for built-in themes: pressing
	// Enter must not flip the field into editing mode.
	var accentField form.Field
outer:
	for _, s := range secs {
		for _, f := range s.Fields {
			if f.Key() == "color:accent" {
				accentField = f
				break outer
			}
		}
	}
	if accentField == nil {
		t.Fatal("color:accent field not found in sections")
	}
	accentField.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if accentField.Editing() {
		t.Error("color:accent must not enter edit mode for a built-in theme (read-only field)")
	}
}
