package ui

import (
	"testing"

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
}
