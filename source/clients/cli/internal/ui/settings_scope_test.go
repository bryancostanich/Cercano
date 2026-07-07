package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func scopeTestThemes(t *testing.T) (*theme.Registry, theme.Theme) {
	t.Helper()
	themes := theme.NewRegistry(theme.BuiltinThemes())
	active, ok := themes.Get("cr4k3r_j4x")
	if !ok {
		t.Fatalf("builtin theme cr4k3r_j4x not found")
	}
	return themes, active
}

func sectionTitles(sp *settingsPage) []string {
	titles := make([]string, 0, len(sp.form.Sections))
	for _, s := range sp.form.Sections {
		titles = append(titles, s.Title)
	}
	return titles
}

// TestScopeUIRendersOnlyThemeSections proves the UI tab is theme-only and does
// not pull in General/Cloud sections — and that it builds with a nil agent
// (the theme tab must work offline, never issuing a GetConfig).
func TestScopeUIRendersOnlyThemeSections(t *testing.T) {
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 80, 40, themes, active, scopeUI)
	titles := sectionTitles(sp)
	if len(titles) == 0 {
		t.Fatal("UI scope produced no sections")
	}
	for _, title := range titles {
		if !strings.HasPrefix(title, "Theme") {
			t.Fatalf("UI scope leaked non-theme section %q", title)
		}
	}
	for _, banned := range []string{"Routing", "Permissions", "Cloud Providers", "Development Tools"} {
		for _, title := range titles {
			if title == banned {
				t.Fatalf("UI scope must not contain %q", banned)
			}
		}
	}
}

// TestScopeCloudRendersOnlyCloudSection proves the Cloud tab is the cloud
// editor alone, again with a nil agent (profiles fetch is agent-gated).
func TestScopeCloudRendersOnlyCloudSection(t *testing.T) {
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 80, 40, themes, active, scopeCloud)
	titles := sectionTitles(sp)
	if len(titles) != 1 || titles[0] != "Cloud Providers" {
		t.Fatalf("Cloud scope sections = %v, want [Cloud Providers]", titles)
	}
}
