package uiconfig

import (
	"os"
	"path/filepath"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestActiveThemeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CERCANO_UI_CONFIG", filepath.Join(dir, "ui.yaml"))
	if got := LoadActiveTheme(); got != "cracker" {
		t.Fatalf("default = %q, want cracker", got)
	}
	if err := SaveActiveTheme("phosphor"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := LoadActiveTheme(); got != "phosphor" {
		t.Fatalf("after save = %q, want phosphor", got)
	}
}

func TestCustomThemeSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CERCANO_THEMES_DIR", dir)
	mt := theme.Theme{Name: "mine", Palette: theme.Cracker()}
	if err := SaveCustomTheme(mt); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.yaml")); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	got := LoadCustomThemes()
	if len(got) != 1 || got[0].Name != "mine" {
		t.Fatalf("load = %v", got)
	}
	if err := DeleteCustomTheme("mine"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(LoadCustomThemes()) != 0 {
		t.Fatal("theme should be gone after delete")
	}
}

func TestImportThemeCopiesIntoThemesDir(t *testing.T) {
	src := t.TempDir()
	themesDir := t.TempDir()
	t.Setenv("CERCANO_THEMES_DIR", themesDir)

	// write a source theme yaml named "cool.yaml" outside the themes dir
	data, err := theme.MarshalTheme(theme.Theme{Name: "cool", Palette: theme.Cracker()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srcPath := filepath.Join(src, "cool.yaml")
	if err := os.WriteFile(srcPath, data, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	got, err := ImportTheme(srcPath)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got.Name != "cool" {
		t.Fatalf("imported name = %q, want cool", got.Name)
	}
	loaded := LoadCustomThemes()
	if len(loaded) != 1 || loaded[0].Name != "cool" {
		t.Fatalf("theme not copied into themes dir: %v", loaded)
	}
}
