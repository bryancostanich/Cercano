// Package uiconfig persists CLI-only UI preferences: the active theme name
// (ui.yaml) and custom theme files (themes/<name>.yaml).
package uiconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"cercano/source/clients/cli/internal/theme"
)

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cercano")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cercano")
}

// ConfigPath resolves the ui.yaml path.
func ConfigPath() string {
	if p := os.Getenv("CERCANO_UI_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "ui.yaml")
}

// ThemesDir resolves the custom-themes directory.
func ThemesDir() string {
	if p := os.Getenv("CERCANO_THEMES_DIR"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "themes")
}

type uiFile struct {
	Theme string `yaml:"theme"`
}

// LoadActiveTheme returns the persisted active theme name, or "cr4k3r_j4x".
func LoadActiveTheme() string {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return "cr4k3r_j4x"
	}
	var f uiFile
	if yaml.Unmarshal(data, &f) != nil || f.Theme == "" {
		return "cr4k3r_j4x"
	}
	return f.Theme
}

// SaveActiveTheme persists the active theme name.
func SaveActiveTheme(name string) error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(uiFile{Theme: name})
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0o644)
}

// LoadCustomThemes parses every *.yaml in ThemesDir, skipping invalid files.
func LoadCustomThemes() []theme.Theme {
	entries, err := os.ReadDir(ThemesDir())
	if err != nil {
		return nil
	}
	var out []theme.Theme
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ThemesDir(), e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		t, err := theme.UnmarshalTheme(name, data)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func themePath(name string) string { return filepath.Join(ThemesDir(), name+".yaml") }

// SaveCustomTheme writes a theme to ThemesDir/<name>.yaml.
func SaveCustomTheme(t theme.Theme) error {
	if t.Name == "" {
		return fmt.Errorf("theme name required")
	}
	if err := os.MkdirAll(ThemesDir(), 0o755); err != nil {
		return err
	}
	data, err := theme.MarshalTheme(t)
	if err != nil {
		return err
	}
	return os.WriteFile(themePath(t.Name), data, 0o644)
}

// DeleteCustomTheme removes a custom theme file.
func DeleteCustomTheme(name string) error { return os.Remove(themePath(name)) }

// ImportTheme reads a yaml theme from an arbitrary path, names it after the file
// base, and copies it into ThemesDir.
func ImportTheme(path string) (theme.Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return theme.Theme{}, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	t, err := theme.UnmarshalTheme(name, data)
	if err != nil {
		return theme.Theme{}, err
	}
	if err := SaveCustomTheme(t); err != nil {
		return theme.Theme{}, err
	}
	return t, nil
}
